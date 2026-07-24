package coordinator

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"math/rand"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/foreman/foreman/internal/adapter"
	"github.com/foreman/foreman/internal/controlplane"
	"github.com/foreman/foreman/internal/eventbus"
	"github.com/foreman/foreman/internal/identity"
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
	bus                 eventbus.EventBus
	cp                  *controlplane.ControlPlane
	sbox                sandbox.Sandbox
	mcpHub              mcphub.MCPHub
	adapters            map[string]adapter.AgentAdapter
	adapterList         []adapter.AgentAdapter // ordered, for picking the first
	policies            []policy.Policy
	maxConcurrent       int
	active              map[string]context.CancelFunc
	tokenIssuer         *identity.Issuer     // optional, for scoped agent tokens
	heartbeatInterval   time.Duration        // how often to check sandbox liveness (0 = disabled)
	heartbeatTimeout    time.Duration        // max interval without heartbeat before declaring crash
	sessionHeartbeats   map[string]time.Time // sessionID -> last successful heartbeat time
	sessionAttempts     map[string]int       // sessionID -> current retry attempt count
	maxRetries          int                  // max crash recovery retries
	backoffBase         time.Duration        // base delay for exponential backoff
	backoffMultiplier   float64              // multiplier per retry attempt
	jitter              time.Duration        // random jitter added to backoff delay
	accepting           bool
	cpuAlertPercent     float64              // 0 = disabled
	memoryAlertPercent  float64              // 0 = disabled
	lastResourceAlert   map[string]time.Time // sessionID -> last alert time (debounce)
	lastResourceAlertMu sync.Mutex
	mu                  sync.Mutex
}

// New creates a Coordinator.
// heartbeatInterval and heartbeatTimeout control sandbox liveness monitoring.
// Pass 0 for heartbeatInterval to disable monitoring.
func New(
	bus eventbus.EventBus,
	cp *controlplane.ControlPlane,
	sbox sandbox.Sandbox,
	mcpHub mcphub.MCPHub,
	adapters []adapter.AgentAdapter,
	policies []policy.Policy,
	maxConcurrent int,
	tokenIssuer *identity.Issuer,
	heartbeatInterval time.Duration,
	heartbeatTimeout time.Duration,
	maxRetries int,
	backoffBase time.Duration,
	backoffMultiplier float64,
	jitter time.Duration,
	cpuAlertPercent float64,
	memoryAlertPercent float64,
) *Coordinator {
	adapterMap := make(map[string]adapter.AgentAdapter)
	adapterList := make([]adapter.AgentAdapter, 0, len(adapters))
	for _, a := range adapters {
		adapterMap[a.Name()] = a
		adapterList = append(adapterList, a)
	}
	if heartbeatInterval <= 0 {
		heartbeatInterval = 0 // disabled
	}
	if heartbeatTimeout <= 0 {
		heartbeatTimeout = 60 * time.Second // default
	}
	if maxRetries <= 0 {
		maxRetries = 3
	}
	if backoffBase <= 0 {
		backoffBase = 5 * time.Second
	}
	if backoffMultiplier <= 0 {
		backoffMultiplier = 2.0
	}
	if jitter <= 0 {
		jitter = 1 * time.Second
	}
	if maxConcurrent <= 0 {
		maxConcurrent = 1
	}
	return &Coordinator{
		bus:                bus,
		cp:                 cp,
		sbox:               sbox,
		mcpHub:             mcpHub,
		adapters:           adapterMap,
		adapterList:        adapterList,
		policies:           policies,
		maxConcurrent:      maxConcurrent,
		active:             make(map[string]context.CancelFunc),
		tokenIssuer:        tokenIssuer,
		heartbeatInterval:  heartbeatInterval,
		heartbeatTimeout:   heartbeatTimeout,
		sessionHeartbeats:  make(map[string]time.Time),
		sessionAttempts:    make(map[string]int),
		maxRetries:         maxRetries,
		backoffBase:        backoffBase,
		backoffMultiplier:  backoffMultiplier,
		jitter:             jitter,
		accepting:          true,
		cpuAlertPercent:    cpuAlertPercent,
		memoryAlertPercent: memoryAlertPercent,
		lastResourceAlert:  make(map[string]time.Time),
	}
}

// StopAccepting prevents new tasks from being submitted. Active tasks
// continue running. This is the first phase of graceful shutdown.
func (c *Coordinator) StopAccepting() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.accepting = false
}

// ActiveCount returns the number of currently active task sessions.
func (c *Coordinator) ActiveCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.active)
}

// Drain blocks until all active sessions have completed or the context is
// cancelled. Returns the number of sessions still active when the context
// expired (0 means all sessions completed). This is the second phase of
// graceful shutdown.
func (c *Coordinator) Drain(ctx context.Context) int {
	for {
		count := c.ActiveCount()
		if count == 0 {
			return 0
		}
		select {
		case <-ctx.Done():
			remaining := c.ActiveCount()
			log.Printf("coordinator: drain timeout reached, %d session(s) still active", remaining)
			return remaining
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// SessionInfo holds a snapshot of a session's current state, safe for API serialization.
type SessionInfo struct {
	ID          string `json:"id"`
	Status      string `json:"status"`
	TaskID      string `json:"task_id"`
	Description string `json:"description"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

// GetSession returns a snapshot of the session with the given ID.
// Returns nil if the session does not exist.
func (c *Coordinator) GetSession(sessionID string) *SessionInfo {
	s, ok := c.cp.GetSession(sessionID)
	if !ok {
		return nil
	}
	return &SessionInfo{
		ID:          s.ID,
		Status:      string(s.Status),
		TaskID:      s.TaskID,
		Description: s.Description,
		CreatedAt:   s.CreatedAt.Format(time.RFC3339),
		UpdatedAt:   s.UpdatedAt.Format(time.RFC3339),
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
// The session is already in RUNNING state (recovered from DB), so it skips
// the CREATED->ALLOCATING transition (which would be invalid from RUNNING)
// and directly resolves tools and launches a new agent goroutine.
// runAgent handles the RUNNING->RUNNING idempotency internally.
func (c *Coordinator) ResumeSession(ctx context.Context, sessionID, description string) error {
	c.mu.Lock()
	if !c.accepting {
		c.mu.Unlock()
		return fmt.Errorf("not accepting new tasks (shutting down)")
	}
	if len(c.active) >= c.maxConcurrent {
		c.mu.Unlock()
		return fmt.Errorf("max concurrent tasks reached (%d)", c.maxConcurrent)
	}
	c.mu.Unlock()

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
	if !c.accepting {
		c.mu.Unlock()
		return fmt.Errorf("not accepting new tasks (shutting down)")
	}
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

	// --- Pick the first configured adapter (ordered by config) ---
	var agent adapter.AgentAdapter
	if len(c.adapterList) > 0 {
		agent = c.adapterList[0]
	}
	if agent == nil {
		c.failSession(ctx, sessionID, fmt.Errorf("no agent adapter configured"))
		return
	}

	c.cp.Audit(ctx, sessionID, "coordinator.adapter_selected", map[string]any{
		"adapter": agent.Name(),
	})

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

	// --- Generate scoped agent token (if issuer is configured) ---
	env := map[string]string{}

	// Pass through LLM provider env vars from the host so the agent (e.g.
	// opencode) can call the LLM API inside the sandbox container.
	for _, k := range []string{
		"OPENAI_API_KEY",
		"OPENAI_BASE_URL",
		"OPENAI_MODEL",
		"ANTHROPIC_API_KEY",
		"ANTHROPIC_BASE_URL",
		"GOOGLE_API_KEY",
		"OPENROUTER_API_KEY",
	} {
		if v := os.Getenv(k); v != "" {
			env[k] = v
		}
	}

	if c.tokenIssuer != nil {
		scope := &identity.AgentScope{
			Actions: []string{"read", "pull"},
		}
		token, tokenErr := c.tokenIssuer.IssueScopedAgentToken(ctx, sessionID, "", 30*time.Minute, scope)
		if tokenErr != nil {
			c.failSession(ctx, sessionID, fmt.Errorf("issue scoped token: %w", tokenErr))
			return
		}
		env["FOREMAN_AGENT_TOKEN"] = token
	}

	// --- Provision a sandbox ---
	sandboxID, err := c.sbox.Provision(ctx, sandbox.SandboxSpec{
		WorkDir: "/workspace",
		Env:     env,
	})
	if err != nil {
		c.failSession(ctx, sessionID, fmt.Errorf("provision sandbox: %w", err))
		return
	}

	c.cp.Audit(ctx, sessionID, "coordinator.sandbox_provisioned", map[string]any{
		"sandbox_id": sandboxID,
		"agent":      agent.Name(),
	})

	// --- Transition to RUNNING ---
	// Idempotent -- skip if session is already RUNNING (e.g. after a crash retry
	// where the session was never transitioned to a terminal state).
	if s, ok := c.cp.GetSession(sessionID); !ok || s.Status != schemas.StatusRunning {
		if err := c.cp.Transition(ctx, sessionID, schemas.StatusRunning); err != nil {
			c.cleanupSandbox(ctx, sandboxID)
			log.Printf("coordinator: transition to running: %v", err)
			return
		}
	}

	// --- Save baseline checkpoint ---
	baselineSnapshot, _ := json.Marshal(map[string]any{
		"session_id":  sessionID,
		"description": description,
		"sandbox_id":  sandboxID,
		"step":        "allocated",
		"agent":       agent.Name(),
	})
	if _, cpErr := c.cp.SaveCheckpoint(ctx, sessionID, baselineSnapshot); cpErr != nil {
		log.Printf("coordinator: save baseline checkpoint: %v", cpErr)
		// Non-fatal -- continue even if checkpoint fails
	}

	// --- Start heartbeat monitor (if enabled) ---
	heartbeatCtx, heartbeatCancel := context.WithCancel(ctx)
	defer heartbeatCancel()

	crashCh := make(chan error, 1)
	if c.heartbeatInterval > 0 {
		go c.monitorHeartbeat(heartbeatCtx, sessionID, sandboxID, crashCh, heartbeatCancel)
	}

	// --- Start resource monitor (if thresholds configured) ---
	if c.cpuAlertPercent > 0 || c.memoryAlertPercent > 0 {
		go c.monitorResources(heartbeatCtx, sessionID, sandboxID)
	}

	// --- Execute the agent in the sandbox ---
	// The heartbeat monitor can cancel heartbeatCtx to interrupt the agent
	// if the sandbox stops responding.

	// DEBUG: log env vars being passed to sandbox
	log.Printf("DEBUG sandbox env: %v", env)

	// DEBUG: exec env in sandbox to verify env vars are set
	if dbg, err := c.sbox.Execute(heartbeatCtx, sandboxID, []string{"env"}, 5*time.Second); err == nil {
		log.Printf("DEBUG sandbox container env:\n%s", dbg.Stdout)
	} else {
		log.Printf("DEBUG sandbox env exec failed: %v", err)
	}

	result, execErr := c.sbox.Execute(heartbeatCtx, sandboxID, args, 0)

	// Check if execution was interrupted by heartbeat failure.
	select {
	case hbErr := <-crashCh:
		c.cp.Audit(ctx, sessionID, "coordinator.agent_crashed", map[string]any{
			"error": hbErr.Error(),
		})
		if c.handleCrash(ctx, sessionID, description, sandboxID, hbErr) {
			return // retry launched in new goroutine
		}
		return // retries exhausted
	default:
	}

	if execErr != nil {
		c.failSession(ctx, sessionID, fmt.Errorf("execute agent: %w", execErr))
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

	// If the session was in APPROVAL, transition back through RUNNING first.
	if s, ok := c.cp.GetSession(sessionID); ok && s.Status == schemas.StatusApproval {
		if err := c.cp.Transition(ctx, sessionID, schemas.StatusRunning); err != nil {
			log.Printf("coordinator: transition to running after approval: %v", err)
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

// monitorHeartbeat runs in a goroutine per active session. It calls
// sbox.Heartbeat on every tick. After heartbeatTimeout worth of consecutive
// failures it sends the error to crashCh and calls heartbeatCancel to
// interrupt the agent execution. Returns immediately if monitoring is
// disabled (heartbeatInterval <= 0).
func (c *Coordinator) monitorHeartbeat(ctx context.Context, sessionID, sandboxID string, crashCh chan<- error, heartbeatCancel context.CancelFunc) {
	if c.heartbeatInterval <= 0 {
		log.Printf("coordinator: heartbeat disabled for session %s (interval=%v)", sessionID, c.heartbeatInterval)
		return
	}

	interval := c.heartbeatInterval
	maxFailures := int(c.heartbeatTimeout / interval)
	if maxFailures < 1 {
		maxFailures = 3
	}

	log.Printf("coordinator: heartbeat monitor started for session %s (interval=%v, timeout=%v, maxFailures=%d)", sessionID, interval, c.heartbeatTimeout, maxFailures)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	consecutiveFailures := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			hbErr := c.sbox.Heartbeat(ctx, sandboxID)
			if hbErr != nil {
				consecutiveFailures++
				log.Printf("coordinator: heartbeat %d/%d failed for session %s: %v",
					consecutiveFailures, maxFailures, sessionID, hbErr)
				if consecutiveFailures >= maxFailures {
					select {
					case crashCh <- fmt.Errorf("heartbeat lost after %d failures: %w", consecutiveFailures, hbErr):
					default:
					}
					heartbeatCancel() // interrupt the blocking sbox.Execute
					return
				}
			} else {
				consecutiveFailures = 0
				c.mu.Lock()
				c.sessionHeartbeats[sessionID] = time.Now()
				c.mu.Unlock()
			}
		}
	}
}

// monitorResources runs alongside the heartbeat monitor and periodically
// checks sandbox resource usage (CPU, memory). If thresholds are set and
// breached, it emits an audit event and logs a warning. Alerts are debounced
// to at most once per 60s per session. Returns immediately if resource
// thresholds are disabled (cpuAlertPercent == 0 && memoryAlertPercent == 0).
func (c *Coordinator) monitorResources(ctx context.Context, sessionID, sandboxID string) {
	if c.cpuAlertPercent <= 0 && c.memoryAlertPercent <= 0 {
		return
	}
	// Check every 60s — Docker stats is not free to poll.
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Stats is an optional interface -- check at runtime.
			statsProvider, statsOk := c.sbox.(sandbox.StatsProvider)
			if !statsOk {
				return
			}
			usage, err := statsProvider.Stats(ctx, sandboxID)
			if err != nil {
				log.Printf("coordinator: resource stats for session %s: %v", sessionID, err)
				continue
			}

			var alerts []string
			if c.cpuAlertPercent > 0 && usage.CPUPercent > c.cpuAlertPercent {
				alerts = append(alerts, fmt.Sprintf("CPU %.1f%% > %.1f%%", usage.CPUPercent, c.cpuAlertPercent))
			}
			if c.memoryAlertPercent > 0 {
				memPct := float64(usage.MemoryBytes) / float64(usage.MemoryLimit) * 100
				if memPct > c.memoryAlertPercent {
					alerts = append(alerts, fmt.Sprintf("memory %.1f%% > %.1f%%", memPct, c.memoryAlertPercent))
				}
			}

			if len(alerts) == 0 {
				continue
			}

			// Debounce: don't emit alerts more than once per 60s per session.
			c.lastResourceAlertMu.Lock()
			last, has := c.lastResourceAlert[sessionID]
			now := time.Now()
			if has && now.Sub(last) < 60*time.Second {
				c.lastResourceAlertMu.Unlock()
				continue
			}
			c.lastResourceAlert[sessionID] = now
			c.lastResourceAlertMu.Unlock()

			log.Printf("coordinator: resource alert for session %s: %s", sessionID, strings.Join(alerts, ", "))
			c.cp.Audit(ctx, sessionID, "coordinator.resource_alert", map[string]any{
				"alerts": alerts,
			})
		}
	}
}

// backoffDuration computes the delay before a retry attempt using
// exponential backoff with jitter: base * multiplier^attempt + rand(jitter).
func (c *Coordinator) backoffDuration(attempt int) time.Duration {
	base := float64(c.backoffBase)
	delay := base * math.Pow(c.backoffMultiplier, float64(attempt-1))
	jitter := time.Duration(rand.Int63n(int64(c.jitter)))
	return time.Duration(delay) + jitter
}

// handleCrash is called when heartbeat monitoring detects a sandbox crash.
// It saves a crash checkpoint, cleans up the dead sandbox, and either
// retries with exponential backoff (returning true) or fails the session
// when retries are exhausted (returning false).
func (c *Coordinator) handleCrash(ctx context.Context, sessionID, description, sandboxID string, crashErr error) bool {
	c.mu.Lock()
	c.sessionAttempts[sessionID]++
	attempt := c.sessionAttempts[sessionID]
	c.mu.Unlock()

	// Save crash checkpoint.
	crashSnapshot, _ := json.Marshal(map[string]any{
		"session_id": sessionID,
		"step":       "crashed",
		"error":      crashErr.Error(),
		"attempt":    attempt,
	})
	if _, cpErr := c.cp.SaveCheckpoint(ctx, sessionID, crashSnapshot); cpErr != nil {
		log.Printf("coordinator: save crash checkpoint: %v", cpErr)
	}

	// Clean up the dead sandbox.
	c.cleanupSandbox(ctx, sandboxID)

	if attempt > c.maxRetries {
		c.cp.Audit(ctx, sessionID, "coordinator.retries_exhausted", map[string]any{
			"error":   crashErr.Error(),
			"attempt": attempt,
		})
		c.failSession(ctx, sessionID, fmt.Errorf("agent crashed, retries exhausted after %d attempts: %w", attempt, crashErr))
		return false
	}

	// Exponential backoff with jitter.
	delay := c.backoffDuration(attempt)
	log.Printf("coordinator: session %s crashed (attempt %d/%d), retrying in %v",
		sessionID, attempt, c.maxRetries, delay)

	c.cp.Audit(ctx, sessionID, "coordinator.agent_retrying", map[string]any{
		"attempt": attempt,
		"delay":   delay.String(),
	})

	select {
	case <-ctx.Done():
		return false
	case <-time.After(delay):
	}

	// Retry in a new goroutine with the same context (cancellation chain intact).
	go c.runAgent(ctx, sessionID, description)
	return true
}

// ReapOrphanedContainers removes any Docker containers left behind by a previous
// Foreman instance. It lists all containers with the foreman-sbox- prefix and
// destroys those whose session IDs are not in the provided active set.
// Call this on bootstrap before starting new sessions.
func (c *Coordinator) ReapOrphanedContainers(ctx context.Context, activeSessions map[string]bool) {
	// Get the Docker API client from the sandbox if it supports it.
	dockerSbox, ok := c.sbox.(*sandbox.DockerSandbox)
	if !ok {
		log.Printf("coordinator: sandbox is not Docker-based, skipping container reaping")
		return
	}
	apiClient := dockerSbox.APIClient()

	containers, err := apiClient.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		log.Printf("coordinator: list containers for reaping: %v", err)
		return
	}
	for _, ctr := range containers {
		// Only consider containers with the foreman-sbox- prefix.
		if !strings.HasPrefix(ctr.Names[0], "/foreman-sbox-") {
			continue
		}
		name := strings.TrimPrefix(ctr.Names[0], "/")
		// Skip containers that belong to active sessions.
		if activeSessions[name] {
			continue
		}
		log.Printf("coordinator: reaping orphaned container %s (id=%s)", name, ctr.ID[:12])
		// Use the Docker API directly -- Destroy requires the container
		// to be in the in-memory sandbox map, which orphaned containers aren't.
		if dErr := apiClient.ContainerRemove(ctx, ctr.ID, container.RemoveOptions{Force: true}); dErr != nil {
			log.Printf("coordinator: reap container %s: %v", name, dErr)
		}
	}
}

// --- helpers ---

func (c *Coordinator) failSession(ctx context.Context, sessionID string, err error) {
	log.Printf("coordinator: session %s failed: %v", sessionID, err)
	c.cp.Audit(ctx, sessionID, "coordinator.session_failed", map[string]any{
		"error": err.Error(),
	})
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

	c.cp.Audit(ctx, sessionID, "coordinator.approval_requested", map[string]any{
		"tools":       toolNames,
		"policy_name": matches[0].Policy.Name,
	})

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
			c.cp.Audit(ctx, sessionID, "coordinator.approval_granted", map[string]any{
				"user_id": resp.UserID,
			})
			return nil
		}
		log.Printf("coordinator: session %s denied by %s: %s", sessionID, resp.UserID, resp.Reason)
		c.cp.Audit(ctx, sessionID, "coordinator.approval_denied", map[string]any{
			"user_id": resp.UserID,
			"reason":  resp.Reason,
		})
		return fmt.Errorf("denied by %s: %s", resp.UserID, resp.Reason)
	case <-time.After(timeout):
		c.cp.Audit(ctx, sessionID, "coordinator.approval_timed_out", nil)
		return fmt.Errorf("approval timed out after %v", timeout)
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ApproveSession publishes an approval.granted event for the given session.
// This unblocks waitForApproval so the session can continue to COMPLETED.
func (c *Coordinator) ApproveSession(ctx context.Context, sessionID, userID string) error {
	evt := schemas.Event{
		ID:        fmt.Sprintf("app_%s_%d", sessionID, time.Now().UnixNano()),
		Type:      schemas.EvApprovalGranted,
		SessionID: sessionID,
		Timestamp: time.Now(),
		Payload:   map[string]string{"user_id": userID},
	}
	return c.bus.Publish(ctx, schemas.Subject("approval", sessionID, "granted"), evt)
}

// DenySession publishes an approval.denied event for the given session.
func (c *Coordinator) DenySession(ctx context.Context, sessionID, userID, reason string) error {
	evt := schemas.Event{
		ID:        fmt.Sprintf("app_%s_%d", sessionID, time.Now().UnixNano()),
		Type:      schemas.EvApprovalDenied,
		SessionID: sessionID,
		Timestamp: time.Now(),
		Payload:   map[string]string{"user_id": userID, "reason": reason},
	}
	return c.bus.Publish(ctx, schemas.Subject("approval", sessionID, "denied"), evt)
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
