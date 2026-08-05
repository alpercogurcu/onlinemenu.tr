package domain

import "github.com/google/uuid"

// The Guest* types below are the customer-facing menu read model (ADR-ARCH-006
// §6). They deliberately declare ONLY what an anonymous diner is allowed to
// see. Cost price, stock levels, supplier and internal notes have no field
// here at all — not an unexported one, not one with `json:"-"` — so a future
// handler cannot leak them by accident, and a widening query has nowhere to
// put the extra columns.

// GuestModifier is a single selectable option (e.g. "extra shot").
type GuestModifier struct {
	ID         uuid.UUID
	Name       string
	PriceDelta int64
}

// GuestModifierGroup is a set of options attached to a product.
type GuestModifierGroup struct {
	ID            uuid.UUID
	Name          string
	SelectionType string
	MinSelect     int
	MaxSelect     int
	Modifiers     []GuestModifier
}

// GuestProduct is a sellable item as the diner sees it. PriceAmount is the
// effective sale price in kuruş, resolved server-side from
// menu_items.price_override ?? products.price_amount.
type GuestProduct struct {
	ID             uuid.UUID
	Name           string
	Description    string
	PriceAmount    int64
	Currency       string
	ImageURL       string
	Allergens      []string
	IsAvailable    bool
	ModifierGroups []GuestModifierGroup
}

// GuestCategory groups products for display.
type GuestCategory struct {
	ID        uuid.UUID
	Name      string
	SortOrder int16
	Products  []GuestProduct
}
