package core

import (
	"context"
	"fmt"
	"log"

	"github.com/foreman/foreman/internal/adapter"
	"github.com/foreman/foreman/internal/config"
	"github.com/foreman/foreman/internal/controlplane"
	"github.com/foreman/foreman/internal/coordinator"
	"github.com/foreman/foreman/internal/eventbus"
	"github.com/foreman/foreman/internal/mcphub"
	"github.com/foreman/foreman/internal/plugins"
	"github.com/foreman/foreman/internal/plugins/discord"
	"github.com/foreman/foreman/internal/plugins/slack"
	"github.com/foreman/foreman/internal/policy"
	"github.com/foreman/foreman/internal/sandbox"
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
	if err := cp.Recover(ctx); err != nil {
		return nil, fmt.Errorf("recover sessions: %w", err)
	}

	adapters := newAdapters(cfg, bus)

	policies, err := newPolicies(cfg)
	if err != nil {
		return nil, fmt.Errorf("policies: %w", err)
	}

	co := coordinator.New(bus, cp, sbox, mcp, adapters, policies, cfg.Subsystems.Coordinator.MaxConcurrent)

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
	}, nil
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

// Shutdown gracefully shuts down the app.
func (a *App) Shutdown(ctx context.Context) {
	for _, p := range a.Plugins {
		if err := p.Stop(ctx); err != nil {
			log.Printf("shutdown: plugin %s: stop: %v", p.Name(), err)
		}
	}
	if a.StateStore != nil {
		a.StateStore.Close()
	}
	if err := a.EventBus.Close(); err != nil {
		log.Printf("shutdown: eventbus close: %v", err)
	}
}
