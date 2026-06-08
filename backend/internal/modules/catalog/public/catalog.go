// Package public exposes the catalog module's contract to other modules.
// Imports of internal catalog packages (domain, repo, http) from outside
// the catalog module are forbidden by go-arch-lint.
package public

import (
	"context"

	"github.com/google/uuid"
)

// Product is the read-only projection other modules may reference.
// Carries only the fields that cross-module consumers legitimately need.
type Product struct {
	ID          uuid.UUID
	Name        string
	PriceAmount int64 // kuruş
	Currency    string
	TaxRateBPS  int
	IsActive    bool
}

// ProductReader allows other modules (e.g. pos, delivery) to look up product data
// without importing catalog internals.
type ProductReader interface {
	GetByID(ctx context.Context, tenantID, productID uuid.UUID) (Product, error)
}

// ErrNotFound is returned when a requested catalog resource does not exist.
var ErrNotFound = catalogNotFoundError{}

type catalogNotFoundError struct{}

func (catalogNotFoundError) Error() string { return "catalog: not found" }

// ValidationError is returned when service-level domain validation fails.
// The HTTP layer checks for it with errors.As and returns 422 Unprocessable Entity.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return "catalog: " + e.Msg }
