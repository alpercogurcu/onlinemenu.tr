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
type StockLevel struct {
	ProductID uuid.UUID
	BranchID  uuid.UUID
	Quantity  float64
	UpdatedAt time.Time
}

// StockReader allows other modules (e.g. pos) to query current stock levels
// without importing inventory internals.
type StockReader interface {
	GetLevel(ctx context.Context, tenantID, branchID, productID uuid.UUID) (StockLevel, error)
}

// ErrNotFound is returned when a requested inventory resource does not exist.
var ErrNotFound = inventoryNotFoundError{}

type inventoryNotFoundError struct{}

func (inventoryNotFoundError) Error() string { return "inventory: not found" }

// ValidationError is returned when service-level domain validation fails.
// The HTTP layer checks for it with errors.As and returns 422 Unprocessable Entity.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return "inventory: " + e.Msg }
