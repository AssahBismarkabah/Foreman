package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Subsystems Subsystems `yaml:"subsystems"`
}

type Subsystems struct {
	EventBus    EventBusConfig    `yaml:"eventbus"`
	Sandbox     SandboxConfig     `yaml:"sandbox"`
	Coordinator CoordinatorConfig `yaml:"coordinator"`
	Agents      []AgentConfig     `yaml:"agents"`
	MCPHub      MCPHubConfig      `yaml:"mcphub"`
}

type EventBusConfig struct {
	Kind string `yaml:"kind"` // "memory" | "nats"
	URL  string `yaml:"url"`
}

type SandboxConfig struct {
	Kind  string `yaml:"kind"` // "docker" | "gvisor" | "kata"
	Image string `yaml:"image"`
}

type CoordinatorConfig struct {
	MaxConcurrent  int           `yaml:"max_concurrent"`
	DefaultTimeout time.Duration `yaml:"default_timeout"`
}

type AgentConfig struct {
	Name              string        `yaml:"name"`
	Kind              string        `yaml:"kind"`
	Cmd               string        `yaml:"cmd"`
	Cwd               string        `yaml:"cwd"`
	HeartbeatInterval time.Duration `yaml:"heartbeat_interval"`
	HeartbeatTimeout  time.Duration `yaml:"heartbeat_timeout"`
}

type MCPHubConfig struct {
	Servers []MCPServerConfig `yaml:"servers"`
}

type MCPServerConfig struct {
	Name      string   `yaml:"name"`
	Transport string   `yaml:"transport"` // "stdio" | "sse"
	Command   string   `yaml:"command"`
	Args      []string `yaml:"args"`
}

func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	return &cfg, nil
}
