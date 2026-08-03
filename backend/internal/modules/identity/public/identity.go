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

// CashierPinService lets other modules drive PIN-based cashier switching
// (ADR-DATA-008 PIN akışı) without importing identity internals. PIN storage
// and verification are identity's responsibility (the pin scope is
// (person, tenant), an identity-owned concept per the ADR); this is the
// narrow surface the payment module's cash-session join/switch/reset flows
// need. It never returns a PIN or its hash, in any form, to any caller.
type CashierPinService interface {
	// SetOwnPin sets/replaces personID's PIN for tenantID. Callers MUST
	// have already established that this is the ADR's "trusted moment" —
	// personID authenticated via the full Keycloak flow just now, not via a
	// PIN-derived session token — before calling this; the interface itself
	// performs no such check and trusts the caller completely.
	SetOwnPin(ctx context.Context, tenantID, personID uuid.UUID, pin string) error

	// VerifyPin reports whether pin matches personID's stored PIN for
	// tenantID. It returns ErrPinVerificationFailed for EVERY negative
	// outcome — wrong PIN, no PIN ever set, and (as far as the caller can
	// tell) a nonexistent personID all look identical, in error value AND
	// in the CPU time spent, by construction (see
	// identity/service/pin.go's DummyPinCost use). Callers must not layer
	// their own distinguishing error handling on top of this — e.g. do not
	// short-circuit on "person not found" before calling VerifyPin, or the
	// timing distinction VerifyPin itself avoids leaks back in at the
	// call site.
	VerifyPin(ctx context.Context, tenantID, personID uuid.UUID, pin string) error

	// ResetPin deletes personID's PIN row for tenantID (manager action —
	// ADR-DATA-008 PIN akışı §2: a manager may only clear, never read or
	// set). The person sets a fresh PIN at their next full-Keycloak join.
	// Resetting an already-unset PIN is not an error.
	ResetPin(ctx context.Context, tenantID, personID uuid.UUID) error
}

// ErrPinVerificationFailed is the single sentinel VerifyPin returns for
// every negative outcome (wrong PIN, unset PIN, unknown person). Callers map
// it to HTTP 401 with a generic message — never a message or status that
// would let a caller distinguish "wrong PIN" from "no such cashier".
var ErrPinVerificationFailed = identityPinVerificationFailedError{}

type identityPinVerificationFailedError struct{}

func (identityPinVerificationFailedError) Error() string { return "identity: pin verification failed" }

// ErrPinFormatInvalid is returned by SetOwnPin when pin fails the 4-6 digit
// format rule. Unlike ErrPinVerificationFailed this IS a caller-input
// problem (maps to 422), not an auth outcome — the two must stay distinct so
// a malformed PIN and a wrong PIN don't collapse into the same status code.
var ErrPinFormatInvalid = identityPinFormatInvalidError{}

type identityPinFormatInvalidError struct{}

func (identityPinFormatInvalidError) Error() string { return "identity: pin must be 4-6 digits" }

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

// ErrInvalidInput marks a caller-supplied value the staff invite service
// rejects (a blank name/email, a malformed email, a branch-scoped role
// invited without a branch_id). Callers map it to HTTP 422.
//
// This mirrors payment.ErrInvalidInput (commit d451cb2): without a sentinel,
// these fall through to the generic error branch, are reported as 500
// "internal server error", and are logged at Error level even though the
// caller — not the server — is at fault. It is distinct from ErrInvalid
// (mapped to 400 elsewhere in this module) so introducing it does not change
// the status code of any existing identity endpoint.
var ErrInvalidInput = identityInvalidInputError{}

type identityInvalidInputError struct{}

func (identityInvalidInputError) Error() string { return "identity: invalid input" }

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
