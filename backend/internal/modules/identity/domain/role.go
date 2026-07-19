package domain

import (
	"time"

	"github.com/google/uuid"
)

// RoleScope classifies where a role was defined.
type RoleScope string

const (
	RoleScopeSystem RoleScope = "system" // tenant_id IS NULL
	RoleScopeTenant RoleScope = "tenant" // tenant-wide custom role
	RoleScopeBranch RoleScope = "branch" // branch-specific custom role
)

// Role defines a named permission profile. System roles (IsSystem=true) are seeded
// at migration time with tenant_id=nil and serve as immutable templates.
// Tenant and branch admins may create custom roles derived from system defaults.
type Role struct {
	ID        uuid.UUID
	TenantID  *uuid.UUID // nil = system role
	BranchID  *uuid.UUID // nil = system or tenant-wide role
	Name      string
	SystemKey string // non-empty only for system roles: "cashier", "manager", …
	IsSystem  bool
	// BranchScoped marks roles that may only be granted at a concrete branch.
	// Unlike Scope(), this survives tenant cloning (the clone copies the flag),
	// which is why it — not Scope() — is the authoritative check.
	BranchScoped bool
	CreatedAt    time.Time
}

// RequiresBranch reports whether a membership granting this role must name a branch.
func (r Role) RequiresBranch() bool {
	return r.BranchScoped || r.Scope() == RoleScopeBranch
}

// Scope returns the classification of this role based on its tenant/branch context.
func (r Role) Scope() RoleScope {
	if r.TenantID == nil {
		return RoleScopeSystem
	}
	if r.BranchID == nil {
		return RoleScopeTenant
	}
	return RoleScopeBranch
}
