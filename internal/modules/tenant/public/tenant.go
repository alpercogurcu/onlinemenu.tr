// Package public exposes the tenant module's contract to other modules.
// Imports of internal tenant packages from outside the tenant module are
// forbidden by go-arch-lint.
package public

import (
	"context"

	"github.com/google/uuid"
)

// Plan represents the subscription tier for a tenant.
type Plan string

const (
	PlanStarter    Plan = "starter"
	PlanPro        Plan = "pro"
	PlanEnterprise Plan = "enterprise"
)

// Tenant is the read-only projection that other modules may reference.
type Tenant struct {
	ID             uuid.UUID
	Name           string
	Slug           string
	Plan           Plan
	EnabledModules []string
	IsActive       bool
}

// Branch is the read-only projection of a physical location belonging to a tenant.
type Branch struct {
	ID       uuid.UUID
	TenantID uuid.UUID
	Name     string
	IsActive bool
}

// TenantReader allows other modules to look up tenant and branch data
// without importing tenant internals. Implemented by tenant.Service.
type TenantReader interface {
	// GetByID returns the tenant for the given ID.
	GetByID(ctx context.Context, tenantID uuid.UUID) (Tenant, error)

	// GetBranch returns the branch for the given tenant and branch ID.
	GetBranch(ctx context.Context, tenantID, branchID uuid.UUID) (Branch, error)

	// IsModuleEnabled reports whether the given module slug is active for the tenant.
	IsModuleEnabled(ctx context.Context, tenantID uuid.UUID, module string) (bool, error)
}

// ErrNotFound is returned when a requested resource does not exist.
var ErrNotFound = tenantNotFoundError{}

type tenantNotFoundError struct{}

func (tenantNotFoundError) Error() string { return "tenant: not found" }
