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

// StaffCartLine is one line of a staff-placed (POS) order as submitted: what
// was ordered and which options were chosen. Like CartLine it carries no
// price — see StaffPricer.
type StaffCartLine struct {
	ProductID   uuid.UUID
	Quantity    int
	ModifierIDs []uuid.UUID
}

// StaffPricer re-derives what a staff-placed order line must cost, so the POS
// cannot name its own price (docs/pos-ux-spec.md bulgu #14: until this
// existed, pos/service.OrderService.Place copied the client's
// unit_price_amount straight into the order, which made every POS terminal a
// discount channel).
//
// It sits beside StorefrontMenuReader.PriceCart instead of reusing it because
// the two answer different questions. PriceCart answers "what was this diner
// shown", so a product outside the branch's active menu is not orderable at
// all. A cashier sells from the product catalog itself: POS has always billed
// products.price_amount (that is what the product list endpoint pos-desktop
// reads returns), and making an active menu a precondition for every counter
// sale would stop a tenant that has configured none from taking any order.
// Aligning the two onto menu-resolved pricing is a product decision, not a
// security fix, and is deliberately left out of this one.
//
// Modifier validation is NOT duplicated: both paths run the same rules (the
// modifier must be attached to the product, may not repeat, may not exceed
// its group's selection limit, may not drive the line negative) through one
// shared implementation — those rules are the only server-side bound on how
// far a client can move a line's price, and a second copy would drift.
//
// Returns exactly one PricedLine per input line, in input order, or a
// *ValidationError; never a shortened slice, for the reason PriceCart gives.
//
// branchID names the branch the sale happens at (ADR-DATA-009): the tenant's
// catalog is the default, but a branch may sell a product at its own price or
// not sell it at all. uuid.Nil means "no branch named" and resolves every
// line to the tenant default, which is what a takeaway/delivery caller with
// no branch context gets. A product the branch has switched off is rejected
// as a *ValidationError, not silently priced at the tenant rate.
type StaffPricer interface {
	PriceStaffCart(ctx context.Context, tenantID, branchID uuid.UUID, lines []StaffCartLine) ([]PricedLine, error)
}

// ErrBranchForbidden is returned when a branch-scoped principal reaches for a
// branch that is not theirs (ADR-AUTH-001 layer 3 / ADR-SEC-005). The HTTP
// layer maps it to 403 with code "branch_forbidden", matching pos and payment.
var ErrBranchForbidden = catalogBranchForbiddenError{}

type catalogBranchForbiddenError struct{}

func (catalogBranchForbiddenError) Error() string { return "catalog: branch forbidden" }

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
