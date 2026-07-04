// Package public exposes the inventory module's contract to other modules.
// Imports of internal inventory packages (domain, repo, http) from outside
// the inventory module are forbidden by go-arch-lint.
package public

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// StockLevel is the read-only projection other modules may use to check stock.
// Stock is warehouse-scoped (ADR-DATA-005): callers resolve a warehouse_id
// (e.g. from the branch's default warehouse) before querying.
type StockLevel struct {
	StockItemID uuid.UUID
	WarehouseID uuid.UUID
	OnHand      float64
	Available   float64
	UpdatedAt   time.Time
}

// StockReader allows other modules (e.g. pos) to query current stock levels
// without importing inventory internals.
type StockReader interface {
	GetLevel(ctx context.Context, tenantID, warehouseID, stockItemID uuid.UUID) (StockLevel, error)
}

// ErrNotFound is returned when a requested inventory resource does not exist.
var ErrNotFound = inventoryNotFoundError{}

type inventoryNotFoundError struct{}

func (inventoryNotFoundError) Error() string { return "inventory: not found" }

// ValidationError is returned when service-level domain validation fails.
// The HTTP layer checks for it with errors.As and returns 422 Unprocessable Entity.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return "inventory: " + e.Msg }

// TransitionError is returned when a status transition is not allowed from
// the resource's current status (BranchTransferOrder / Shipment state
// machines). The HTTP layer checks for it with errors.As and returns 409
// Conflict.
type TransitionError struct{ Msg string }

func (e *TransitionError) Error() string { return "inventory: " + e.Msg }

// ErrBranchForbidden is returned when the acting principal attempts a
// branch-scoped action (ADR-DATA-006: BTO submit/approve/reject/cancel/
// fulfil, shipment create/approve/advance/cancel/receive, stock movement
// create, warehouse update/delete) on a resource belonging to a branch it
// does not have access to (ADR-AUTH-001, layer 3). Tenant-wide principals
// (OPA scope "tenant", e.g. manager) are exempt. The resource is already
// known to be tenant-visible (RLS, layer 1, already passed) so this is not
// treated as a not-found — the HTTP layer maps it to 403 Forbidden.
var ErrBranchForbidden = inventoryBranchForbiddenError{}

type inventoryBranchForbiddenError struct{}

func (inventoryBranchForbiddenError) Error() string { return "inventory: forbidden for this branch" }
