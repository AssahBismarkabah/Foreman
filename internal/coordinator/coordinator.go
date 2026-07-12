package coordinator

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/foreman/foreman/internal/adapter"
	"github.com/foreman/foreman/internal/controlplane"
	"github.com/foreman/foreman/internal/eventbus"
	"github.com/foreman/foreman/internal/mcphub"
	"github.com/foreman/foreman/internal/policy"
	"github.com/foreman/foreman/internal/sandbox"
	"github.com/foreman/foreman/internal/schemas"
)

// ApprovalResponse is sent through a channel when a human approves or denies.
type ApprovalResponse struct {
	Approved bool
	UserID   string
	Reason   string
}

// Coordinator receives tasks, provisions sandboxes, and runs agents.
type Coordinator struct {
	bus           eventbus.EventBus
	cp            *controlplane.ControlPlane
	sbox          sandbox.Sandbox
	mcpHub        mcphub.MCPHub
	adapters      map[string]adapter.AgentAdapter
	policies      []policy.Policy
	maxConcurrent int
	active        map[string]context.CancelFunc
	mu            sync.Mutex
}

// New creates a Coordinator.
func New(
	bus eventbus.EventBus,
	cp *controlplane.ControlPlane,
	sbox sandbox.Sandbox,
	mcpHub mcphub.MCPHub,
	adapters []adapter.AgentAdapter,
	policies []policy.Policy,
	maxConcurrent int,
) *Coordinator {
	adapterMap := make(map[string]adapter.AgentAdapter)
	for _, a := range adapters {
		adapterMap[a.Name()] = a
	}
	return &Coordinator{
		bus:           bus,
		cp:            cp,
		sbox:          sbox,
		mcpHub:        mcpHub,
		adapters:      adapterMap,
		policies:      policies,
		maxConcurrent: maxConcurrent,
		active:        make(map[string]context.CancelFunc),
	}
}

// SubmitTask creates a session and starts running the task asynchronously.
func (c *Coordinator) SubmitTask(ctx context.Context, taskID, description string) error {
	sessionID := fmt.Sprintf("ses_%s", taskID)
	if err := c.cp.CreateSession(ctx, sessionID, taskID, description); err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return c.startTask(ctx, sessionID, description)
}

// ResumeSession restarts a session that was recovered after a restart.
// It transitions the session back to ALLOCATING and launches a new agent.
// The session must already exist in the ControlPlane (loaded by Recover).
func (c *Coordinator) ResumeSession(ctx context.Context, sessionID, description string) error {
	c.mu.Lock()
	if len(c.active) >= c.maxConcurrent {
		c.mu.Unlock()
		return fmt.Errorf("max concurrent tasks reached (%d)", c.maxConcurrent)
	}
	c.mu.Unlock()

	if err := c.cp.Transition(ctx, sessionID, schemas.StatusAllocating); err != nil {
		return fmt.Errorf("transition to allocating: %w", err)
	}

	if _, err := c.mcpHub.ResolveTools(ctx, sessionID); err != nil {
		c.failSession(ctx, sessionID, fmt.Errorf("resolve tools: %w", err))
		return fmt.Errorf("resolve tools: %w", err)
	}

	runCtx, cancel := context.WithCancel(ctx)
	c.mu.Lock()
	c.active[sessionID] = cancel
	c.mu.Unlock()

	go c.runAgent(runCtx, sessionID, description)
	return nil
}

// startTask transitions to ALLOCATING and launches the agent goroutine.
func (c *Coordinator) startTask(ctx context.Context, sessionID, description string) error {
	c.mu.Lock()
	if len(c.active) >= c.maxConcurrent {
		c.mu.Unlock()
		return fmt.Errorf("max concurrent tasks reached (%d)", c.maxConcurrent)
	}
	c.mu.Unlock()

	if err := c.cp.Transition(ctx, sessionID, schemas.StatusAllocating); err != nil {
		return fmt.Errorf("transition to allocating: %w", err)
	}

	if _, err := c.mcpHub.ResolveTools(ctx, sessionID); err != nil {
		c.failSession(ctx, sessionID, fmt.Errorf("resolve tools: %w", err))
		return fmt.Errorf("resolve tools: %w", err)
	}

	runCtx, cancel := context.WithCancel(ctx)
	c.mu.Lock()
	c.active[sessionID] = cancel
	c.mu.Unlock()

	go c.runAgent(runCtx, sessionID, description)

	return nil
}

// runAgent is the core goroutine: provision sandbox, run agent, parse output,
// handle completion or failure, and clean up.
func (c *Coordinator) runAgent(ctx context.Context, sessionID, description string) {
	defer func() {
		c.mu.Lock()
		delete(c.active, sessionID)
		c.mu.Unlock()
	}()

	// --- Pick the first configured adapter ---
	var agent adapter.AgentAdapter
	for _, a := range c.adapters {
		agent = a
		break
	}
	if agent == nil {
		c.failSession(ctx, sessionID, fmt.Errorf("no agent adapter configured"))
		return
	}

	// --- Verify the adapter binary ---
	if err := agent.Verify(ctx); err != nil {
		c.failSession(ctx, sessionID, fmt.Errorf("adapter verify: %w", err))
		return
	}

	// --- Build the agent command ---
	args, err := agent.BuildConfig(ctx, adapter.BuildConfig{
		TaskDescription: description,
		SessionID:       sessionID,
	})
	if err != nil {
		c.failSession(ctx, sessionID, fmt.Errorf("build config: %w", err))
		return
	}

	// --- Provision a sandbox ---
	sandboxID, err := c.sbox.Provision(ctx, sandbox.SandboxSpec{
		WorkDir: "/workspace",
	})
	if err != nil {
		c.failSession(ctx, sessionID, fmt.Errorf("provision sandbox: %w", err))
		return
	}

	// --- Transition to RUNNING ---
	if err := c.cp.Transition(ctx, sessionID, schemas.StatusRunning); err != nil {
		c.cleanupSandbox(ctx, sandboxID)
		log.Printf("coordinator: transition to running: %v", err)
		return
	}

	// --- Execute the agent in the sandbox ---
	result, err := c.sbox.Execute(ctx, sandboxID, args, 0)
	if err != nil {
		c.failSession(ctx, sessionID, fmt.Errorf("execute agent: %w", err))
		c.cleanupSandbox(ctx, sandboxID)
		return
	}

	// --- Parse stdout and publish events; collect tool_use for policy check ---
	var toolCalls []policy.ToolUse
	if result.Stdout != "" {
		for _, line := range strings.Split(result.Stdout, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			parsed, parseErr := agent.ParseEvent(ctx, []byte(line))
			if parseErr != nil || parsed == nil {
				continue
			}
			// Extract tool_use events for policy matching from any adapter output
			if tc := extractToolUse(parsed); tc != nil {
				toolCalls = append(toolCalls, *tc)
			}
			c.publishAgentEvent(ctx, sessionID, parsed)
		}
	}

	// --- Handle result + optional approval ---
	if result.ExitCode != 0 {
		c.failSession(ctx, sessionID, fmt.Errorf("agent exited with code %d: %s",
			result.ExitCode, strings.TrimSpace(result.Stderr)))
		c.cleanupSandbox(ctx, sandboxID)
		return
	}

	// Check if any tool calls match approval policies.
	if matched := c.checkPolicies(toolCalls); len(matched) > 0 {
		if err := c.cp.Transition(ctx, sessionID, schemas.StatusApproval); err != nil {
			log.Printf("coordinator: transition to approval: %v", err)
			c.cleanupSandbox(ctx, sandboxID)
			return
		}
		c.publishApprovalRequest(ctx, sessionID, matched)
		if err := c.waitForApproval(ctx, sessionID, matched[0].Policy.DefaultTimeout()); err != nil {
			c.failSession(ctx, sessionID, fmt.Errorf("approval: %w", err))
			c.cleanupSandbox(ctx, sandboxID)
			return
		}
	}

	if err := c.cp.Transition(ctx, sessionID, schemas.StatusCompleted); err != nil {
		log.Printf("coordinator: transition to completed: %v", err)
	}

	// --- Cleanup ---
	c.cleanupSandbox(ctx, sandboxID)
}

// Start subscribes to task.submitted events and dispatches them.
func (c *Coordinator) Start(ctx context.Context) error {
	cancel, err := c.bus.Subscribe(ctx, schemas.Subject("task", "submitted"),
		func(ctx context.Context, evt schemas.Event) error {
			pay, ok := evt.Payload.(schemas.TaskPayload)
			if !ok {
				return fmt.Errorf("unexpected payload type %T", evt.Payload)
			}
			go func() {
				if err := c.SubmitTask(ctx, pay.TaskID, pay.Description); err != nil {
					log.Printf("coordinator: submit task %s: %v", pay.TaskID, err)
				}
			}()
			return nil
		},
	)
	if err != nil {
		return fmt.Errorf("subscribe to task.submitted: %w", err)
	}
	defer cancel()
	<-ctx.Done()
	return nil
}

// --- helpers ---

func (c *Coordinator) failSession(ctx context.Context, sessionID string, err error) {
	log.Printf("coordinator: session %s failed: %v", sessionID, err)
	if tErr := c.cp.Transition(ctx, sessionID, schemas.StatusFailed); tErr != nil {
		log.Printf("coordinator: failed to mark session %s as failed: %v", sessionID, tErr)
	}
}

func (c *Coordinator) cleanupSandbox(ctx context.Context, sandboxID string) {
	if err := c.sbox.Destroy(ctx, sandboxID); err != nil {
		log.Printf("coordinator: destroy sandbox %s: %v", sandboxID, err)
	}
}

func (c *Coordinator) publishAgentEvent(ctx context.Context, sessionID string, parsed any) {
	evt := schemas.Event{
		ID:        fmt.Sprintf("%s-%d", sessionID, time.Now().UnixNano()),
		Type:      schemas.EvAgentOutput,
		SessionID: sessionID,
		Timestamp: time.Now(),
		Payload:   parsed,
	}
	subject := schemas.Subject("agent", sessionID, "output")
	if err := c.bus.Publish(ctx, subject, evt); err != nil {
		log.Printf("coordinator: publish agent event: %v", err)
	}
}

// --- Approval helpers ---

// extractToolUse attempts to parse a tool_use event from the adapter output.
func extractToolUse(parsed any) *policy.ToolUse {
	// Try the OpenCode adapter format (*adapter.OpenCodeEvent)
	type toolEvent interface{ ToolUse() bool }
	if tu, ok := parsed.(toolEvent); ok && tu.ToolUse() {
		return nil // handled below via JSON extraction
	}

	// Fall back to generic JSON extraction: any type with a "tool" field
	// that looks like a tool_use event.
	b, err := json.Marshal(parsed)
	if err != nil {
		return nil
	}
	var raw struct {
		Type string `json:"type"`
		Name string `json:"name"`
		Part *struct {
			Tool  string         `json:"tool"`
			State map[string]any `json:"state"`
		} `json:"part"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil
	}
	if raw.Type != "tool_use" {
		return nil
	}
	if raw.Part == nil || raw.Part.Tool == "" {
		return nil
	}
	return &policy.ToolUse{
		Tool:   raw.Part.Tool,
		Inputs: raw.Part.State,
	}
}

// checkPolicies returns all policy matches found in the tool calls.
func (c *Coordinator) checkPolicies(toolCalls []policy.ToolUse) []policy.MatchResult {
	if len(c.policies) == 0 || len(toolCalls) == 0 {
		return nil
	}
	var matches []policy.MatchResult
	for _, tc := range toolCalls {
		for _, p := range c.policies {
			if p.Action != "require_approval" {
				continue
			}
			if p.Matches(tc.Tool, tc.Inputs) {
				matches = append(matches, policy.MatchResult{
					Policy:  p,
					ToolUse: tc,
				})
			}
		}
	}
	return matches
}

// publishApprovalRequest sends an EvApprovalRequest event so plugins can
// present the prompt to the user.
func (c *Coordinator) publishApprovalRequest(ctx context.Context, sessionID string, matches []policy.MatchResult) {
	toolNames := make([]string, 0, len(matches))
	for _, m := range matches {
		toolNames = append(toolNames, m.ToolUse.Tool)
	}

	evt := schemas.Event{
		ID:        fmt.Sprintf("approval-req-%s-%d", sessionID, time.Now().UnixNano()),
		Type:      schemas.EvApprovalRequest,
		SessionID: sessionID,
		Timestamp: time.Now(),
		Payload: map[string]any{
			"session_id":  sessionID,
			"tool_names":  toolNames,
			"policy_name": matches[0].Policy.Name,
		},
	}
	subject := schemas.Subject("approval", sessionID, "request")
	if err := c.bus.Publish(ctx, subject, evt); err != nil {
		log.Printf("coordinator: publish approval request: %v", err)
	}
}

// waitForApproval blocks until the session is approved, denied, or times out.
func (c *Coordinator) waitForApproval(ctx context.Context, sessionID string, timeout time.Duration) error {
	respCh := make(chan *ApprovalResponse, 1)
	defer close(respCh)

	// Subscribe to approval granted/denied events for this session.
	grantedCancel, err := c.bus.Subscribe(ctx,
		schemas.Subject("approval", sessionID, "granted"),
		func(ctx context.Context, evt schemas.Event) error {
			respCh <- &ApprovalResponse{
				Approved: true,
				UserID:   extractUserID(evt.Payload),
			}
			return nil
		},
	)
	if err != nil {
		return fmt.Errorf("subscribe approval.granted: %w", err)
	}
	defer grantedCancel()

	deniedCancel, err := c.bus.Subscribe(ctx,
		schemas.Subject("approval", sessionID, "denied"),
		func(ctx context.Context, evt schemas.Event) error {
			respCh <- &ApprovalResponse{
				Approved: false,
				UserID:   extractUserID(evt.Payload),
			}
			return nil
		},
	)
	if err != nil {
		return fmt.Errorf("subscribe approval.denied: %w", err)
	}
	defer deniedCancel()

	log.Printf("coordinator: waiting for approval on session %s (timeout %v)", sessionID, timeout)

	select {
	case resp := <-respCh:
		if resp.Approved {
			log.Printf("coordinator: session %s approved by %s", sessionID, resp.UserID)
			return nil
		}
		return fmt.Errorf("denied by %s", resp.UserID)
	case <-time.After(timeout):
		return fmt.Errorf("approval timed out after %v", timeout)
	case <-ctx.Done():
		return ctx.Err()
	}
}

// extractUserID pulls a user_id from a payload that may be a map or a string.
func extractUserID(payload any) string {
	switch p := payload.(type) {
	case map[string]any:
		if uid, ok := p["user_id"].(string); ok {
			return uid
		}
	case map[string]string:
		return p["user_id"]
	case string:
		return p
	}
	return "unknown"
}
