// Package public exposes the identity module's contract to other modules.
// Imports of internal identity packages (domain, repo, http) from outside
// the identity module are forbidden by go-arch-lint.
package public

import (
	"context"

	"github.com/google/uuid"
)

// User is the read-only projection that other modules may reference.
// It contains no persistence details and carries no ORM tags.
type User struct {
	ID       uuid.UUID
	TenantID uuid.UUID
	Email    string
	FullName string
	IsActive bool
}

// UserReader allows other modules to look up user data without importing
// identity internals. Implemented by identity.Service.
type UserReader interface {
	// GetByID returns the user for the given tenant and user ID.
	// Returns an error wrapping ErrNotFound if the user does not exist.
	GetByID(ctx context.Context, tenantID, userID uuid.UUID) (User, error)

	// GetByKeycloakSub resolves a Keycloak subject to a platform user.
	GetByKeycloakSub(ctx context.Context, tenantID uuid.UUID, sub string) (User, error)
}

// ErrNotFound is returned when a requested resource does not exist.
// Callers should use errors.Is to check for this condition.
var ErrNotFound = userNotFoundError{}

type userNotFoundError struct{}

func (userNotFoundError) Error() string { return "identity: user not found" }
