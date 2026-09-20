package domain

import (
	"time"

	"github.com/google/uuid"
)

// BranchProductOverride is how one branch deviates from the tenant-wide
// catalog for one product (ADR-DATA-009).
//
// The absence of a row means "tenant default", which is why PriceAmount is a
// pointer rather than a zero value: 0 kuruş is a legal sale price (a giveaway
// line), so it cannot double as "no override".
type BranchProductOverride struct {
	TenantID    uuid.UUID
	BranchID    uuid.UUID
	ProductID   uuid.UUID
	IsAvailable bool
	PriceAmount *int64
	UpdatedAt   time.Time
}

// BranchProduct is a product as one branch actually sells it: the catalog row
// with the branch's effective price already applied.
//
// BranchPriceOverridden exists so admin and POS can label a price as
// branch-specific. It is derived, never persisted — the embedded Product
// carries the effective price, not the tenant one, so a caller that ignores
// this flag still bills correctly.
type BranchProduct struct {
	Product
	BranchPriceOverridden bool
}
