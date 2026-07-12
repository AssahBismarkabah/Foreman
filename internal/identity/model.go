package identity

import "time"

// IdentityType represents the kind of identity in the system.
type IdentityType string

const (
	IdentityUser           IdentityType = "user"
	IdentityAgent          IdentityType = "agent"
	IdentityServiceAccount IdentityType = "service_account"
)

// Subject represents any identified entity in the system.
type Subject struct {
	Type        IdentityType      `json:"type"`
	ID          string            `json:"id"`
	DisplayName string            `json:"display_name,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// User represents a human user authenticated via a chat plugin.
type User struct {
	ID          string    `json:"id"`
	DisplayName string    `json:"display_name"`
	SlackTeamID string    `json:"slack_team_id,omitempty"`
	PluginID    string    `json:"plugin_id,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// Agent represents a coding agent running in a sandbox.
type Agent struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	SandboxID      string    `json:"sandbox_id"`
	AssignedUserID string    `json:"assigned_user_id,omitempty"`
	SessionID      string    `json:"session_id"`
	CreatedAt      time.Time `json:"created_at"`
}

// ServiceAccount represents a non-human service identity.
type ServiceAccount struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// AgentScope defines the permissions granted to an agent token.
// See architecture.md section 5.2 for the full specification.
type AgentScope struct {
	// Repos the agent is allowed to access (e.g. ["org/repo"]).
	Repos []string `json:"repos,omitempty"`
	// Actions the agent may perform: "read", "pull", "push", etc.
	Actions []string `json:"actions,omitempty"`
	// Branches the agent is restricted to (glob patterns, e.g. ["feature/*"]).
	Branches []string `json:"branches,omitempty"`
	// MaxPRs is the maximum number of pull requests the agent may create (0 = unlimited).
	MaxPRs int `json:"max_prs,omitempty"`
	// NoDelete prohibits destructive operations (branch/tag deletion).
	NoDelete bool `json:"no_delete"`
}

// InstallationState represents the lifecycle state of a platform installation.
type InstallationState string

const (
	InstallationActive    InstallationState = "active"
	InstallationSuspended InstallationState = "suspended"
	InstallationDeleted   InstallationState = "deleted"
)

// Installation represents a GitHub (or GitLab) App installation on an account.
type Installation struct {
	ID                string            `json:"id"`                  // Foreman internal UUID
	Platform          string            `json:"platform"`            // "github" or "gitlab"
	PlatformInstallID int64             `json:"platform_install_id"` // Platform's installation ID
	AccountLogin      string            `json:"account_login"`
	AccountType       string            `json:"account_type"` // "User" or "Organization"
	AccountID         int64             `json:"account_id"`
	CreatedAt         time.Time         `json:"created_at"`
	UpdatedAt         time.Time         `json:"updated_at"`
	State             InstallationState `json:"state"`
	Metadata          map[string]string `json:"metadata,omitempty"`
}
