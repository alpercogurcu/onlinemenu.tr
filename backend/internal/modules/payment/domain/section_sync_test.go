package domain

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMockFiscalAdapterFetchSections keeps the dev/CI section-mapping flow
// honest: the mock must report sections that look like a real ÖKC's, with tax
// rates in permyriad (1000 = %10) rather than percent.
func TestMockFiscalAdapterFetchSections(t *testing.T) {
	t.Parallel()

	var adapter FiscalDeviceAdapter = MockFiscalAdapter{}
	syncer, ok := adapter.(SectionSyncer)
	require.True(t, ok, "the admin section-sync endpoint type-asserts this capability")

	sections, err := syncer.FetchSections(context.Background(), "ANY-SERIAL")
	require.NoError(t, err)
	require.Len(t, sections, 3)

	assert.Equal(t, []DeviceSection{
		{SectionNo: 1, Name: "KDV %1", TaxPermyriad: 100},
		{SectionNo: 2, Name: "KDV %10", TaxPermyriad: 1000},
		{SectionNo: 3, Name: "KDV %20", TaxPermyriad: 2000},
	}, sections)

	seen := make(map[int]struct{}, len(sections))
	for _, s := range sections {
		assert.Positive(t, s.SectionNo, "section numbers are 1-based on every device")
		assert.NotEmpty(t, s.Name)
		_, dup := seen[s.SectionNo]
		assert.False(t, dup, "duplicate section numbers would violate the storage unique index")
		seen[s.SectionNo] = struct{}{}
	}
}
