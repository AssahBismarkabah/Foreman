package identity

import "context"

// Provider is the full identity abstraction for the rest of Foreman.
// Implementations are backed by the State Store (PostgreSQL).
type Provider interface {
	// GetUser returns a user by their Foreman user ID.
	GetUser(ctx context.Context, id string) (*User, error)

	// GetAgent returns an agent by its Foreman agent ID.
	GetAgent(ctx context.Context, id string) (*Agent, error)

	// GetServiceAccount returns a service account by its ID.
	GetServiceAccount(ctx context.Context, id string) (*ServiceAccount, error)

	// StoreInstallation saves a platform installation record.
	StoreInstallation(ctx context.Context, inst *Installation) error

	// GetInstallation returns an installation by its Foreman internal ID.
	GetInstallation(ctx context.Context, id string) (*Installation, error)

	// GetInstallationByPlatformID returns an installation by platform + platform install ID.
	GetInstallationByPlatformID(ctx context.Context, platform string, platformInstallID int64) (*Installation, error)

	// ListInstallations returns all installations for a given platform.
	ListInstallations(ctx context.Context, platform string) ([]Installation, error)

	// UpdateInstallationState updates the state of an installation.
	UpdateInstallationState(ctx context.Context, id string, state InstallationState) error

	// DeleteInstallation permanently removes an installation record.
	DeleteInstallation(ctx context.Context, id string) error

	// ListInstallationsByAccount returns installations for a given platform account.
	ListInstallationsByAccount(ctx context.Context, platform string, accountID int64) ([]Installation, error)
}
