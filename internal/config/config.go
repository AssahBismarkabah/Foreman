package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/foreman/foreman/internal/identity"
)

type Config struct {
	Subsystems Subsystems `yaml:"subsystems"`
}

type Subsystems struct {
	EventBus    EventBusConfig                  `yaml:"eventbus"`
	StateStore  StateStoreConfig                `yaml:"statestore"`
	Sandbox     SandboxConfig                   `yaml:"sandbox"`
	Coordinator CoordinatorConfig               `yaml:"coordinator"`
	Agents      []AgentConfig                   `yaml:"agents"`
	MCPHub      MCPHubConfig                    `yaml:"mcphub"`
	Plugins     PluginsConfig                   `yaml:"plugins"`
	Identity    identity.IdentityProviderConfig `yaml:"identity"`
}

type EventBusConfig struct {
	Kind string `yaml:"kind"` // "memory" | "nats"
	URL  string `yaml:"url"`
}

type SandboxConfig struct {
	Kind  string `yaml:"kind"` // "docker" | "gvisor" | "kata"
	Image string `yaml:"image"`
}

type StateStoreConfig struct {
	Kind     string `yaml:"kind"` // "postgres"
	DSN      string `yaml:"dsn"`  // postgres://user:pass@host:port/dbname?sslmode=disable
	MaxConns int32  `yaml:"max_connections"`
	MinConns int32  `yaml:"min_connections"`
}

type CoordinatorConfig struct {
	MaxConcurrent     int            `yaml:"max_concurrent"`
	DefaultTimeout    time.Duration  `yaml:"default_timeout"`
	HeartbeatInterval time.Duration  `yaml:"heartbeat_interval"` // how often to check sandbox liveness
	HeartbeatTimeout  time.Duration  `yaml:"heartbeat_timeout"`  // max time without heartbeat before crash declared
	MaxRetries        int            `yaml:"max_retries"`        // max crash recovery retries (default 3)
	BackoffBase       time.Duration  `yaml:"backoff_base"`       // base delay for exponential backoff (default 5s)
	BackoffMultiplier float64        `yaml:"backoff_multiplier"` // multiplier per attempt (default 2.0)
	Jitter            time.Duration  `yaml:"jitter"`             // random jitter added to backoff (default 1s)
	Policies          []PolicyConfig `yaml:"policies"`
}

type PolicyConfig struct {
	Name    string        `yaml:"name"`
	Match   MatchConfig   `yaml:"match"`
	Action  string        `yaml:"action"`
	Timeout time.Duration `yaml:"timeout"`
}

type MatchConfig struct {
	Tool   string            `yaml:"tool"`
	Inputs map[string]string `yaml:"inputs,omitempty"`
}

type AgentConfig struct {
	Name              string        `yaml:"name"`
	Kind              string        `yaml:"kind"`
	Cmd               string        `yaml:"cmd"`
	Cwd               string        `yaml:"cwd"`
	HeartbeatInterval time.Duration `yaml:"heartbeat_interval"`
	HeartbeatTimeout  time.Duration `yaml:"heartbeat_timeout"`
}

type PluginsConfig struct {
	Slack   *SlackPluginConfig   `yaml:"slack,omitempty"`
	Discord *DiscordPluginConfig `yaml:"discord,omitempty"`
}

type SlackPluginConfig struct {
	BotToken      string `yaml:"bot_token"`
	AppToken      string `yaml:"app_token"`
	SigningSecret string `yaml:"signing_secret"`
}

type DiscordPluginConfig struct {
	BotToken string `yaml:"bot_token"` // Bot token from Discord Developer Portal
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
