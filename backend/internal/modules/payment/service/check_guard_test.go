package service

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	pub "onlinemenu.tr/internal/modules/payment/public"
	pospub "onlinemenu.tr/internal/modules/pos/public"
)

// TestTranslateCheckGuardErr pins the module boundary: pos's sentinels are
// re-expressed as payment's own, so payment_http never has to import
// pos_public to answer a caller. Anything that is not a verdict (a dead
// connection, a context deadline) must translate to nil so RegisterSale can
// tell "the check refuses this sale" from "we could not ask".
func TestTranslateCheckGuardErr(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want error
	}{
		{
			name: "check not open",
			err:  fmt.Errorf("pos/service/check-read: assert writable: %w", pospub.ErrCheckNotOpen),
			want: pub.ErrCheckNotOpen,
		},
		{
			name: "branch mismatch",
			err:  fmt.Errorf("pos/service/check-read: assert writable: %w", pospub.ErrCheckBranchMismatch),
			want: pub.ErrCheckBranchMismatch,
		},
		{
			name: "unknown or cross-tenant check id",
			err:  fmt.Errorf("pos/service/check-read: assert writable: %w", pospub.ErrNotFound),
			want: pub.ErrCheckNotFound,
		},
		{
			name: "infrastructure failure is not a verdict",
			err:  errors.New("pos/service/check-read: assert writable: conn closed"),
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := translateCheckGuardErr(tt.err)
			if tt.want == nil {
				assert.NoError(t, got)
				return
			}
			assert.ErrorIs(t, got, tt.want)
		})
	}
}
