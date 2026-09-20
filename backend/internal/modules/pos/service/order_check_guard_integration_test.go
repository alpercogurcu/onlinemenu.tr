package service_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"onlinemenu.tr/internal/modules/pos/domain"
	pub "onlinemenu.tr/internal/modules/pos/public"
)

// TestOrderService_Place_ChecksCheckIsWritable is the regression for the
// 2026-09-15 production finding: POST /api/v1/pos/orders accepted orders
// against closed and cancelled checks (201), because Place never looked at
// the check at all. Only a real database can exercise it — the guard reads
// and locks the persisted check row.
func TestOrderService_Place_ChecksCheckIsWritable(t *testing.T) {
	ctx := context.Background()
	checks := newCheckService()
	orders := newOrderService()

	closedCheck := openTestCheck(t, ctx, checks)
	_, err := checks.Close(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), closedCheck.ID, staffA)
	require.NoError(t, err)

	cancelledCheck := openTestCheck(t, ctx, checks)
	_, err = checks.Cancel(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), cancelledCheck.ID, staffA)
	require.NoError(t, err)

	openCheck := openTestCheck(t, ctx, checks)

	otherBranchCheck, err := checks.Open(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), domain.Check{
		BranchID:   branchB,
		TableLabel: "Masa Guard B",
		OpenedBy:   &staffA,
	})
	require.NoError(t, err)

	missingCheckID := uuid.New()

	tests := []struct {
		name     string
		checkID  *uuid.UUID
		branchID uuid.UUID
		wantErr  error
	}{
		{
			name:     "open check in the order's branch",
			checkID:  &openCheck.ID,
			branchID: branchA,
		},
		{
			name:     "no check at all (takeaway)",
			branchID: branchA,
		},
		{
			name:     "closed check",
			checkID:  &closedCheck.ID,
			branchID: branchA,
			wantErr:  pub.ErrCheckNotOpen,
		},
		{
			name:     "cancelled check",
			checkID:  &cancelledCheck.ID,
			branchID: branchA,
			wantErr:  pub.ErrCheckNotOpen,
		},
		{
			name:     "check belongs to another branch",
			checkID:  &otherBranchCheck.ID,
			branchID: branchA,
			wantErr:  pub.ErrCheckBranchMismatch,
		},
		{
			name:     "check does not exist",
			checkID:  &missingCheckID,
			branchID: branchA,
			wantErr:  pub.ErrNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			placed, err := orders.Place(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), domain.Order{
				BranchID:     tt.branchID,
				CheckID:      tt.checkID,
				OrderChannel: domain.OrderChannelDineIn,
				Items: []domain.OrderItem{
					{ProductID: testProduct("Çay", 1000), ProductName: "Çay", ProductCurrency: "TRY", Quantity: 1, UnitPriceAmount: 1000},
				},
			})
			if tt.wantErr == nil {
				require.NoError(t, err)
				assert.Equal(t, domain.OrderStatusPending, placed.Status)
				return
			}
			assert.ErrorIs(t, err, tt.wantErr)
			assert.Equal(t, uuid.Nil, placed.ID, "a rejected order must not be persisted")
		})
	}
}
