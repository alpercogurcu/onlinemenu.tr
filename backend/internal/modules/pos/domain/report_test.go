package domain_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"onlinemenu.tr/internal/modules/pos/domain"
)

// TestNewTaxLine exercises the tax-inclusive split formula without a
// database: ReportRepo.SalesSummary's byTaxRate delegates to this for every
// row it scans, so this table is the one place the arithmetic itself is
// pinned down.
func TestNewTaxLine(t *testing.T) {
	tests := []struct {
		name     string
		rateBPS  int
		gross    int64
		wantBase int64
		wantTax  int64
	}{
		{name: "1000bps 23000 gross", rateBPS: 1000, gross: 23000, wantBase: 20909, wantTax: 2091},
		{name: "2000bps 5000 gross", rateBPS: 2000, gross: 5000, wantBase: 4167, wantTax: 833},
		{name: "zero gross", rateBPS: 1000, gross: 0, wantBase: 0, wantTax: 0},
		{name: "zero bps", rateBPS: 0, gross: 10000, wantBase: 10000, wantTax: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			line := domain.NewTaxLine(tt.rateBPS, tt.gross)
			assert.Equal(t, tt.rateBPS, line.RateBPS)
			assert.Equal(t, tt.gross, line.Gross)
			assert.Equal(t, tt.wantBase, line.Base)
			assert.Equal(t, tt.wantTax, line.Tax)
			assert.Equal(t, tt.gross, line.Base+line.Tax, "base+tax must reconstitute gross")
		})
	}
}
