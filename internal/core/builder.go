package core

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/foreman/foreman/internal/adapter"
	"github.com/foreman/foreman/internal/api"
	"github.com/foreman/foreman/internal/config"
	"github.com/foreman/foreman/internal/controlplane"
	"github.com/foreman/foreman/internal/coordinator"
	"github.com/foreman/foreman/internal/eventbus"
	"github.com/foreman/foreman/internal/identity"
	"github.com/foreman/foreman/internal/identity/githubapp"
	"github.com/foreman/foreman/internal/mcphub"
	"github.com/foreman/foreman/internal/plugins"
	"github.com/foreman/foreman/internal/plugins/discord"
	"github.com/foreman/foreman/internal/plugins/slack"
	"github.com/foreman/foreman/internal/policy"
	"github.com/foreman/foreman/internal/sandbox"
	"github.com/foreman/foreman/internal/schemas"
	"github.com/foreman/foreman/internal/statestore"
)

// App holds all the wired subsystems.
type App struct {
	Config       *config.Config
	EventBus     eventbus.EventBus
	StateStore   statestore.StateStore
	Sandbox      sandbox.Sandbox
	MCPHub       mcphub.MCPHub
	ControlPlane *controlplane.ControlPlane
	Coordinator  *coordinator.Coordinator
	Plugins      []plugins.Plugin
	APIServer    *api.Server
}

// Bootstrap reads config, creates all subsystems, and wires them together.
func Bootstrap(ctx context.Context, cfg *config.Config) (_ *App, err error) {
	bus, err := newEventBus(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("eventbus: %w", err)
	}
	defer func() {
		if err != nil {
			if cerr := bus.Close(); cerr != nil {
				log.Printf("bootstrap: close eventbus: %v", cerr)
			}
		}
	}()

	store, err := newStateStore(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("statestore: %w", err)
	}
	defer func() {
		if err != nil && store != nil {
			store.Close()
		}
	}()

	sbox, err := newSandbox(cfg)
	if err != nil {
		return nil, fmt.Errorf("sandbox: %w", err)
	}

	mcp := newMCPHub(cfg)
	cp := controlplane.New(bus, store)

	// Recover non-terminal sessions from the State Store.
	recovered, err := cp.Recover(ctx)
	if err != nil {
		return nil, fmt.Errorf("recover sessions: %w", err)
	}

	adapters := newAdapters(cfg, bus)

	policies, err := newPolicies(cfg)
	if err != nil {
		return nil, fmt.Errorf("policies: %w", err)
	}

	iss := newIssuer(cfg)
	co := coordinator.New(
		bus, cp, sbox, mcp, adapters, policies,
		cfg.Subsystems.Coordinator.MaxConcurrent, iss,
		cfg.Subsystems.Coordinator.HeartbeatInterval,
		cfg.Subsystems.Coordinator.HeartbeatTimeout,
		cfg.Subsystems.Coordinator.MaxRetries,
		cfg.Subsystems.Coordinator.BackoffBase,
		cfg.Subsystems.Coordinator.BackoffMultiplier,
		cfg.Subsystems.Coordinator.Jitter,
		cfg.Subsystems.Coordinator.DefaultTimeout,
		parseFloat64(cfg.Subsystems.Sandbox.CPUAlertPercent, 0),
		parseFloat64(cfg.Subsystems.Sandbox.MemoryAlertPercent, 0),
	)

	// Reap orphaned Docker containers from previous runs.
	activeSessions := make(map[string]bool, len(recovered))
	for _, s := range recovered {
		activeSessions[s.ID] = true
	}
	co.ReapOrphanedContainers(ctx, activeSessions)

	srv, err := newAPIServer(ctx, cfg, bus, iss, co)
	if err != nil {
		return nil, fmt.Errorf("api server: %w", err)
	}
	if srv != nil {
		if err := srv.Start(ctx); err != nil {
			return nil, fmt.Errorf("api server start: %w", err)
		}
		log.Printf("bootstrap: api server listening on %s", cfg.Subsystems.Identity.API.ListenAddr)
	}

	// Recover non-terminal sessions with heartbeat age awareness.
	// ALLOCATING sessions never completed provisioning -> fail.
	// RUNNING sessions with recent heartbeat -> resume; stale -> fail.
	// APPROVAL sessions were orphaned -> cancel.
	heartbeatTimeout := cfg.Subsystems.Coordinator.HeartbeatTimeout
	if heartbeatTimeout <= 0 {
		heartbeatTimeout = 90 * time.Second
	}
	for _, s := range recovered {
		switch s.Status {
		case string(schemas.StatusAllocating):
			log.Printf("bootstrap: failing incomplete allocation session %s", s.ID)
			_ = cp.Transition(ctx, s.ID, schemas.StatusFailed)
			cp.Audit(ctx, s.ID, "recovery.allocating_session_failed", map[string]any{
				"reason": "allocation never completed before restart",
			})
		case string(schemas.StatusRunning):
			age := time.Since(s.UpdatedAt)
			if age > heartbeatTimeout {
				log.Printf("bootstrap: failing stale running session %s (age %v > timeout %v)", s.ID, age, heartbeatTimeout)
				_ = cp.Transition(ctx, s.ID, schemas.StatusFailed)
				cp.Audit(ctx, s.ID, "recovery.stale_session_failed", map[string]any{
					"age_seconds": age.Seconds(),
					"timeout":     heartbeatTimeout.Seconds(),
				})
			} else {
				log.Printf("bootstrap: resuming session %s (age %v)", s.ID, age)
				if err := co.ResumeSession(ctx, s.ID, s.Description); err != nil {
					log.Printf("bootstrap: resume session %s: %v", s.ID, err)
				}
			}
		case string(schemas.StatusApproval):
			// Approval request was lost in the crash. Cancel it.
			log.Printf("bootstrap: cancelling orphaned approval session %s", s.ID)
			_ = cp.Transition(ctx, s.ID, schemas.StatusCancelling)
			_ = cp.Transition(ctx, s.ID, schemas.StatusFailed)
			cp.Audit(ctx, s.ID, "recovery.orphaned_approval_cancelled", nil)
		case string(schemas.StatusCancelling):
			log.Printf("bootstrap: completing cancelled session %s", s.ID)
			_ = cp.Transition(ctx, s.ID, schemas.StatusFailed)
		default:
			log.Printf("bootstrap: leaving session %s in state %s (no auto-resume)", s.ID, s.Status)
		}
	}

	plugs := newPlugins(cfg)
	for _, p := range plugs {
		if err := p.Start(ctx, bus); err != nil {
			return nil, fmt.Errorf("plugin %s: start: %w", p.Name(), err)
		}
		log.Printf("bootstrap: plugin %s started", p.Name())
	}

	// Subscribe to plugin message subjects so communication plugins (Discord,
	// Slack) can submit tasks via the event bus. Each user.message event is
	// converted into a task and submitted to the coordinator.
	if co != nil {
		pluginSubjects := []string{
			schemas.Subject("plugin", "discord", "message"),
			schemas.Subject("plugin", "slack", "message"),
		}
		for _, subj := range pluginSubjects {
			if _, err := bus.Subscribe(ctx, subj,
				func(ctx context.Context, evt schemas.Event) error {
					msg, ok := evt.Payload.(schemas.UserMessage)
					if !ok {
						return fmt.Errorf("unexpected payload type %T for %s", evt.Payload, subj)
					}
					taskID := fmt.Sprintf("%s_%d", msg.Plugin, time.Now().UnixNano())
					log.Printf("bootstrap: submitting task from %s: %q", subj, msg.Text)
					go func() {
						if err := co.SubmitTask(ctx, taskID, msg.Text); err != nil {
							log.Printf("bootstrap: submit task from %s: %v", subj, err)
						}
					}()
					return nil
				},
			); err != nil {
				return nil, fmt.Errorf("subscribe to %s: %w", subj, err)
			}
		}
	}

	return &App{
		Config:       cfg,
		EventBus:     bus,
		StateStore:   store,
		Sandbox:      sbox,
		MCPHub:       mcp,
		ControlPlane: cp,
		Coordinator:  co,
		Plugins:      plugs,
		APIServer:    srv,
	}, nil
}

// newAPIServer creates the HTTP API server with identity/health/webhook and
// task management routes.
func newAPIServer(ctx context.Context, cfg *config.Config, _ eventbus.EventBus, iss *identity.Issuer, co *coordinator.Coordinator) (*api.Server, error) {
	idCfg := cfg.Subsystems.Identity

	// If no identity config is present, skip the API server.
	if (idCfg == identity.IdentityProviderConfig{}) {
		return nil, nil
	}

	listenAddr := idCfg.API.ListenAddr
	if listenAddr == "" {
		listenAddr = identity.DefaultListenAddr
	}

	srv := api.New(api.Config{
		ListenAddr: listenAddr,
	})

	srv.RegisterRoute("GET", "/healthz", healthHandler)

	// Identity endpoints
	if iss != nil {
		srv.RegisterRoute("GET", "/.well-known/jwks.json", iss.JWKSHandler())
		srv.RegisterRoute("GET", "/.well-known/openid-configuration",
			iss.OIDCConfigurationHandler(idCfg.API.PublicURL))
	}

	// Task management endpoints
	if co != nil {
		srv.RegisterRoute("POST", "/api/v1/tasks", handleSubmitTask(co))
		srv.RegisterRoute("GET", "/api/v1/sessions/{id}", handleGetSession(co))
		srv.RegisterRoute("POST", "/api/v1/sessions/{id}/approve", handleApproveSession(co))
		srv.RegisterRoute("POST", "/api/v1/sessions/{id}/deny", handleDenySession(co))
	}

	// GitHub App webhook
	if idCfg.GitHubApp != nil {
		ghClient := githubapp.NewClient(idCfg.GitHubApp, nil)
		ghWebhook := githubapp.NewWebhookHandler(idCfg.GitHubApp, ghClient, nil)
		srv.RegisterRoute("POST", idCfg.GitHubApp.WebhookEndpoint, ghWebhook.ServeHTTP)
		log.Printf("bootstrap: github app webhook at %s", idCfg.GitHubApp.WebhookEndpoint)
	}

	return srv, nil
}

// healthHandler responds with 200 OK for health checks.
func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(w, `{"status":"ok"}`)
}

// handleSubmitTask returns an HTTP handler that submits a new task.
// Request body: {"task_id": "...", "description": "..."}
// Response 202: {"session_id": "ses_..."}
func handleSubmitTask(co *coordinator.Coordinator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			TaskID      string `json:"task_id"`
			Description string `json:"description"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"invalid JSON body"}`, http.StatusBadRequest)
			return
		}
		if req.TaskID == "" || req.Description == "" {
			http.Error(w, `{"error":"task_id and description are required"}`, http.StatusBadRequest)
			return
		}

		if err := co.SubmitTask(context.Background(), req.TaskID, req.Description); err != nil {
			log.Printf("api: submit task %s: %v", req.TaskID, err)
			status := http.StatusInternalServerError
			if strings.Contains(err.Error(), "max concurrent tasks reached") {
				status = http.StatusTooManyRequests
			}
			http.Error(w, `{"error":"`+err.Error()+`"}`, status)
			return
		}

		sessionID := "ses_" + req.TaskID
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = fmt.Fprintf(w, `{"session_id":"%s"}`, sessionID)
	}
}

// handleGetSession returns an HTTP handler that returns the current status of a session.
func handleGetSession(co *coordinator.Coordinator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sessionID := r.PathValue("id")
		if sessionID == "" {
			http.Error(w, `{"error":"session id is required"}`, http.StatusBadRequest)
			return
		}

		info := co.GetSession(sessionID)
		if info == nil {
			http.Error(w, `{"error":"session not found"}`, http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(info)
	}
}

// handleApproveSession returns an HTTP handler that approves a session waiting at the approval gate.
// Request body (optional): {"user_id": "..."}
// Response 200: {"status":"approved"}
func handleApproveSession(co *coordinator.Coordinator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sessionID := r.PathValue("id")
		if sessionID == "" {
			http.Error(w, `{"error":"session id is required"}`, http.StatusBadRequest)
			return
		}

		userID := "api-user"
		var req struct {
			UserID string `json:"user_id"`
		}
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&req)
		}
		if req.UserID != "" {
			userID = req.UserID
		}

		if err := co.ApproveSession(r.Context(), sessionID, userID); err != nil {
			log.Printf("api: approve session %s: %v", sessionID, err)
			http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, `{"status":"approved"}`)
	}
}

// handleDenySession returns an HTTP handler that denies a session waiting at the approval gate.
// Request body (optional): {"user_id": "...", "reason": "..."}
// Response 200: {"status":"denied"}
func handleDenySession(co *coordinator.Coordinator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sessionID := r.PathValue("id")
		if sessionID == "" {
			http.Error(w, `{"error":"session id is required"}`, http.StatusBadRequest)
			return
		}

		userID := "api-user"
		reason := "denied via API"
		var req struct {
			UserID string `json:"user_id"`
			Reason string `json:"reason"`
		}
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&req)
		}
		if req.UserID != "" {
			userID = req.UserID
		}
		if req.Reason != "" {
			reason = req.Reason
		}

		if err := co.DenySession(r.Context(), sessionID, userID, reason); err != nil {
			log.Printf("api: deny session %s: %v", sessionID, err)
			http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, `{"status":"denied"}`)
	}
}

// newEventBus creates the event bus based on config.
func newEventBus(ctx context.Context, cfg *config.Config) (eventbus.EventBus, error) {
	switch cfg.Subsystems.EventBus.Kind {
	case "memory", "":
		return eventbus.NewMemoryBus(), nil
	case "nats":
		if cfg.Subsystems.EventBus.URL != "" {
			return eventbus.NewNATSBus(ctx, cfg.Subsystems.EventBus.URL)
		}
		return eventbus.NewEmbeddedNATSBus(ctx)
	default:
		return nil, fmt.Errorf("unknown eventbus kind: %s", cfg.Subsystems.EventBus.Kind)
	}
}

// newSandbox creates the sandbox provider based on config.
func newSandbox(cfg *config.Config) (sandbox.Sandbox, error) {
	switch cfg.Subsystems.Sandbox.Kind {
	case "docker":
		return sandbox.NewDockerSandboxWithNetwork(cfg.Subsystems.Sandbox.Image, cfg.Subsystems.Sandbox.Network)
	case "":
		return nil, nil
	default:
		return nil, fmt.Errorf("unknown sandbox kind: %s", cfg.Subsystems.Sandbox.Kind)
	}
}

// newMCPHub creates the MCP tool hub from the static server config.
func newMCPHub(cfg *config.Config) mcphub.MCPHub {
	serverCfgs := make([]mcphub.MCPServerConfig, 0, len(cfg.Subsystems.MCPHub.Servers))
	for _, s := range cfg.Subsystems.MCPHub.Servers {
		serverCfgs = append(serverCfgs, mcphub.MCPServerConfig{
			Name:      s.Name,
			Transport: s.Transport,
			Command:   s.Command,
			Args:      s.Args,
		})
	}
	return mcphub.NewStaticHub(serverCfgs)
}

// newAdapters builds the configured agent adapters.
func newAdapters(cfg *config.Config, bus eventbus.EventBus) []adapter.AgentAdapter {
	var result []adapter.AgentAdapter
	for _, a := range cfg.Subsystems.Agents {
		switch a.Kind {
		case "opencode":
			result = append(result, adapter.NewOpenCodeAdapter(
				a.Cmd, a.Cwd, bus, a.HeartbeatInterval, a.HeartbeatTimeout,
			))
		case "exec":
			result = append(result, adapter.NewExecAdapter(
				a.Cmd, a.Cwd, a.HeartbeatTimeout,
			))
		default:
			log.Printf("bootstrap: unknown agent kind %q for agent %q", a.Kind, a.Name)
		}
	}
	return result
}

// newIssuer creates the identity token issuer if identity config is present.
func newIssuer(cfg *config.Config) *identity.Issuer {
	idCfg := cfg.Subsystems.Identity
	if (idCfg == identity.IdentityProviderConfig{}) {
		return nil
	}
	km, err := identity.NewSigningKeyManager(idCfg.SigningKey)
	if err != nil {
		log.Printf("bootstrap: signing key manager: %v (token issuance disabled)", err)
		return nil
	}
	return identity.NewIssuer(km, "foreman")
}

// newStateStore creates the state store based on config.
func newStateStore(ctx context.Context, cfg *config.Config) (statestore.StateStore, error) {
	switch cfg.Subsystems.StateStore.Kind {
	case "postgres":
		return statestore.NewPostgresStore(ctx, cfg.Subsystems.StateStore.DSN, statestore.PoolConfig{
			MaxConns: cfg.Subsystems.StateStore.MaxConns,
			MinConns: cfg.Subsystems.StateStore.MinConns,
		})
	case "", "memory":
		return nil, nil
	default:
		return nil, fmt.Errorf("unknown statestore kind: %s", cfg.Subsystems.StateStore.Kind)
	}
}

// newPolicies compiles policy configs into usable policies.
func newPolicies(cfg *config.Config) ([]policy.Policy, error) {
	cfgs := cfg.Subsystems.Coordinator.Policies
	if len(cfgs) == 0 {
		return nil, nil
	}
	policyCfgs := make([]policy.Config, 0, len(cfgs))
	for _, c := range cfgs {
		policyCfgs = append(policyCfgs, policy.Config{
			Name: c.Name,
			Match: policy.MatchDef{
				Tool:   c.Match.Tool,
				Inputs: c.Match.Inputs,
			},
			Action:  c.Action,
			Timeout: c.Timeout,
		})
	}
	return policy.CompileConfigs(policyCfgs)
}

// newPlugins builds the configured communication plugins.
func newPlugins(cfg *config.Config) []plugins.Plugin {
	var result []plugins.Plugin
	if cfg.Subsystems.Plugins.Slack != nil {
		result = append(result, slack.New(slack.Config{
			BotToken:      cfg.Subsystems.Plugins.Slack.BotToken,
			AppToken:      cfg.Subsystems.Plugins.Slack.AppToken,
			SigningSecret: cfg.Subsystems.Plugins.Slack.SigningSecret,
		}))
	}
	if cfg.Subsystems.Plugins.Discord != nil {
		result = append(result, discord.New(discord.Config{
			BotToken: cfg.Subsystems.Plugins.Discord.BotToken,
		}))
	}
	return result
}

// parseFloat64 parses a string into a float64. Returns the fallback value on
// parse failure or if the string is empty.
func parseFloat64(s string, fallback float64) float64 {
	if s == "" {
		return fallback
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return fallback
	}
	return v
}

// Shutdown gracefully shuts down the app with a 3-phase drain sequence:
//
//  1. StopAccepting -- prevent new tasks from being submitted
//  2. Drain -- wait for active sessions to checkpoint or 30s timeout
//  3. Cleanup -- stop plugins, API server, state store, event bus
func (a *App) Shutdown(ctx context.Context) {
	log.Println("shutdown: stopping new tasks")
	if a.Coordinator != nil {
		a.Coordinator.StopAccepting()
	}

	// Phase 2: drain active sessions with a dedicated timeout.
	drainCtx, drainCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer drainCancel()

	if a.Coordinator != nil {
		remaining := a.Coordinator.Drain(drainCtx)
		if remaining > 0 {
			log.Printf("shutdown: %d session(s) still active after drain timeout", remaining)
		} else {
			log.Println("shutdown: all sessions drained")
		}
	}

	// Phase 3: clean up subsystems.
	for _, p := range a.Plugins {
		if err := p.Stop(ctx); err != nil {
			log.Printf("shutdown: plugin %s: stop: %v", p.Name(), err)
		}
	}
	if a.APIServer != nil {
		if err := a.APIServer.Shutdown(ctx); err != nil {
			log.Printf("shutdown: api server: %v", err)
		}
	}
	if a.StateStore != nil {
		a.StateStore.Close()
	}
	if err := a.EventBus.Close(); err != nil {
		log.Printf("shutdown: eventbus close: %v", err)
	}
	log.Println("shutdown: complete")
}
