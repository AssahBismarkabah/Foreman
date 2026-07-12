package githubapp

import (
	"context"

	"github.com/foreman/foreman/internal/identity"
)

// InstallationStore handles persistence for GitHub App installations.
// The actual implementation is provided by the identity.Provider, but this
// narrower interface keeps githubapp decoupled from the full provider surface.
type InstallationStore interface {
	// Create persists a new installation record.
	Create(ctx context.Context, inst *identity.Installation) error

	// GetByPlatformID retrieves an installation by its GitHub installation ID.
	GetByPlatformID(ctx context.Context, platformInstallID int64) (*identity.Installation, error)

	// UpdateState changes the state of an installation (active/suspended/deleted).
	UpdateState(ctx context.Context, platformInstallID int64, state identity.InstallationState) error

	// Delete hard-deletes an installation record.
	Delete(ctx context.Context, platformInstallID int64) error

	// ListByAccount retrieves all installations for a given GitHub account.
	ListByAccount(ctx context.Context, accountID int64) ([]identity.Installation, error)
}
