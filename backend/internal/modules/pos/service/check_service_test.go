package service

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"onlinemenu.tr/internal/modules/pos/domain"
)

// TestNormalizeServiceType pins Open's service-type contract at its single
// choke point: dine_in (explicit or defaulted from empty) changes nothing,
// takeaway/delivery reject a table_id and require customer contact fields,
// and a blank table_label is filled from customer_name so every existing
// table_label consumer (KDS, kitchen receipt) renders the customer's name.
func TestNormalizeServiceType(t *testing.T) {
	tableID := uuid.New()

	tests := []struct {
		name            string
		check           domain.Check
		wantErr         error
		wantServiceType domain.ServiceType
		wantLabel       string
	}{
		{
			name:            "empty defaults to dine_in, customer fields untouched",
			check:           domain.Check{TableLabel: "Masa 5"},
			wantServiceType: domain.ServiceTypeDineIn,
			wantLabel:       "Masa 5",
		},
		{
			name:            "explicit dine_in passes through",
			check:           domain.Check{ServiceType: domain.ServiceTypeDineIn, TableID: &tableID},
			wantServiceType: domain.ServiceTypeDineIn,
		},
		{
			name:    "unknown service_type rejected",
			check:   domain.Check{ServiceType: "drive_thru"},
			wantErr: ErrInvalidServiceType,
		},
		{
			name: "takeaway with table_id rejected",
			check: domain.Check{
				ServiceType:  domain.ServiceTypeTakeaway,
				TableID:      &tableID,
				CustomerName: "Alper Vural",
			},
			wantErr: ErrTableNotAllowed,
		},
		{
			name:    "takeaway without customer_name rejected",
			check:   domain.Check{ServiceType: domain.ServiceTypeTakeaway},
			wantErr: ErrCustomerNameRequired,
		},
		{
			name:    "takeaway with whitespace-only customer_name rejected",
			check:   domain.Check{ServiceType: domain.ServiceTypeTakeaway, CustomerName: "   "},
			wantErr: ErrCustomerNameRequired,
		},
		{
			name: "takeaway fills blank label from customer_name",
			check: domain.Check{
				ServiceType:  domain.ServiceTypeTakeaway,
				CustomerName: "Alper Vural",
			},
			wantServiceType: domain.ServiceTypeTakeaway,
			wantLabel:       "Alper Vural",
		},
		{
			name: "takeaway keeps an explicit label",
			check: domain.Check{
				ServiceType:  domain.ServiceTypeTakeaway,
				CustomerName: "Alper Vural",
				TableLabel:   "Gel Al 3",
			},
			wantServiceType: domain.ServiceTypeTakeaway,
			wantLabel:       "Gel Al 3",
		},
		{
			name: "delivery without phone rejected",
			check: domain.Check{
				ServiceType:  domain.ServiceTypeDelivery,
				CustomerName: "Alper Vural",
			},
			wantErr: ErrCustomerPhoneRequired,
		},
		{
			name: "delivery with name and phone accepted",
			check: domain.Check{
				ServiceType:   domain.ServiceTypeDelivery,
				CustomerName:  "Alper Vural",
				CustomerPhone: "05321112233",
			},
			wantServiceType: domain.ServiceTypeDelivery,
			wantLabel:       "Alper Vural",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := tt.check
			err := normalizeServiceType(&c)
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantServiceType, c.ServiceType)
			assert.Equal(t, tt.wantLabel, c.TableLabel)
		})
	}
}

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
