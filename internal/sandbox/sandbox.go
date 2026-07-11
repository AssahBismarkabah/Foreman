package sandbox

import (
	"context"
	"time"

	"github.com/foreman/foreman/internal/schemas"
)

// SandboxStatus is now defined in schemas package.
// Deprecated: use schemas.SandboxStatus / schemas.Sandbox*
type SandboxStatus = schemas.SandboxStatus

const (
	StatusProvisioning = schemas.SandboxProvisioning
	StatusRunning      = schemas.SandboxRunning
	StatusStopped      = schemas.SandboxStopped
	StatusFailed       = schemas.SandboxFailed
)

type ExecutionResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

type SandboxSpec struct {
	Image   string
	Memory  string
	CPU     string
	Env     map[string]string
	WorkDir string
}

type SandboxEvent struct {
	Type    string
	Payload any
}

type Sandbox interface {
	Provision(ctx context.Context, spec SandboxSpec) (string, error)
	Execute(ctx context.Context, sessionID string, cmd []string, timeout time.Duration) (*ExecutionResult, error)
	WriteFile(ctx context.Context, sessionID string, path string, content []byte) error
	ReadFile(ctx context.Context, sessionID string, path string) ([]byte, error)
	UploadCheckpoint(ctx context.Context, sessionID string, sourceDir string) (string, error)
	SubscribeEvents(ctx context.Context, sessionID string) (<-chan SandboxEvent, error)
	Heartbeat(ctx context.Context, sessionID string) error
	Destroy(ctx context.Context, sessionID string) error
}
