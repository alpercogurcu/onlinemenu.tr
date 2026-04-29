// Package auth provides JWT validation middleware, principal extraction,
// and OPA-based authorization for the chi HTTP router.
package auth

import (
	"github.com/google/uuid"
)

// Principal carries the authenticated identity for a single request.
// It is stored in the request context via middleware and consumed by
// service and OPA layers. The domain model does not reference Principal directly.
type Principal struct {
	// Sub is the Keycloak subject claim (unique user identifier).
	Sub string

	// TenantID is derived from the Keycloak token's tenant claim.
	TenantID uuid.UUID

	// BranchIDs lists the branches this principal is authorized to access.
	// An empty slice means no branch-level restriction (tenant-wide access).
	BranchIDs []uuid.UUID

	// Roles holds the Keycloak realm roles for this principal.
	Roles []string
}

// HasRole reports whether the principal holds the given role.
func (p Principal) HasRole(role string) bool {
	for _, r := range p.Roles {
		if r == role {
			return true
		}
	}
	return false
}

// HasBranchAccess reports whether the principal can access the given branch.
// Principals with an empty BranchIDs slice have unrestricted branch access.
func (p Principal) HasBranchAccess(branchID uuid.UUID) bool {
	if len(p.BranchIDs) == 0 {
		return true
	}
	for _, id := range p.BranchIDs {
		if id == branchID {
			return true
		}
	}
	return false
}

type contextKey struct{}

// principalKey is the context key for storing Principal values.
var principalKey = contextKey{}
