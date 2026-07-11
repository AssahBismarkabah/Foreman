package plugin

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os/exec"
	"time"

	"github.com/foreman/foreman/internal/eventbus"
	"github.com/foreman/foreman/internal/schemas"
)

type Plugin interface {
	Name() string
	Version() string
	Start(ctx context.Context, bus eventbus.EventBus) error
	Stop(ctx context.Context) error
	SendMessage(ctx context.Context, msg []byte) error
	SendBlockMessage(ctx context.Context, blockType string, content []byte) error
}

type CLI struct {
	name    string
	version string
	cmd     string
	args    []string
	cwd     string
	bus     eventbus.EventBus
	process *exec.Cmd
	stdin   io.WriteCloser
}

func NewCLI(name, version, cmd, cwd string, args []string) *CLI {
	return &CLI{
		name:    name,
		version: version,
		cmd:     cmd,
		args:    args,
		cwd:     cwd,
	}
}

func (p *CLI) Name() string    { return p.name }
func (p *CLI) Version() string { return p.version }

func (p *CLI) Start(ctx context.Context, bus eventbus.EventBus) error {
	p.bus = bus
	p.process = exec.CommandContext(ctx, p.cmd, p.args...)
	p.process.Dir = p.cwd

	stdout, err := p.process.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	stdin, err := p.process.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}
	p.stdin = stdin

	if err := p.process.Start(); err != nil {
		return fmt.Errorf("start process: %w", err)
	}

	go p.readOutput(ctx, stdout)

	return nil
}

func (p *CLI) Stop(ctx context.Context) error {
	if p.process != nil && p.process.Process != nil {
		return p.process.Process.Kill()
	}
	return nil
}

func (p *CLI) SendMessage(ctx context.Context, msg []byte) error {
	if p.stdin == nil {
		return fmt.Errorf("plugin not started")
	}
	_, err := p.stdin.Write(append(msg, '\n'))
	return err
}

func (p *CLI) SendBlockMessage(ctx context.Context, blockType string, content []byte) error {
	block := map[string]any{
		"type":    blockType,
		"content": string(content),
	}
	raw, _ := json.Marshal(block)
	return p.SendMessage(ctx, raw)
}

func (p *CLI) readOutput(ctx context.Context, stdout io.Reader) {
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := scanner.Bytes()
		evt := schemas.Event{
			Type:      schemas.EvAgentOutput,
			Timestamp: time.Now(),
			Payload:   string(line),
		}
		if p.bus != nil {
			if err := p.bus.Publish(ctx, schemas.Subject("agent", p.name, "output"), evt); err != nil {
				log.Printf("plugin %s: publish output event: %v", p.name, err)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		log.Printf("plugin %s: read error: %v", p.name, err)
	}
	evt := schemas.Event{
		Type:      schemas.EvAgentDone,
		Timestamp: time.Now(),
	}
	if p.bus != nil {
		if err := p.bus.Publish(ctx, schemas.Subject("agent", p.name, "done"), evt); err != nil {
			log.Printf("plugin %s: publish done event: %v", p.name, err)
		}
	}
}
