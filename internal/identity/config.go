package identity

import (
	"errors"
	"os"
)

const (
	// DefaultSigningKeyEnvVar is the default environment variable name for the signing key.
	DefaultSigningKeyEnvVar = "FOREMAN_SIGNING_KEY"

	// DefaultSigningKeyID is the default key identifier for signing.
	DefaultSigningKeyID = "foreman-1"

	// DefaultWebhookEndpoint is the default path for GitHub webhooks.
	DefaultWebhookEndpoint = "/api/v1/webhooks/github"

	// DefaultListenAddr is the default HTTP listen address for the API server.
	DefaultListenAddr = ":8080"
)

// IdentityProviderConfig configures the identity subsystem.
type IdentityProviderConfig struct {
	// API configures the HTTP server for webhooks and OIDC endpoints.
	API APIConfig `yaml:"api"`

	SigningKey SigningKeyConfig `yaml:"signing_key"`

	// GitHubApp is optional. If nil, GitHub App features are disabled.
	GitHubApp *GitHubAppConfig `yaml:"github_app,omitempty"`
}

// APIConfig configures the HTTP server exposed by the identity subsystem.
type APIConfig struct {
	// ListenAddr is the TCP address to listen on (e.g. ":8080").
	// Defaults to ":8080".
	ListenAddr string `yaml:"listen_addr"`

	// PublicURL is the externally-accessible base URL used for
	// OIDC discovery (e.g. "https://foreman.example.com").
	PublicURL string `yaml:"public_url"`
}

// Validate checks the identity provider config for errors.
func (c IdentityProviderConfig) Validate() error {
	if err := c.SigningKey.Validate(); err != nil {
		return err
	}
	if c.GitHubApp != nil {
		if err := c.GitHubApp.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// SigningKeyConfig configures how the signing key is loaded.
type SigningKeyConfig struct {
	// Source is "env" or "file".
	Source string `yaml:"source"`

	// KeyID is the identifier for this signing key (used in JWT kid header).
	KeyID string `yaml:"key_id"`

	// EnvVarName is the environment variable name (only for source: env).
	EnvVarName string `yaml:"env_var_name,omitempty"`

	// FilePath is the path to the PEM file (only for source: file).
	FilePath string `yaml:"file_path,omitempty"`
}

// Validate checks the signing key config for errors.
func (c SigningKeyConfig) Validate() error {
	switch c.Source {
	case "env":
		if c.EnvVarName == "" {
			return errors.New("signing_key: env_var_name is required when source is 'env'")
		}
		if os.Getenv(c.EnvVarName) == "" {
			return errors.New("signing_key: environment variable " + c.EnvVarName + " is not set")
		}
	case "file":
		if c.FilePath == "" {
			return errors.New("signing_key: file_path is required when source is 'file'")
		}
		if _, err := os.Stat(c.FilePath); err != nil {
			return errors.New("signing_key: " + err.Error())
		}
	case "":
		return errors.New("signing_key: source is required ('env' or 'file')")
	default:
		return errors.New("signing_key: invalid source '" + c.Source + "'; must be 'env' or 'file'")
	}
	return nil
}

// GitHubAppConfig configures a GitHub App integration.
type GitHubAppConfig struct {
	// AppID is the GitHub App ID (integer).
	AppID int64 `yaml:"app_id"`

	// PrivateKeyPath is the path to the GitHub App's PEM-encoded private key.
	PrivateKeyPath string `yaml:"private_key_path"`

	// WebhookSecret is the secret used to verify GitHub webhook payloads.
	WebhookSecret string `yaml:"webhook_secret"`

	// AppSlug is the slug/name of the GitHub App (for display purposes).
	AppSlug string `yaml:"app_slug,omitempty"`

	// WebhookEndpoint is the path to receive GitHub webhooks.
	// Defaults to /api/v1/webhooks/github
	WebhookEndpoint string `yaml:"webhook_endpoint,omitempty"`
}

// Validate checks the GitHub App config for errors.
func (c *GitHubAppConfig) Validate() error {
	if c.AppID == 0 {
		return errors.New("github_app: app_id is required")
	}
	if c.PrivateKeyPath == "" {
		return errors.New("github_app: private_key_path is required")
	}
	if _, err := os.Stat(c.PrivateKeyPath); err != nil {
		return errors.New("github_app: " + err.Error())
	}
	if c.WebhookSecret == "" {
		return errors.New("github_app: webhook_secret is required")
	}
	if c.WebhookEndpoint == "" {
		c.WebhookEndpoint = DefaultWebhookEndpoint
	}
	return nil
}
