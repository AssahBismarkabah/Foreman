package core

import (
	"context"
	"fmt"
	"log"
	"net/http"
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
	)

	// Reap orphaned Docker containers from previous runs.
	activeSessions := make(map[string]bool, len(recovered))
	for _, s := range recovered {
		activeSessions[s.ID] = true
	}
	co.ReapOrphanedContainers(ctx, activeSessions)

	srv, err := newAPIServer(ctx, cfg, bus, iss)
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

// newAPIServer creates the HTTP API server with identity/health/webhook routes.
func newAPIServer(ctx context.Context, cfg *config.Config, _ eventbus.EventBus, iss *identity.Issuer) (*api.Server, error) {
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
		return sandbox.NewDockerSandbox(cfg.Subsystems.Sandbox.Image), nil
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
