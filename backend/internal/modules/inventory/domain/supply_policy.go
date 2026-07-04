package domain

import (
	"time"

	"github.com/google/uuid"
)

// SupplyScope is the granularity a SupplyPolicy row applies to (ADR-DATA-007).
type SupplyScope string

const (
	SupplyScopeStockItem     SupplyScope = "stock_item"
	SupplyScopeCategory      SupplyScope = "category"
	SupplyScopeTenantDefault SupplyScope = "tenant_default"
)

// Valid reports whether s is a recognised supply policy scope.
func (s SupplyScope) Valid() bool {
	switch s {
	case SupplyScopeStockItem, SupplyScopeCategory, SupplyScopeTenantDefault:
		return true
	}
	return false
}

// SupplyMode is the resolved commercial procurement rule for a stock item
// (ADR-DATA-007): whether a branch may source it exclusively from HQ, from
// an approved supplier list, or freely.
type SupplyMode string

const (
	// SupplyModeExclusiveHQ restricts sourcing to HQ/imalat (via BTO). This
	// is also the safe default applied when no policy row matches at all —
	// see DefaultSupplyMode.
	SupplyModeExclusiveHQ       SupplyMode = "exclusive_hq"
	SupplyModeApprovedSuppliers SupplyMode = "approved_suppliers"
	SupplyModeFree              SupplyMode = "free"
)

// Valid reports whether m is a recognised supply mode.
func (m SupplyMode) Valid() bool {
	switch m {
	case SupplyModeExclusiveHQ, SupplyModeApprovedSuppliers, SupplyModeFree:
		return true
	}
	return false
}

// DefaultSupplyMode is the mode resolved when no supply policy row applies
// to a stock item at all (ADR-DATA-007: the franchisor-protective safe
// default — a franchise cannot free-source a raw material simply because
// nobody has configured a policy for it yet).
const DefaultSupplyMode = SupplyModeExclusiveHQ

// SupplyPolicy is a time-versioned commercial procurement rule (ADR-DATA-007).
// Policy changes never UPDATE an existing row (DATA-002 immutability ruhu):
// a new row with a later EffectiveFrom supersedes the previous one for its
// resolution key. Cost/visibility for a stock item is never a column on
// StockItem itself — it is always derived by resolving the applicable
// SupplyPolicy (see ResolvePolicy).
type SupplyPolicy struct {
	ID                  uuid.UUID
	TenantID            uuid.UUID
	BranchID            *uuid.UUID // nil = tenant-wide; non-nil = branch (franchise) override
	Scope               SupplyScope
	StockItemID         *uuid.UUID // set iff Scope == SupplyScopeStockItem
	Category            string     // set iff Scope == SupplyScopeCategory
	Mode                SupplyMode
	ApprovedSupplierIDs []uuid.UUID // meaningful iff Mode == SupplyModeApprovedSuppliers
	EffectiveFrom       time.Time
	CreatedBy           *uuid.UUID
	CreatedAt           time.Time
}

// ResolvePolicy resolves the effective SupplyMode (and, when applicable, the
// approved supplier list) for a stock item at a branch at a point in time.
//
// It is a pure function over the full candidate set: callers load every
// SupplyPolicy row potentially relevant to the tenant (there is no SQL-side
// "is active" filter — see migrations/inventory/000006) and this function
// picks the winner.
//
// Resolution priority (ADR-DATA-007, most specific wins; within a tier the
// row with the greatest EffectiveFrom <= at wins; a tier with no active row
// falls through to the next):
//
//	branch+item > branch+category > tenant+item > tenant+category > tenant_default
//
// If no row applies at any tier, the result is DefaultSupplyMode
// (exclusive_hq) with no approved suppliers.
func ResolvePolicy(policies []SupplyPolicy, item StockItem, branchID uuid.UUID, at time.Time) (SupplyMode, []uuid.UUID) {
	tiers := []func(SupplyPolicy) bool{
		func(p SupplyPolicy) bool {
			return branchID != uuid.Nil && p.BranchID != nil && *p.BranchID == branchID &&
				p.Scope == SupplyScopeStockItem && p.StockItemID != nil && *p.StockItemID == item.ID
		},
		func(p SupplyPolicy) bool {
			return branchID != uuid.Nil && p.BranchID != nil && *p.BranchID == branchID &&
				p.Scope == SupplyScopeCategory && item.Category != "" && p.Category == item.Category
		},
		func(p SupplyPolicy) bool {
			return p.BranchID == nil &&
				p.Scope == SupplyScopeStockItem && p.StockItemID != nil && *p.StockItemID == item.ID
		},
		func(p SupplyPolicy) bool {
			return p.BranchID == nil &&
				p.Scope == SupplyScopeCategory && item.Category != "" && p.Category == item.Category
		},
		func(p SupplyPolicy) bool {
			return p.BranchID == nil && p.Scope == SupplyScopeTenantDefault
		},
	}

	for _, match := range tiers {
		if winner, ok := latestActive(policies, at, match); ok {
			return winner.Mode, winner.ApprovedSupplierIDs
		}
	}
	return DefaultSupplyMode, nil
}

// latestActive returns the SupplyPolicy matching `match` with the greatest
// EffectiveFrom that is still <= at ("active" as of at), or false if none
// match.
func latestActive(policies []SupplyPolicy, at time.Time, match func(SupplyPolicy) bool) (SupplyPolicy, bool) {
	var best SupplyPolicy
	found := false
	for _, p := range policies {
		if p.EffectiveFrom.After(at) {
			continue
		}
		if !match(p) {
			continue
		}
		if !found || p.EffectiveFrom.After(best.EffectiveFrom) {
			best = p
			found = true
		}
	}
	return best, found
}
