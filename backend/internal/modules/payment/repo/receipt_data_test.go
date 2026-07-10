package repo

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"onlinemenu.tr/internal/modules/payment/domain"
)

// TestMergeReceiptData pins the storage contract for ZNo/VendorRef: fiscal_receipts
// deliberately has no columns for them, so they are folded into receipt_data.
func TestMergeReceiptData(t *testing.T) {
	tests := []struct {
		name    string
		receipt domain.FiscalReceipt
		want    map[string]any
	}{
		{
			name:    "empty receipt yields an empty document, never a nil map",
			receipt: domain.FiscalReceipt{},
			want:    map[string]any{},
		},
		{
			name:    "z_no and vendor_ref are folded in",
			receipt: domain.FiscalReceipt{ZNo: "0042", VendorRef: "tx-1"},
			want:    map[string]any{"z_no": "0042", "vendor_ref": "tx-1"},
		},
		{
			name: "empty struct fields are omitted rather than written as blanks",
			receipt: domain.FiscalReceipt{
				ZNo:         "",
				VendorRef:   "",
				ReceiptData: map[string]any{"raw": "payload"},
			},
			want: map[string]any{"raw": "payload"},
		},
		{
			name: "vendor payload keys survive alongside the folded fields",
			receipt: domain.FiscalReceipt{
				ZNo:         "0042",
				VendorRef:   "tx-1",
				ReceiptData: map[string]any{"raw": "payload"},
			},
			want: map[string]any{"z_no": "0042", "vendor_ref": "tx-1", "raw": "payload"},
		},
		{
			name: "an explicit receipt_data entry wins over the struct field",
			receipt: domain.FiscalReceipt{
				ZNo:         "struct-value",
				ReceiptData: map[string]any{"z_no": "explicit-value"},
			},
			want: map[string]any{"z_no": "explicit-value"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := mergeReceiptData(tc.receipt)
			assert.NotNil(t, got)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestMergeReceiptData_DoesNotMutateInput guards against the merge writing back
// into the caller's map, which would leak fields across receipts.
func TestMergeReceiptData_DoesNotMutateInput(t *testing.T) {
	original := map[string]any{"raw": "payload"}
	rec := domain.FiscalReceipt{ZNo: "0042", VendorRef: "tx-1", ReceiptData: original}

	mergeReceiptData(rec)

	assert.Equal(t, map[string]any{"raw": "payload"}, original)
}
