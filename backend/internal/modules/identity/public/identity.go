// Package public exposes the identity module's contract to other modules.
// Imports of internal identity packages (domain, repo, http) from outside
// the identity module are forbidden by go-arch-lint.
package public

import (
	"context"

	"github.com/google/uuid"
)

// Person is the read-only projection other modules may reference.
// It contains no persistence details and carries no ORM tags.
type Person struct {
	ID       uuid.UUID
	Email    string
	FullName string
}

// PersonReader allows other modules to look up person data without importing
// identity internals.
type PersonReader interface {
	GetByID(ctx context.Context, personID uuid.UUID) (Person, error)
	GetByKeycloakSub(ctx context.Context, sub string) (Person, error)
}

// MembershipResolver allows other modules to query the active role IDs a person
// holds at a given branch within a tenant.
type MembershipResolver interface {
	ActiveRoleIDsAt(ctx context.Context, tenantID, personID, branchID uuid.UUID) ([]uuid.UUID, error)
}

// ErrNotFound is returned when a requested resource does not exist.
// Callers should use errors.Is to check for this condition.
var ErrNotFound = identityNotFoundError{}

type identityNotFoundError struct{}

func (identityNotFoundError) Error() string { return "identity: not found" }

// ErrInvalid is returned when input fails domain validation.
// Callers should use errors.Is to check for this condition.
var ErrInvalid = identityInvalidError{}

type identityInvalidError struct{}

func (identityInvalidError) Error() string { return "identity: invalid input" }
