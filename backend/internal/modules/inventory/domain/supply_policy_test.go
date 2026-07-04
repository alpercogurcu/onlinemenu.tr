package domain_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	"onlinemenu.tr/internal/modules/inventory/domain"
)

func TestSupplyScope_Valid(t *testing.T) {
	valid := []domain.SupplyScope{
		domain.SupplyScopeStockItem, domain.SupplyScopeCategory, domain.SupplyScopeTenantDefault,
	}
	for _, s := range valid {
		assert.True(t, s.Valid(), "expected %q to be valid", s)
	}
	assert.False(t, domain.SupplyScope("unknown").Valid())
}

func TestSupplyMode_Valid(t *testing.T) {
	valid := []domain.SupplyMode{
		domain.SupplyModeExclusiveHQ, domain.SupplyModeApprovedSuppliers, domain.SupplyModeFree,
	}
	for _, m := range valid {
		assert.True(t, m.Valid(), "expected %q to be valid", m)
	}
	assert.False(t, domain.SupplyMode("unknown").Valid())
}

// TestResolvePolicy_PriorityMatrix is the ADR-DATA-007 resolution-priority
// regression test: branch+item > branch+category > tenant+item >
// tenant+category > tenant_default > (no row -> exclusive_hq default).
func TestResolvePolicy_PriorityMatrix(t *testing.T) {
	now := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	past := now.Add(-24 * time.Hour)

	item := domain.StockItem{ID: uuid.MustParse("aaaaaaaa-0000-0000-0000-000000000001"), Category: "meat"}
	branchID := uuid.MustParse("bbbbbbbb-0000-0000-0000-000000000001")
	otherBranchID := uuid.MustParse("bbbbbbbb-0000-0000-0000-000000000002")
	supplierA := uuid.MustParse("cccccccc-0000-0000-0000-000000000001")
	supplierB := uuid.MustParse("cccccccc-0000-0000-0000-000000000002")

	tenantDefault := domain.SupplyPolicy{
		Scope: domain.SupplyScopeTenantDefault, Mode: domain.SupplyModeFree, EffectiveFrom: past,
	}
	tenantCategory := domain.SupplyPolicy{
		Scope: domain.SupplyScopeCategory, Category: "meat", Mode: domain.SupplyModeApprovedSuppliers,
		ApprovedSupplierIDs: []uuid.UUID{supplierA}, EffectiveFrom: past,
	}
	tenantItem := domain.SupplyPolicy{
		Scope: domain.SupplyScopeStockItem, StockItemID: &item.ID, Mode: domain.SupplyModeExclusiveHQ,
		EffectiveFrom: past,
	}
	branchCategory := domain.SupplyPolicy{
		BranchID: &branchID, Scope: domain.SupplyScopeCategory, Category: "meat",
		Mode: domain.SupplyModeApprovedSuppliers, ApprovedSupplierIDs: []uuid.UUID{supplierB}, EffectiveFrom: past,
	}
	branchItem := domain.SupplyPolicy{
		BranchID: &branchID, Scope: domain.SupplyScopeStockItem, StockItemID: &item.ID,
		Mode: domain.SupplyModeFree, EffectiveFrom: past,
	}

	tests := []struct {
		name         string
		policies     []domain.SupplyPolicy
		branchID     uuid.UUID
		wantMode     domain.SupplyMode
		wantApproved []uuid.UUID
	}{
		{
			name:     "no policies at all falls back to exclusive_hq default",
			policies: nil,
			branchID: branchID,
			wantMode: domain.SupplyModeExclusiveHQ,
		},
		{
			name:     "tenant_default only",
			policies: []domain.SupplyPolicy{tenantDefault},
			branchID: branchID,
			wantMode: domain.SupplyModeFree,
		},
		{
			name:     "tenant+category beats tenant_default",
			policies: []domain.SupplyPolicy{tenantDefault, tenantCategory},
			branchID: branchID,
			wantMode: domain.SupplyModeApprovedSuppliers, wantApproved: []uuid.UUID{supplierA},
		},
		{
			name:     "tenant+item beats tenant+category and tenant_default",
			policies: []domain.SupplyPolicy{tenantDefault, tenantCategory, tenantItem},
			branchID: branchID,
			wantMode: domain.SupplyModeExclusiveHQ,
		},
		{
			name:     "branch+category beats tenant+item",
			policies: []domain.SupplyPolicy{tenantDefault, tenantCategory, tenantItem, branchCategory},
			branchID: branchID,
			wantMode: domain.SupplyModeApprovedSuppliers, wantApproved: []uuid.UUID{supplierB},
		},
		{
			name:     "branch+item beats everything (highest priority)",
			policies: []domain.SupplyPolicy{tenantDefault, tenantCategory, tenantItem, branchCategory, branchItem},
			branchID: branchID,
			wantMode: domain.SupplyModeFree,
		},
		{
			name: "branch override does not leak to a different branch",
			policies: []domain.SupplyPolicy{
				tenantItem, branchItem, // branchItem is scoped to branchID, not otherBranchID
			},
			branchID: otherBranchID,
			wantMode: domain.SupplyModeExclusiveHQ,
		},
		{
			name: "empty item category never matches a category-scoped policy",
			policies: []domain.SupplyPolicy{
				{Scope: domain.SupplyScopeCategory, Category: "", Mode: domain.SupplyModeFree, EffectiveFrom: past},
			},
			branchID: branchID,
			wantMode: domain.SupplyModeExclusiveHQ,
		},
		{
			name: "future-dated branch+item override falls through to active tenant+item",
			policies: []domain.SupplyPolicy{
				tenantItem,
				{
					BranchID: &branchID, Scope: domain.SupplyScopeStockItem, StockItemID: &item.ID,
					Mode: domain.SupplyModeFree, EffectiveFrom: now.Add(48 * time.Hour), // not yet effective
				},
			},
			branchID: branchID,
			wantMode: domain.SupplyModeExclusiveHQ,
		},
		{
			name: "later effective_from within the same tier wins",
			policies: []domain.SupplyPolicy{
				{Scope: domain.SupplyScopeTenantDefault, Mode: domain.SupplyModeExclusiveHQ, EffectiveFrom: past},
				{Scope: domain.SupplyScopeTenantDefault, Mode: domain.SupplyModeFree, EffectiveFrom: past.Add(1 * time.Hour)},
			},
			branchID: branchID,
			wantMode: domain.SupplyModeFree,
		},
		{
			name:     "zero branchID never matches a branch-scoped policy",
			policies: []domain.SupplyPolicy{branchItem, tenantItem},
			branchID: uuid.Nil,
			wantMode: domain.SupplyModeExclusiveHQ,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotMode, gotApproved := domain.ResolvePolicy(tt.policies, item, tt.branchID, now)
			assert.Equal(t, tt.wantMode, gotMode)
			assert.Equal(t, tt.wantApproved, gotApproved)
		})
	}
}
