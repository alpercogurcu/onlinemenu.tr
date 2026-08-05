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

// ---------------------------------------------------------------------------
// Storefront read model (ADR-ARCH-006 §6)
//
// The types below are the ONLY catalog data an anonymous diner may reach.
// They deliberately have no field for cost price, stock quantity, supplier or
// internal notes: a widening query would have nowhere to put those columns,
// and a future handler cannot leak what the type cannot carry.
// ---------------------------------------------------------------------------

// StorefrontModifier is one selectable option and its price delta (kuruş,
// may be negative).
type StorefrontModifier struct {
	ID         uuid.UUID
	Name       string
	PriceDelta int64
}

// StorefrontModifierGroup is a set of options attached to a product.
// MaxSelect 0 means "no upper bound" (modifier_groups.max_selections IS NULL).
type StorefrontModifierGroup struct {
	ID            uuid.UUID
	Name          string
	SelectionType string
	MinSelect     int
	MaxSelect     int
	Modifiers     []StorefrontModifier
}

// StorefrontProduct is a sellable item as the diner sees it.
//
// PriceAmount is the effective sale price in kuruş, resolved server-side from
// menu_items.price_override ?? products.price_amount — never from the client.
//
// ImageKey is the object-storage key (products.image_key), NOT a URL: nothing
// in this codebase mints signed/CDN URLs yet, so returning it under a *_url
// name would hand the storefront a string it cannot put in an <img> tag.
//
// Allergens is always empty today: no allergen column exists anywhere in the
// catalog schema. The field is part of the contract so adding the column
// later does not change the wire shape.
type StorefrontProduct struct {
	ID             uuid.UUID
	Name           string
	Description    string
	PriceAmount    int64
	Currency       string
	ImageKey       string
	Allergens      []string
	IsAvailable    bool
	ModifierGroups []StorefrontModifierGroup
}

// StorefrontCategory groups products for display. A zero ID with an empty
// Name is the synthetic "uncategorised" bucket: products.category_id is
// nullable (ON DELETE SET NULL), and silently dropping such products would
// hide sellable items from the menu.
type StorefrontCategory struct {
	ID        uuid.UUID
	Name      string
	SortOrder int16
	Products  []StorefrontProduct
}

// CartLine is one submitted cart line. It carries no price — see PriceCart.
type CartLine struct {
	ProductID   uuid.UUID
	Quantity    int
	ModifierIDs []uuid.UUID
}

// PricedModifier is a modifier as re-priced by the server.
type PricedModifier struct {
	ID         uuid.UUID
	Name       string
	PriceDelta int64
}

// PricedLine is a cart line after server-side re-pricing.
//
// UnitPriceAmount already includes the selected modifiers' deltas, because
// pos bills a line as quantity × unit_price_amount (pos CheckRepo.GetTotal):
// any delta kept in a separate field would never be charged.
type PricedLine struct {
	ProductID       uuid.UUID
	ProductName     string
	BasePriceAmount int64
	UnitPriceAmount int64
	Currency        string
	TaxRateBPS      int
	Quantity        int
	Modifiers       []PricedModifier
}

// StorefrontMenuReader is the storefront module's only door into the catalog.
//
// Both methods resolve prices through one shared SQL predicate (see
// catalog/repo.storefrontMenuItemsCTE): a branch can match several active
// menus, so "which menu wins" must be decided in exactly one place, or the
// server could re-price an order using a menu the diner was never shown.
// PriceCart returns exactly one PricedLine per input line, in input order, or
// a *ValidationError — never a shortened slice: silently dropping an
// unorderable line would hand the diner a cheaper order than the one they
// submitted. Callers rely on the positional correspondence to carry their own
// per-line data (notes) through.
type StorefrontMenuReader interface {
	GetStorefrontMenu(ctx context.Context, tenantID, branchID uuid.UUID) ([]StorefrontCategory, error)
	PriceCart(ctx context.Context, tenantID, branchID uuid.UUID, lines []CartLine) ([]PricedLine, error)
}

// ErrNotFound is returned when a requested catalog resource does not exist.
var ErrNotFound = catalogNotFoundError{}

type catalogNotFoundError struct{}

func (catalogNotFoundError) Error() string { return "catalog: not found" }

// ValidationError is returned when service-level domain validation fails.
// The HTTP layer checks for it with errors.As and returns 422 Unprocessable Entity.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return "catalog: " + e.Msg }
