package service

import "testing"

func TestBuildItemsTaxInclusive(t *testing.T) {
	tests := []struct {
		name      string
		qty       int32
		unit      int64
		bps       int32
		wantNet   int64
		wantTax   int64
		wantGross int64
	}{
		{"kdv %10 tam bolunur", 1, 11000, 1000, 10000, 1000, 11000},
		{"kdv %20 tam bolunur", 2, 12000, 2000, 20000, 4000, 24000},
		{"kdv %1", 1, 10100, 100, 10000, 100, 10100},
		{"kdv %10 yuvarlama asagi", 1, 1001, 1000, 910, 91, 1001},
		{"kdv %10 yuvarlama yukari (yari)", 1, 105, 1000, 95, 10, 105},
		{"kdv %20 yuvarlama", 3, 999, 2000, 2497, 500, 2997},
		{"kdv %0", 2, 500, 0, 1000, 0, 1000},
		{"tek kurus", 1, 1, 1000, 1, 0, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			items := buildItems([]InvoiceItemRequest{{ProductName: "x", Quantity: tc.qty, UnitPriceAmount: tc.unit, TaxRateBPS: tc.bps}})
			got := items[0]
			if got.LineTotal != tc.wantNet || got.TaxAmount != tc.wantTax {
				t.Fatalf("net/tax = %d/%d, want %d/%d", got.LineTotal, got.TaxAmount, tc.wantNet, tc.wantTax)
			}
			if got.LineTotal+got.TaxAmount != tc.wantGross {
				t.Fatalf("net+tax = %d, want gross %d", got.LineTotal+got.TaxAmount, tc.wantGross)
			}
			totals := calculateTotals(items)
			if totals.amountTotal != tc.wantGross {
				t.Fatalf("amountTotal = %d, want %d", totals.amountTotal, tc.wantGross)
			}
			if totals.amountExcludingTax+totals.taxAmount != totals.amountTotal {
				t.Fatalf("totals not consistent: %+v", totals)
			}
		})
	}
}

func TestCalculateTotalsSumsToGross(t *testing.T) {
	items := buildItems([]InvoiceItemRequest{
		{ProductName: "a", Quantity: 1, UnitPriceAmount: 1001, TaxRateBPS: 1000},
		{ProductName: "b", Quantity: 2, UnitPriceAmount: 999, TaxRateBPS: 2000},
		{ProductName: "c", Quantity: 1, UnitPriceAmount: 10100, TaxRateBPS: 100},
	})
	totals := calculateTotals(items)
	if want := int64(1001 + 1998 + 10100); totals.amountTotal != want {
		t.Fatalf("amountTotal = %d, want %d", totals.amountTotal, want)
	}
}
