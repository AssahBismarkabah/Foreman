package coordinator

import (
	"context"
	"fmt"
	"log"
	"sync"

	"github.com/foreman/foreman/internal/controlplane"
	"github.com/foreman/foreman/internal/eventbus"
	"github.com/foreman/foreman/internal/mcphub"
	"github.com/foreman/foreman/internal/sandbox"
	"github.com/foreman/foreman/internal/schemas"
)

type AgentAdapter interface {
	Name() string
}

type Coordinator struct {
	bus           eventbus.EventBus
	cp            *controlplane.ControlPlane
	sbox          sandbox.Sandbox
	mcpHub        mcphub.MCPHub
	adapters      map[string]AgentAdapter
	maxConcurrent int
	active        map[string]context.CancelFunc
	mu            sync.Mutex
}

func New(
	bus eventbus.EventBus,
	cp *controlplane.ControlPlane,
	sbox sandbox.Sandbox,
	mcpHub mcphub.MCPHub,
	adapters []AgentAdapter,
	maxConcurrent int,
) *Coordinator {
	adapterMap := make(map[string]AgentAdapter)
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

func (c *Coordinator) SubmitTask(ctx context.Context, taskID, description string) error {
	sessionID := fmt.Sprintf("ses_%s", taskID)
	if err := c.cp.CreateSession(ctx, sessionID, taskID); err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return c.startTask(ctx, sessionID, description)
}

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
		if tErr := c.cp.Transition(ctx, sessionID, schemas.StatusFailed); tErr != nil {
			log.Printf("coordinator: failed to mark session %s as failed: %v", sessionID, tErr)
		}
		return fmt.Errorf("resolve tools: %w", err)
	}

	if err := c.cp.Transition(ctx, sessionID, schemas.StatusRunning); err != nil {
		return fmt.Errorf("transition to running: %w", err)
	}

	runCtx, cancel := context.WithCancel(ctx)
	c.mu.Lock()
	c.active[sessionID] = cancel
	c.mu.Unlock()

	go func() {
		defer func() {
			c.mu.Lock()
			delete(c.active, sessionID)
			c.mu.Unlock()
		}()
		<-runCtx.Done()
		c.cp.Transition(ctx, sessionID, schemas.StatusCompleted)
	}()

	return nil
}

func (c *Coordinator) Start(ctx context.Context) error {
	cancel, err := c.bus.Subscribe(ctx, schemas.Subject("task", "submitted"),
		func(ctx context.Context, evt schemas.Event) error {
			pay, ok := evt.Payload.(schemas.TaskPayload)
			if !ok {
				return fmt.Errorf("unexpected payload type")
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
