package identity

import (
	"os"
	"testing"
)

func TestSigningKeyConfig_Validate_EnvMissingVarName(t *testing.T) {
	cfg := SigningKeyConfig{Source: "env", EnvVarName: ""}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for missing env_var_name")
	}
}

func TestSigningKeyConfig_Validate_EnvMissingEnvVar(t *testing.T) {
	cfg := SigningKeyConfig{Source: "env", EnvVarName: "FOREMAN_TEST_NONEXISTENT_KEY"}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for unset environment variable")
	}
}

func TestSigningKeyConfig_Validate_EnvValid(t *testing.T) {
	os.Setenv("FOREMAN_TEST_KEY", "test-key-data")
	defer os.Unsetenv("FOREMAN_TEST_KEY")

	cfg := SigningKeyConfig{Source: "env", EnvVarName: "FOREMAN_TEST_KEY"}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestSigningKeyConfig_Validate_FileMissingPath(t *testing.T) {
	cfg := SigningKeyConfig{Source: "file", FilePath: ""}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for missing file_path")
	}
}

func TestSigningKeyConfig_Validate_FileNotFound(t *testing.T) {
	cfg := SigningKeyConfig{Source: "file", FilePath: "/tmp/nonexistent-key.pem"}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

func TestSigningKeyConfig_Validate_InvalidSource(t *testing.T) {
	cfg := SigningKeyConfig{Source: "invalid"}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for invalid source")
	}
}

func TestSigningKeyConfig_Validate_EmptySource(t *testing.T) {
	cfg := SigningKeyConfig{Source: ""}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for empty source")
	}
}

func TestGitHubAppConfig_Validate_MissingAppID(t *testing.T) {
	cfg := &GitHubAppConfig{AppID: 0, PrivateKeyPath: "/tmp/key.pem", WebhookSecret: "secret"}
	// Create temp file for path validation
	tmp, _ := os.CreateTemp("", "key.pem")
	defer os.Remove(tmp.Name())
	cfg.PrivateKeyPath = tmp.Name()

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for missing app_id")
	}
}

func TestGitHubAppConfig_Validate_MissingPrivateKeyPath(t *testing.T) {
	cfg := &GitHubAppConfig{AppID: 123, PrivateKeyPath: ""}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for missing private_key_path")
	}
}

func TestGitHubAppConfig_Validate_MissingWebhookSecret(t *testing.T) {
	tmp, _ := os.CreateTemp("", "key.pem")
	defer os.Remove(tmp.Name())

	cfg := &GitHubAppConfig{AppID: 123, PrivateKeyPath: tmp.Name(), WebhookSecret: ""}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for missing webhook_secret")
	}
}

func TestGitHubAppConfig_Validate_DefaultsWebhookEndpoint(t *testing.T) {
	tmp, _ := os.CreateTemp("", "key.pem")
	defer os.Remove(tmp.Name())

	cfg := &GitHubAppConfig{AppID: 123, PrivateKeyPath: tmp.Name(), WebhookSecret: "secret"}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if cfg.WebhookEndpoint != DefaultWebhookEndpoint {
		t.Fatalf("expected default endpoint %q, got %q", DefaultWebhookEndpoint, cfg.WebhookEndpoint)
	}
}

func TestGitHubAppConfig_Validate_Valid(t *testing.T) {
	tmp, _ := os.CreateTemp("", "key.pem")
	defer os.Remove(tmp.Name())

	cfg := &GitHubAppConfig{
		AppID:           123,
		PrivateKeyPath:  tmp.Name(),
		WebhookSecret:   "secret",
		WebhookEndpoint: "/custom/webhook",
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if cfg.WebhookEndpoint != "/custom/webhook" {
		t.Fatalf("expected endpoint %q, got %q", "/custom/webhook", cfg.WebhookEndpoint)
	}
}

func TestIdentityProviderConfig_Validate_SigningKeyError(t *testing.T) {
	cfg := IdentityProviderConfig{SigningKey: SigningKeyConfig{Source: "invalid"}}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for invalid signing key config")
	}
}

func TestIdentityProviderConfig_Validate_GitHubAppDisabled(t *testing.T) {
	// GitHubApp nil = disabled, should pass signing key check with valid env
	os.Setenv("FOREMAN_TEST_VALID", "key-data")
	defer os.Unsetenv("FOREMAN_TEST_VALID")

	cfg := IdentityProviderConfig{
		SigningKey: SigningKeyConfig{Source: "env", EnvVarName: "FOREMAN_TEST_VALID"},
		GitHubApp:  nil,
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}
