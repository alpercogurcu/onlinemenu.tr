package domain

import (
	"time"

	"github.com/google/uuid"
)

// BranchSettings is a branch's storefront configuration row
// (storefront_branch_settings). A branch without a row behaves exactly like
// one whose every field holds its default: the row is created lazily on the
// first admin change, never on read, so "no row" and "all defaults" must stay
// indistinguishable to every consumer.
type BranchSettings struct {
	TenantID        uuid.UUID
	BranchID        uuid.UUID
	OrderingEnabled bool
	UpdatedBy       uuid.UUID
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// DefaultBranchSettings is what a branch without a settings row behaves like.
// It is the single definition of the defaults: the admin GET and the guest
// ordering gate both fall back to it, so the two surfaces can never disagree
// about what an untouched branch does.
func DefaultBranchSettings(tenantID, branchID uuid.UUID) BranchSettings {
	return BranchSettings{
		TenantID:        tenantID,
		BranchID:        branchID,
		OrderingEnabled: true,
	}
}
