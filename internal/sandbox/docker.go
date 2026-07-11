package sandbox

import (
	"context"
	"fmt"
	"time"
)

type DockerSandbox struct {
	image string
}

func NewDockerSandbox(image string) *DockerSandbox {
	return &DockerSandbox{image: image}
}

func (d *DockerSandbox) Provision(ctx context.Context, spec SandboxSpec) (string, error) {
	return "", fmt.Errorf("docker sandbox: provision not yet implemented")
}

func (d *DockerSandbox) Execute(ctx context.Context, sessionID string, cmd []string, timeout time.Duration) (*ExecutionResult, error) {
	return nil, fmt.Errorf("docker sandbox: execute not yet implemented")
}

func (d *DockerSandbox) WriteFile(ctx context.Context, sessionID, path string, content []byte) error {
	return fmt.Errorf("docker sandbox: write file not yet implemented")
}

func (d *DockerSandbox) ReadFile(ctx context.Context, sessionID, path string) ([]byte, error) {
	return nil, fmt.Errorf("docker sandbox: read file not yet implemented")
}

func (d *DockerSandbox) UploadCheckpoint(ctx context.Context, sessionID, sourceDir string) (string, error) {
	return "", fmt.Errorf("docker sandbox: upload checkpoint not yet implemented")
}

func (d *DockerSandbox) SubscribeEvents(ctx context.Context, sessionID string) (<-chan SandboxEvent, error) {
	return nil, fmt.Errorf("docker sandbox: subscribe events not yet implemented")
}

func (d *DockerSandbox) Heartbeat(ctx context.Context, sessionID string) error {
	return fmt.Errorf("docker sandbox: heartbeat not yet implemented")
}

func (d *DockerSandbox) Destroy(ctx context.Context, sessionID string) error {
	return fmt.Errorf("docker sandbox: destroy not yet implemented")
}
