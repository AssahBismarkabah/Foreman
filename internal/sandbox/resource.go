package sandbox

import "context"

// ResourceUsage represents CPU/memory usage for a sandbox.
type ResourceUsage struct {
	CPUPercent  float64 `json:"cpu_percent"`
	MemoryBytes uint64  `json:"memory_bytes"`
	MemoryLimit uint64  `json:"memory_limit"`
}

// StatsProvider is an optional interface that sandbox implementations can
// implement to expose resource usage statistics. The coordinator checks
// whether the sandbox supports this via a type assertion.
type StatsProvider interface {
	Stats(ctx context.Context, sessionID string) (*ResourceUsage, error)
}
