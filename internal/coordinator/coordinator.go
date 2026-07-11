package coordinator

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/foreman/foreman/internal/adapter"
	"github.com/foreman/foreman/internal/controlplane"
	"github.com/foreman/foreman/internal/eventbus"
	"github.com/foreman/foreman/internal/mcphub"
	"github.com/foreman/foreman/internal/sandbox"
	"github.com/foreman/foreman/internal/schemas"
)

// Coordinator receives tasks, provisions sandboxes, and runs agents.
type Coordinator struct {
	bus           eventbus.EventBus
	cp            *controlplane.ControlPlane
	sbox          sandbox.Sandbox
	mcpHub        mcphub.MCPHub
	adapters      map[string]adapter.AgentAdapter
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
		maxConcurrent: maxConcurrent,
		active:        make(map[string]context.CancelFunc),
	}
}

// SubmitTask creates a session and starts running the task asynchronously.
func (c *Coordinator) SubmitTask(ctx context.Context, taskID, description string) error {
	sessionID := fmt.Sprintf("ses_%s", taskID)
	if err := c.cp.CreateSession(ctx, sessionID, taskID); err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return c.startTask(ctx, sessionID, description)
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

	// --- Parse stdout and publish events ---
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
			c.publishAgentEvent(ctx, sessionID, parsed)
		}
	}

	// --- Handle result ---
	if result.ExitCode != 0 {
		c.failSession(ctx, sessionID, fmt.Errorf("agent exited with code %d: %s",
			result.ExitCode, strings.TrimSpace(result.Stderr)))
	} else {
		if err := c.cp.Transition(ctx, sessionID, schemas.StatusCompleted); err != nil {
			log.Printf("coordinator: transition to completed: %v", err)
		}
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
