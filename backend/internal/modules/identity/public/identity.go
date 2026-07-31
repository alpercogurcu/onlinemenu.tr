// Package public exposes the identity module's contract to other modules.
// Imports of internal identity packages (domain, repo, http) from outside
// the identity module are forbidden by go-arch-lint.
package public

import (
	"context"
	"fmt"

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

// ErrConflict is returned when input is well-formed but conflicts with the
// current state of the resource, so the caller must act before retrying.
// Callers should use errors.Is to check for this condition.
var ErrConflict = identityConflictError{}

type identityConflictError struct{}

func (identityConflictError) Error() string { return "identity: conflict" }

// BranchScopeConflictError reports a refused branch_scoped FALSE -> TRUE flip.
//
// The trigger from identity migration 000012 fires on membership writes, not on
// role writes, so flipping the flag does not re-validate memberships already
// granted chain-wide. Those rows would stay live as chain-wide grants of a role
// that is, from that moment on, branch-scoped — precisely the leak ADR-SEC-005
// exists to close. The flip is therefore refused and the offending count is
// carried to the caller so the conflict is actionable rather than a dead end.
type BranchScopeConflictError struct {
	RoleID               uuid.UUID
	ChainWideMemberships int
}

func (e BranchScopeConflictError) Error() string {
	return fmt.Sprintf(
		"identity: role %s cannot become branch-scoped: %d chain-wide membership(s) would be left in violation",
		e.RoleID, e.ChainWideMemberships,
	)
}

// Unwrap lets errors.Is(err, ErrConflict) succeed so the HTTP layer can map this
// to 409 without importing the concrete type.
func (e BranchScopeConflictError) Unwrap() error { return ErrConflict }
