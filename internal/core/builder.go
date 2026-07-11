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
	"github.com/foreman/foreman/internal/sandbox"
)

// App holds all the wired subsystems.
type App struct {
	Config       *config.Config
	EventBus     eventbus.EventBus
	Sandbox      sandbox.Sandbox
	MCPHub       mcphub.MCPHub
	ControlPlane *controlplane.ControlPlane
	Coordinator  *coordinator.Coordinator
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

	sbox, err := newSandbox(cfg)
	if err != nil {
		return nil, fmt.Errorf("sandbox: %w", err)
	}

	mcp := newMCPHub(cfg)
	cp := controlplane.New(bus)
	adapters := newAdapters(cfg, bus)

	co := coordinator.New(bus, cp, sbox, mcp, adapters, cfg.Subsystems.Coordinator.MaxConcurrent)

	return &App{
		Config:       cfg,
		EventBus:     bus,
		Sandbox:      sbox,
		MCPHub:       mcp,
		ControlPlane: cp,
		Coordinator:  co,
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

// Shutdown gracefully shuts down the app.
func (a *App) Shutdown(ctx context.Context) {
	if err := a.EventBus.Close(); err != nil {
		log.Printf("shutdown: eventbus close: %v", err)
	}
}
