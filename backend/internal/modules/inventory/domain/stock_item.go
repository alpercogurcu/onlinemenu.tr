package domain

import (
	"time"

	"github.com/google/uuid"
)

// StockItemKind classifies a stock item. This is a plain classification, not
// a discriminator: every column of StockItem means the same thing for every
// kind (ADR-DATA-005 İlke 1). It exists only for listing/filtering and for
// expressing invariants (e.g. "raw can be linked to a supplier", "intermediate
// is a recipe output").
type StockItemKind string

const (
	StockItemKindRaw          StockItemKind = "raw"
	StockItemKindIntermediate StockItemKind = "intermediate"
	StockItemKindPackaging    StockItemKind = "packaging"
	StockItemKindFinished     StockItemKind = "finished"
)

// Valid reports whether k is a recognised stock item kind.
func (k StockItemKind) Valid() bool {
	switch k {
	case StockItemKindRaw, StockItemKindIntermediate, StockItemKindPackaging, StockItemKindFinished:
		return true
	}
	return false
}

// StockItem is the canonical stocked entity: raw material, packaging,
// intermediate product or finished good. It is never a sellable-catalog row —
// see catalog.Product and the source_stock_item_id link (ADR-DATA-005 İlke 1).
//
// StockItem carries no cost column: cost cascade is impossible by
// construction (ADR-DATA-005 İlke 3). CanonicalUnit is the single unit of
// record; there is no parallel/legacy unit field (İlke 2).
type StockItem struct {
	ID            uuid.UUID
	TenantID      uuid.UUID
	SKU           string
	Name          string
	Kind          StockItemKind
	CanonicalUnit string
	Category      string
	IsActive      bool
	CreatedAt     time.Time
	UpdatedAt     time.Time
}
