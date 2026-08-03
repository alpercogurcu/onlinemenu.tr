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

	// SessionID is the cash session this token was minted against
	// (ADR-DATA-008 PIN akışı). uuid.Nil for every token issued via the
	// normal Keycloak /auth/context flow (IssueStaff) — only a PIN-switched
	// token (IssueStaffForSession) sets it. Its presence is what
	// RequireOpenSession keys its live "is the session still open" check on.
	SessionID uuid.UUID
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
//
// WARNING: do NOT call this directly from a module's service layer for
// ADR-AUTH-001 layer 3 branch authorization — use that module's own
// requireBranch, which additionally requires the OPA-derived tenant scope
// before honouring a chain-wide principal. The nil-BranchID == "every branch"
// semantics here are only safe for tenant-level ownership checks
// (tenant/http handler, branchAccessMiddleware), where a chain-wide
// membership legitimately means chain-wide reach. In a service acting on
// branch-scoped money or stock it fails OPEN: this check alone cannot tell a
// legitimately chain-wide principal (an owner) from one that merely lacks a
// branch, so it would hand a branch-scoped action to whoever asks.
//
// Note on why that is still true after ADR-SEC-005: 000012/000013 do now
// force a branch_scoped role's membership to carry a non-null branch_id, so
// the specific "mis-provisioned chain-wide cashier" this comment used to cite
// is no longer reachable. The warning stands for the general reason above —
// chain-wide reach is not per-branch authorization — and because the DB
// guard constrains membership rows, not this function's inputs.
//
// Enforced, not just documented: the only direct caller is
// tenant/http.branchAccessMiddleware (a tenant-level ownership check, which
// additionally verifies the branch belongs to the path tenant). Every module
// that authorizes branch-scoped work has its own deliberately stricter
// requireBranch — see payment/pos/inventory/billing service/branch_authz.go.
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
