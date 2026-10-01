package domain

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestWaiterCategoryLayout_Valid(t *testing.T) {
	tests := []struct {
		layout WaiterCategoryLayout
		want   bool
	}{
		{WaiterCategoryLayoutTop, true},
		{WaiterCategoryLayoutSide, true},
		{"", false},
		{"bottom", false},
		{"TOP", false},
	}
	for _, tt := range tests {
		assert.Equalf(t, tt.want, tt.layout.Valid(), "layout %q", tt.layout)
	}
}

func TestOrderFlow_Valid(t *testing.T) {
	tests := []struct {
		flow OrderFlow
		want bool
	}{
		{OrderFlowFull, true},
		{OrderFlowSimple, true},
		{"", false},
		{"kds", false},
		{"Simple", false},
	}
	for _, tt := range tests {
		assert.Equalf(t, tt.want, tt.flow.Valid(), "flow %q", tt.flow)
	}
}

// TestDefaultBranchSettings pins the defaults to today's behaviour: a branch
// without a settings row must read top/full, because that is exactly what
// every branch did before the row existed.
func TestDefaultBranchSettings(t *testing.T) {
	tenantID, branchID := uuid.New(), uuid.New()
	d := DefaultBranchSettings(tenantID, branchID)

	assert.Equal(t, tenantID, d.TenantID)
	assert.Equal(t, branchID, d.BranchID)
	assert.Equal(t, WaiterCategoryLayoutTop, d.WaiterCategoryLayout)
	assert.Equal(t, OrderFlowFull, d.OrderFlow)
	assert.True(t, d.WaiterCategoryLayout.Valid())
	assert.True(t, d.OrderFlow.Valid())
}
