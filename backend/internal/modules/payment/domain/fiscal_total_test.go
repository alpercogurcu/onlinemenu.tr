package domain_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"onlinemenu.tr/internal/modules/payment/domain"
)

func TestFiscalSale_ValidateTotal(t *testing.T) {
	line := func(price, qtyMilli int64) domain.FiscalLine {
		return domain.FiscalLine{Name: "x", UnitPriceMinor: price, QuantityMilli: qtyMilli}
	}
	adjust := func(kind domain.FiscalAdjustKind, mode domain.FiscalAdjustMode, v int64) *domain.FiscalAdjust {
		return &domain.FiscalAdjust{Description: "a", Kind: kind, Mode: mode, Value: v}
	}

	tests := []struct {
		name    string
		sale    domain.FiscalSale
		wantErr bool
	}{
		{name: "single line", sale: domain.FiscalSale{TotalMinor: 4500, Lines: []domain.FiscalLine{line(4500, 1000)}}},
		{name: "quantity multiplies", sale: domain.FiscalSale{TotalMinor: 13000, Lines: []domain.FiscalLine{line(6500, 2000)}}},
		{name: "fractional quantity rounds half up", sale: domain.FiscalSale{TotalMinor: 1667, Lines: []domain.FiscalLine{line(3333, 500)}}},
		{name: "several lines", sale: domain.FiscalSale{TotalMinor: 7000, Lines: []domain.FiscalLine{line(4500, 1000), line(2500, 1000)}}},
		{name: "amount discount", sale: domain.FiscalSale{TotalMinor: 43500, Lines: []domain.FiscalLine{line(43750, 1000)}, Discount: adjust(domain.FiscalAdjustDiscount, domain.FiscalAdjustAmount, 250)}},
		{name: "percent discount", sale: domain.FiscalSale{TotalMinor: 9000, Lines: []domain.FiscalLine{line(10000, 1000)}, Discount: adjust(domain.FiscalAdjustDiscount, domain.FiscalAdjustPercent, 1000)}},
		{name: "amount surcharge", sale: domain.FiscalSale{TotalMinor: 11500, Lines: []domain.FiscalLine{line(10000, 1000)}, Discount: adjust(domain.FiscalAdjustSurcharge, domain.FiscalAdjustAmount, 1500)}},
		{name: "total off by one kuruş", sale: domain.FiscalSale{TotalMinor: 4501, Lines: []domain.FiscalLine{line(4500, 1000)}}, wantErr: true},
		{name: "discount missing", sale: domain.FiscalSale{TotalMinor: 43500, Lines: []domain.FiscalLine{line(43750, 1000)}}, wantErr: true},
		{name: "no lines but a total", sale: domain.FiscalSale{TotalMinor: 100}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.sale.ValidateTotal()
			if tt.wantErr {
				assert.ErrorIs(t, err, domain.ErrFiscalTotalMismatch)
				return
			}
			assert.NoError(t, err)
		})
	}
}

// TestMockFiscalAdapter_ValidatesTotal: the mock refuses a basket a real
// device would refuse, so the mismatch surfaces in dev and CI too.
func TestMockFiscalAdapter_ValidatesTotal(t *testing.T) {
	sale := domain.FiscalSale{
		SubmissionID: uuid.New(), PaymentID: uuid.New(), TotalMinor: 43500,
		Lines: []domain.FiscalLine{{Name: "Burger", UnitPriceMinor: 43750, QuantityMilli: 1000}},
	}

	_, err := domain.MockFiscalAdapter{}.SubmitSale(context.Background(), sale)
	require.ErrorIs(t, err, domain.ErrFiscalTotalMismatch)

	sale.Discount = &domain.FiscalAdjust{Description: "Yuvarlama", Kind: domain.FiscalAdjustDiscount, Mode: domain.FiscalAdjustAmount, Value: 250}
	res, err := domain.MockFiscalAdapter{}.SubmitSale(context.Background(), sale)
	require.NoError(t, err)
	assert.Equal(t, domain.FiscalSubmissionCompleted, res.Status)
}
