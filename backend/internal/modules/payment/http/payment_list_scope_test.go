package http

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"onlinemenu.tr/internal/modules/payment/domain"
)

// TestToPaymentResponses_BranchScope covers the DTO-side half of the payment
// list guard (ADR-AUTH-001 layer 3 / docs/lessons-from-b2b.md §2).
//
// GET /api/v1/payments?check_id=... cannot narrow in SQL: ListByCheck is also
// what pos consults as the double-payment guard, so changing its query would
// change what the service reports as already paid. The wire answer is filtered
// here instead — and a nil scope (the chain manager) must stay unfiltered,
// because the tenant-wide reconciliation view depends on it.
func TestToPaymentResponses_BranchScope(t *testing.T) {
	branchA := uuid.MustParse("11111111-0000-0000-0000-0000000000aa")
	branchB := uuid.MustParse("22222222-0000-0000-0000-0000000000bb")

	atA := domain.Payment{ID: uuid.New(), BranchID: branchA, Method: domain.PaymentMethodCash, AmountTotal: 100, Currency: "TRY"}
	atB := domain.Payment{ID: uuid.New(), BranchID: branchB, Method: domain.PaymentMethodCash, AmountTotal: 200, Currency: "TRY"}

	tests := []struct {
		name     string
		scope    *uuid.UUID
		payments []domain.Payment
		wantIDs  []uuid.UUID
	}{
		{
			name:     "nil scope keeps every branch",
			scope:    nil,
			payments: []domain.Payment{atA, atB},
			wantIDs:  []uuid.UUID{atA.ID, atB.ID},
		},
		{
			name:     "branch scope drops other branches",
			scope:    &branchB,
			payments: []domain.Payment{atA, atB},
			wantIDs:  []uuid.UUID{atB.ID},
		},
		{
			name:     "no visible row yields an empty list, not null",
			scope:    &branchB,
			payments: []domain.Payment{atA},
			wantIDs:  []uuid.UUID{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := toPaymentResponses(tt.payments, tt.scope)

			// Never nil: the handler marshals this straight into
			// {"payments": ...} and a nil slice would emit JSON null, which
			// the POS client iterates over.
			require.NotNil(t, got)

			ids := make([]uuid.UUID, 0, len(got))
			for _, row := range got {
				ids = append(ids, row.ID)
			}
			assert.Equal(t, tt.wantIDs, ids)
		})
	}
}
