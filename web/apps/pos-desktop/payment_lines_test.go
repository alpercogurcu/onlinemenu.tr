package main

import (
	"context"
	"errors"
	"testing"

	"onlinemenu.tr/pos-desktop/internal/apiclient"
)

type fakeMeta map[string]apiclient.Product

func (f fakeMeta) productMeta(_ context.Context, id string) (apiclient.Product, error) {
	p, ok := f[id]
	if !ok {
		return apiclient.Product{}, errors.New("not found")
	}
	return p, nil
}

func TestBuildFiscalLines_AddsTaxCategoryAndUnitFromTheCatalog(t *testing.T) {
	meta := fakeMeta{
		"p1": {ID: "p1", CategoryID: "cat-food", TaxRateBPS: 1000, Unit: "adet"},
		"p2": {ID: "p2", CategoryID: "cat-drink", TaxRateBPS: 2000, Unit: "lt"},
	}
	lines, err := buildFiscalLines(context.Background(), meta, []PaymentLineDTO{
		{ProductID: "p1", Name: "Lahmacun", UnitPriceMinor: 6500, QuantityMilli: 2000},
		{ProductID: "p2", Name: "Ayran", UnitPriceMinor: 1500, QuantityMilli: 1000},
	})
	if err != nil {
		t.Fatalf("buildFiscalLines: %v", err)
	}
	want := []apiclient.FiscalLine{
		{Name: "Lahmacun", UnitPriceMinor: 6500, QuantityMilli: 2000, TaxRatePermyriad: 1000, CategoryID: "cat-food", Unit: "C62"},
		{Name: "Ayran", UnitPriceMinor: 1500, QuantityMilli: 1000, TaxRatePermyriad: 2000, CategoryID: "cat-drink", Unit: "LTR"},
	}
	if len(lines) != len(want) {
		t.Fatalf("got %d lines, want %d", len(lines), len(want))
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Errorf("line %d = %+v, want %+v", i, lines[i], want[i])
		}
	}
}

func TestBuildFiscalLines_NoLinesMeansNoBasket(t *testing.T) {
	lines, err := buildFiscalLines(context.Background(), fakeMeta{}, nil)
	if err != nil || lines != nil {
		t.Fatalf("lines=%v err=%v, want nil, nil", lines, err)
	}
}

func TestBuildFiscalLines_RefusesToGuessWhenTheCatalogCannotBeRead(t *testing.T) {
	_, err := buildFiscalLines(context.Background(), fakeMeta{}, []PaymentLineDTO{{ProductID: "gone", Name: "X", UnitPriceMinor: 100, QuantityMilli: 1000}})
	if err == nil {
		t.Fatal("expected an error: a real device rejects a line whose tax and section are made up, and the payment must not be taken first")
	}
}

func TestFiscalUnitCode(t *testing.T) {
	tests := []struct{ in, want string }{
		{"adet", "C62"},
		{"Adet", "C62"},
		{"porsiyon", "C62"},
		{"kg", "KGM"},
		{"lt", "LTR"},
		{"", ""},
		{"C62", "C62"},
		{"kutu", "C62"},
	}
	for _, tt := range tests {
		if got := fiscalUnitCode(tt.in); got != tt.want {
			t.Errorf("fiscalUnitCode(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestPaymentMethodFor(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"cash", "cash", false},
		{"card", "terminal", false},
		{"", "", true},
		{"bitcoin", "", true},
	}
	for _, tt := range tests {
		got, err := paymentMethodFor(tt.in)
		if (err != nil) != tt.wantErr || got != tt.want {
			t.Errorf("paymentMethodFor(%q) = %q, %v; want %q, err=%v", tt.in, got, err, tt.want, tt.wantErr)
		}
	}
}

func TestBuildFiscalLines_UncategorizedProductGoesWithoutACategory(t *testing.T) {
	// Known gap, pinned so it is not mistaken for a guarantee: a product without
	// a category has no device section to map to, which the mock adapter does not
	// care about but a real ÖKC adapter rejects. It is passed through rather than
	// blocking the sale on installations that run the mock adapter.
	meta := fakeMeta{"p1": {ID: "p1", CategoryID: "", TaxRateBPS: 1000, Unit: "adet"}}
	lines, err := buildFiscalLines(context.Background(), meta, []PaymentLineDTO{{ProductID: "p1", Name: "Çay", UnitPriceMinor: 1500, QuantityMilli: 1000}})
	if err != nil || len(lines) != 1 || lines[0].CategoryID != "" {
		t.Fatalf("lines=%+v err=%v", lines, err)
	}
}

func TestToCheckDTO_CarriesTheRunningTotalForTheCheckRail(t *testing.T) {
	total := int64(24000)
	dto := toCheckDTO(apiclient.Check{ID: "c1", Status: "open", Total: &total})
	if dto.Total == nil || *dto.Total != 24000 {
		t.Fatalf("Total = %v, want 24000 — the open-checks rail shows it (bulgu #10)", dto.Total)
	}
	if toCheckDTO(apiclient.Check{ID: "c2"}).Total != nil {
		t.Fatal("a check the backend sent no total for must stay nil, not show ₺0")
	}
}
