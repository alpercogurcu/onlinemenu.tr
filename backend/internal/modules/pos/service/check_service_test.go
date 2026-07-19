package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestPaymentCoversTotal covers the three-way split Close relies on: fully
// confirmed money closes the check, money still awaiting a fiscal result
// yields the transient ErrFiscalPending, and a genuine shortfall keeps the
// pre-existing ErrInsufficientPayment behaviour.
func TestPaymentCoversTotal(t *testing.T) {
	tests := []struct {
		name    string
		paid    int64
		pending int64
		total   int64
		wantErr error
	}{
		{
			name:  "fully paid, nothing pending",
			paid:  10_000,
			total: 10_000,
		},
		{
			name:  "overpaid",
			paid:  12_000,
			total: 10_000,
		},
		{
			name:    "fully paid while another payment is still pending",
			paid:    10_000,
			pending: 5_000,
			total:   10_000,
		},
		{
			name:    "pending covers the remainder",
			paid:    4_000,
			pending: 6_000,
			total:   10_000,
			wantErr: ErrFiscalPending,
		},
		{
			name:    "pending alone covers the whole total",
			pending: 10_000,
			total:   10_000,
			wantErr: ErrFiscalPending,
		},
		{
			name:    "pending overshoots the remainder",
			paid:    4_000,
			pending: 9_000,
			total:   10_000,
			wantErr: ErrFiscalPending,
		},
		{
			name:    "pending exists but is still short",
			paid:    2_000,
			pending: 3_000,
			total:   10_000,
			wantErr: ErrInsufficientPayment,
		},
		{
			name:    "nothing pending and short",
			paid:    2_000,
			total:   10_000,
			wantErr: ErrInsufficientPayment,
		},
		{
			name:  "nothing paid at all",
			total: 10_000,

			wantErr: ErrInsufficientPayment,
		},
		{
			name:  "zero-total check closes with no payment",
			total: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := paymentCoversTotal(tt.paid, tt.pending, tt.total)
			if tt.wantErr == nil {
				assert.NoError(t, err)
				return
			}
			assert.ErrorIs(t, err, tt.wantErr)
		})
	}
}
