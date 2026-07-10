package tokenx

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"onlinemenu.tr/internal/modules/payment/domain"
)

var errSectionUnmapped = errors.New("category is not mapped to a device section")

func testSale() domain.FiscalSale {
	return domain.FiscalSale{
		SubmissionID: uuid.MustParse("6ba7b810-9dad-11d1-80b4-00c04fd430c8"),
		TenantID:     uuid.New(),
		BranchID:     uuid.New(),
		PaymentID:    uuid.New(),
		Currency:     "TRY",
		TotalMinor:   15000,
		Lines: []domain.FiscalLine{{
			Name:             "Adana Kebap",
			UnitPriceMinor:   15000, // 150.00 TRY
			QuantityMilli:    1000,  // 1 adet
			TaxRatePermyriad: 1000,  // %10
			CategoryID:       uuid.New(),
			Unit:             "C62",
		}},
		Payments: []domain.FiscalPayment{{Method: domain.PaymentMethodCash, AmountMinor: 15000}},
		Meta:     domain.FiscalMeta{TableLabel: "Masa 5", WaiterName: "Ayşe", CheckNumber: 42},
	}
}

func TestBuildBasket(t *testing.T) {
	t.Parallel()

	t.Run("maps units, quantities and tax rates without conversion", func(t *testing.T) {
		t.Parallel()
		sale := testSale()
		sale.Lines = append(sale.Lines, domain.FiscalLine{
			Name:             "Ayran",
			UnitPriceMinor:   2500,
			QuantityMilli:    2500, // 2.5 units
			TaxRatePermyriad: 1000,
			CategoryID:       uuid.New(),
			Unit:             "", // falls back to C62
		})

		basket, err := buildBasket(context.Background(), sale, staticSections(3, 1000), false)
		require.NoError(t, err)

		assert.Equal(t, sale.SubmissionID.String(), basket.BasketID)
		assert.False(t, basket.IsVoid)
		assert.False(t, basket.CreateInvoice)
		assert.Equal(t, documentTypeReceipt, basket.DocumentType)
		assert.Equal(t, "Masa 5", basket.Title)
		assert.Equal(t, "Ayşe", basket.Filter)
		assert.Equal(t, 42, basket.CheckNumber)

		require.Len(t, basket.Items, 2)
		assert.Equal(t, BasketItem{
			Name: "Adana Kebap", Price: 15000, SectionNo: 3, TaxPercent: 1000, Quantity: 1000, Unit: "C62",
		}, basket.Items[0])
		assert.Equal(t, int64(2500), basket.Items[1].Quantity)
		assert.Equal(t, defaultUnitCode, basket.Items[1].Unit, "empty unit must default to C62")

		require.Len(t, basket.PaymentItems, 1)
		assert.Equal(t, PaymentItem{Amount: 15000, Type: paymentTypeCash}, basket.PaymentItems[0])
		assert.Nil(t, basket.Adjust)
		assert.Nil(t, basket.CustomerInfo)
	})

	t.Run("rejects a line whose tax rate differs from its device section", func(t *testing.T) {
		t.Parallel()
		sale := testSale() // line is 1000 permyriad (%10)
		_, err := buildBasket(context.Background(), sale, staticSections(3, 2000), false)
		require.ErrorIs(t, err, ErrTaxMismatch)
	})

	t.Run("propagates an unmapped category instead of guessing a section", func(t *testing.T) {
		t.Parallel()
		resolver := sectionsFunc(func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (int, int, error) {
			return 0, 0, errSectionUnmapped
		})
		_, err := buildBasket(context.Background(), testSale(), resolver, false)
		require.ErrorIs(t, err, errSectionUnmapped)
	})

	t.Run("rejects an empty basket", func(t *testing.T) {
		t.Parallel()
		sale := testSale()
		sale.Lines = nil
		_, err := buildBasket(context.Background(), sale, staticSections(1, 1000), false)
		require.ErrorIs(t, err, ErrNoLines)
	})

	t.Run("rejects an unknown payment method", func(t *testing.T) {
		t.Parallel()
		sale := testSale()
		sale.Payments = []domain.FiscalPayment{{Method: domain.PaymentMethod("crypto"), AmountMinor: 1}}
		_, err := buildBasket(context.Background(), sale, staticSections(1, 1000), false)
		require.ErrorIs(t, err, ErrUnknownPaymentMethod)
	})

	t.Run("rejects a foreign-currency sale instead of registering it as TRY", func(t *testing.T) {
		t.Parallel()
		sale := testSale()
		sale.Currency = "USD"
		_, err := buildBasket(context.Background(), sale, staticSections(1, 1000), false)
		require.ErrorIs(t, err, ErrUnsupportedCurrency)
	})

	t.Run("accepts TRY in any case and an unset currency", func(t *testing.T) {
		t.Parallel()
		for _, currency := range []string{"TRY", "try", ""} {
			sale := testSale()
			sale.Currency = currency
			_, err := buildBasket(context.Background(), sale, staticSections(1, 1000), false)
			require.NoError(t, err, "currency %q must be accepted", currency)
		}
	})

	t.Run("passes the section tax rate through to every item", func(t *testing.T) {
		t.Parallel()
		sale := testSale()
		sale.Lines[0].TaxRatePermyriad = 100 // %1
		basket, err := buildBasket(context.Background(), sale, staticSections(9, 100), false)
		require.NoError(t, err)
		assert.Equal(t, 100, basket.Items[0].TaxPercent)
		assert.Equal(t, 9, basket.Items[0].SectionNo)
	})
}

func TestBuildBasketAdjust(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		adjust domain.FiscalAdjust
		want   Adjust
	}{
		{
			name:   "amount discount",
			adjust: domain.FiscalAdjust{Description: "Kupon", Kind: domain.FiscalAdjustDiscount, Mode: domain.FiscalAdjustAmount, Value: 500},
			want:   Adjust{Description: "Kupon", DiscountOrSurcharge: 0, Type: 0, Value: 500},
		},
		{
			name:   "percent discount",
			adjust: domain.FiscalAdjust{Description: "%10", Kind: domain.FiscalAdjustDiscount, Mode: domain.FiscalAdjustPercent, Value: 1000},
			want:   Adjust{Description: "%10", DiscountOrSurcharge: 0, Type: 1, Value: 1000},
		},
		{
			name:   "amount surcharge",
			adjust: domain.FiscalAdjust{Description: "Servis", Kind: domain.FiscalAdjustSurcharge, Mode: domain.FiscalAdjustAmount, Value: 1500},
			want:   Adjust{Description: "Servis", DiscountOrSurcharge: 1, Type: 0, Value: 1500},
		},
		{
			name:   "percent surcharge",
			adjust: domain.FiscalAdjust{Description: "Servis", Kind: domain.FiscalAdjustSurcharge, Mode: domain.FiscalAdjustPercent, Value: 500},
			want:   Adjust{Description: "Servis", DiscountOrSurcharge: 1, Type: 1, Value: 500},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sale := testSale()
			sale.Discount = &tc.adjust
			basket, err := buildBasket(context.Background(), sale, staticSections(1, 1000), false)
			require.NoError(t, err)
			require.NotNil(t, basket.Adjust)
			assert.Equal(t, tc.want, *basket.Adjust)
		})
	}
}

func TestBuildBasketCustomerInfo(t *testing.T) {
	t.Parallel()
	sale := testSale()
	sale.Customer = &domain.FiscalCustomer{
		Name: "Acme A.Ş.", TaxID: "1234567890", TaxOffice: "Kadıköy",
		Email: "fatura@acme.tr", Telephone: "+905551112233", Address: "Bağdat Cad. 1",
	}

	basket, err := buildBasket(context.Background(), sale, staticSections(1, 1000), false)
	require.NoError(t, err)
	require.NotNil(t, basket.CustomerInfo)
	assert.Equal(t, &CustomerInfo{
		Name: "Acme A.Ş.", TaxID: "1234567890", TaxScheme: "Kadıköy",
		Email: "fatura@acme.tr", Telephone: "+905551112233", Street: "Bağdat Cad. 1",
	}, basket.CustomerInfo)
}

func TestBuildVoidBasket(t *testing.T) {
	t.Parallel()
	ref := domain.FiscalSubmissionRef{SubmissionID: uuid.New(), TenantID: uuid.New(), BranchID: uuid.New()}
	basket := buildVoidBasket(ref)
	assert.Equal(t, ref.SubmissionID.String(), basket.BasketID)
	assert.True(t, basket.IsVoid)
	assert.Empty(t, basket.Items)
}

func TestPaymentTypeMapping(t *testing.T) {
	t.Parallel()

	tests := []struct {
		method domain.PaymentMethod
		code   int
	}{
		{domain.PaymentMethodCash, 1},
		{domain.PaymentMethodTerminal, 3},
		{domain.PaymentMethodMealCard, 7},
		{domain.PaymentMethodNoCharge, 8},
		{domain.PaymentMethodComp, 9},
		{domain.PaymentMethodOpenAccount, 17},
	}

	for _, tc := range tests {
		t.Run(string(tc.method), func(t *testing.T) {
			t.Parallel()
			code, err := paymentTypeOf(tc.method)
			require.NoError(t, err)
			assert.Equal(t, tc.code, code)
			assert.Equal(t, tc.method, methodOf(tc.code), "reverse mapping must round-trip")
		})
	}

	t.Run("every domain method has a vendor code", func(t *testing.T) {
		t.Parallel()
		for _, m := range []domain.PaymentMethod{
			domain.PaymentMethodCash, domain.PaymentMethodTerminal, domain.PaymentMethodMealCard,
			domain.PaymentMethodComp, domain.PaymentMethodNoCharge, domain.PaymentMethodOpenAccount,
		} {
			_, err := paymentTypeOf(m)
			require.NoError(t, err, "method %q must map", m)
		}
	})

	t.Run("unknown vendor code yields an empty method", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, domain.PaymentMethod(""), methodOf(4))
	})

	t.Run("unknown method is rejected", func(t *testing.T) {
		t.Parallel()
		_, err := paymentTypeOf(domain.PaymentMethod("bitcoin"))
		require.ErrorIs(t, err, ErrUnknownPaymentMethod)
	})
}
