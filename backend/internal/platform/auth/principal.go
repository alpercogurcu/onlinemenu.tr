// Package auth provides JWT validation middleware, principal extraction,
// and OPA-based authorization for the chi HTTP router.
package auth

import (
	"context"

	"github.com/google/uuid"
)

// Context identifies which mode the authenticated principal is operating in.
type Context string

const (
	// ContextStaff is a branch-scoped work session. TenantID, BranchID and
	// RoleIDs are populated. Selected via POST /identity/auth/context.
	ContextStaff Context = "staff"

	// ContextCustomer is a platform-wide read session for the person's own
	// purchase history across all tenants. TenantID and BranchID are zero.
	ContextCustomer Context = "customer"
)

// Principal carries the authenticated identity for a single request.
// It is stored in the request context via middleware and consumed by
// service and OPA layers. The domain model does not reference Principal directly.
//
// Two principal shapes exist:
//   - Pre-context (Keycloak token): only KeycloakSub is set. Valid only for
//     GET /identity/me/contexts and POST /identity/auth/context.
//   - Context principal (CTX token): PersonID, Ctx, and Ctx-dependent fields
//     are set. Required for all resource endpoints.
type Principal struct {
	// KeycloakSub is the Keycloak subject claim. Populated from Keycloak tokens.
	// Used by the identity service to resolve PersonID before context selection.
	KeycloakSub string

	// PersonID is the platform-level person identifier (persons.id).
	// Zero until context selection completes.
	PersonID uuid.UUID

	// Ctx indicates the operating mode (staff | customer).
	Ctx Context

	// TenantID is the selected chain. Populated only in ContextStaff.
	TenantID uuid.UUID

	// BranchID is the selected branch. Populated only in ContextStaff.
	BranchID uuid.UUID

	// RoleIDs lists all active roles the person holds at TenantID+BranchID.
	// Permissions are the union of all roles. Populated only in ContextStaff.
	RoleIDs []uuid.UUID
}

// IsPreContext reports whether this is a Keycloak-only principal (no context selected yet).
func (p Principal) IsPreContext() bool { return p.PersonID == uuid.Nil && p.KeycloakSub != "" }

// IsStaff reports whether the principal is in a staff context.
func (p Principal) IsStaff() bool { return p.Ctx == ContextStaff }

// IsCustomer reports whether the principal is in the customer context.
func (p Principal) IsCustomer() bool { return p.Ctx == ContextCustomer }

// HasBranchAccess reports whether the principal's context covers the given branch.
// Chain-wide staff (BranchID == uuid.Nil) can access every branch in their tenant.
// Customer principals always return false (they have no branch context).
func (p Principal) HasBranchAccess(branchID uuid.UUID) bool {
	if p.Ctx != ContextStaff {
		return false
	}
	return p.BranchID == uuid.Nil || p.BranchID == branchID
}

type contextKey struct{}

// principalKey is the context key for storing Principal values.
var principalKey = contextKey{}

// WithPrincipal stores the given Principal in the context.
// Used by the auth middleware and by test helpers that bypass middleware.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey, p)
}
