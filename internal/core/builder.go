package core

import (
	"context"
	"fmt"
	"log"

	"github.com/foreman/foreman/internal/config"
	"github.com/foreman/foreman/internal/controlplane"
	"github.com/foreman/foreman/internal/coordinator"
	"github.com/foreman/foreman/internal/eventbus"
	"github.com/foreman/foreman/internal/mcphub"
	"github.com/foreman/foreman/internal/sandbox"
)

type App struct {
	Config       *config.Config
	EventBus     eventbus.EventBus
	Sandbox      sandbox.Sandbox
	MCPHub       mcphub.MCPHub
	ControlPlane *controlplane.ControlPlane
	Coordinator  *coordinator.Coordinator
}

func Bootstrap(ctx context.Context, cfg *config.Config) (*App, error) {
	bus, err := newEventBus(cfg)
	if err != nil {
		return nil, fmt.Errorf("eventbus: %w", err)
	}

	sbox, err := newSandbox(cfg)
	if err != nil {
		return nil, fmt.Errorf("sandbox: %w", err)
	}

	mcp := newMCPHub(cfg)
	cp := controlplane.New(bus)

	// TODO: build adapters from config
	co := coordinator.New(bus, cp, sbox, mcp, nil, cfg.Subsystems.Coordinator.MaxConcurrent)

	return &App{
		Config:       cfg,
		EventBus:     bus,
		Sandbox:      sbox,
		MCPHub:       mcp,
		ControlPlane: cp,
		Coordinator:  co,
	}, nil
}

func newEventBus(cfg *config.Config) (eventbus.EventBus, error) {
	switch cfg.Subsystems.EventBus.Kind {
	case "memory", "":
		return eventbus.NewMemoryBus(), nil
	case "nats":
		return nil, fmt.Errorf("nats eventbus not yet implemented")
	default:
		return nil, fmt.Errorf("unknown eventbus kind: %s", cfg.Subsystems.EventBus.Kind)
	}
}

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

func (a *App) Shutdown(ctx context.Context) {
	if err := a.EventBus.Close(); err != nil {
		log.Printf("shutdown: eventbus close: %v", err)
	}
}
