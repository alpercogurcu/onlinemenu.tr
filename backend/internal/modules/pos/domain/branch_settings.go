package domain

import (
	"time"

	"github.com/google/uuid"
)

// WaiterCategoryLayout is where the waiter order screen renders its category
// strip. It is a pure UI preference the backend only stores and serves — no
// service behaviour branches on it.
type WaiterCategoryLayout string

const (
	WaiterCategoryLayoutTop  WaiterCategoryLayout = "top"
	WaiterCategoryLayoutSide WaiterCategoryLayout = "side"
)

func (l WaiterCategoryLayout) Valid() bool {
	switch l {
	case WaiterCategoryLayoutTop, WaiterCategoryLayoutSide:
		return true
	}
	return false
}

// OrderFlow is the branch's order lifecycle mode.
//
// "full" is the pre-existing behaviour: orders are born pending and move
// through accept/advance on the kitchen display.
//
// "simple" is for branches that run on kitchen receipts alone (no KDS, no
// status data entry): an order — staff and guest QR alike — is born accepted
// (the counter-approval step the branch never performs is skipped), and
// closing a check pulls its still-live orders to delivered in the same
// transaction, since "the check is being paid" is the only delivery signal
// such a branch ever produces.
type OrderFlow string

const (
	OrderFlowFull   OrderFlow = "full"
	OrderFlowSimple OrderFlow = "simple"
)

func (f OrderFlow) Valid() bool {
	switch f {
	case OrderFlowFull, OrderFlowSimple:
		return true
	}
	return false
}

// BranchSettings is a branch's POS configuration row (pos_branch_settings).
// A branch without a row behaves exactly like one whose every field holds its
// default: the row is created lazily on the first admin change, never on
// read, so "no row" and "all defaults" must stay indistinguishable to every
// consumer — the same contract storefront's branch settings follow.
type BranchSettings struct {
	TenantID             uuid.UUID
	BranchID             uuid.UUID
	WaiterCategoryLayout WaiterCategoryLayout
	OrderFlow            OrderFlow
	Rounding             RoundingPolicy
	UpdatedBy            uuid.UUID
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

// DefaultBranchSettings is what a branch without a settings row behaves like.
// It is the single definition of the defaults: the admin GET, the PUT upsert
// and the service-layer order-flow reads all fall back to it, so no two
// surfaces can disagree about what an untouched branch does.
func DefaultBranchSettings(tenantID, branchID uuid.UUID) BranchSettings {
	return BranchSettings{
		TenantID:             tenantID,
		BranchID:             branchID,
		WaiterCategoryLayout: WaiterCategoryLayoutTop,
		OrderFlow:            OrderFlowFull,
		Rounding:             DefaultRoundingPolicy(),
	}
}

// RoundingStepsMinor are the rounding steps a branch may pick, in kuruş
// (₺0,50 / ₺1 / ₺5 / ₺10). Mirrors the pos_branch_settings CHECK constraint.
var RoundingStepsMinor = []int64{50, 100, 500, 1000}

// ValidRoundingStep reports whether step is one of RoundingStepsMinor.
func ValidRoundingStep(step int64) bool {
	for _, s := range RoundingStepsMinor {
		if s == step {
			return true
		}
	}
	return false
}

// RoundingPolicy is the branch's permission for cash rounding (beşli
// yuvarlama): which payment methods may round the final remainder down, to
// which step, and how much a single check may be conceded in total. The
// cashier applies it per payment; nothing rounds automatically.
type RoundingPolicy struct {
	CashEnabled      bool
	CardEnabled      bool
	StepMinor        int64
	MaxPerCheckMinor int64
}

// DefaultRoundingPolicy is rounding switched off, matching the column
// defaults of pos/000014.
func DefaultRoundingPolicy() RoundingPolicy {
	return RoundingPolicy{StepMinor: 500, MaxPerCheckMinor: 1000}
}
