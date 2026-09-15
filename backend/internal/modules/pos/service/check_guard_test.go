package service

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"onlinemenu.tr/internal/modules/pos/domain"
	pub "onlinemenu.tr/internal/modules/pos/public"
)

// TestAssertCheckWritable covers the single predicate both write paths that
// attach money or food to a check rely on: pos's own OrderService.Place and
// (through pub.CheckWriteGuard) payment's RegisterSale. The production bug it
// locks down is that both used to write against a closed or cancelled check.
func TestAssertCheckWritable(t *testing.T) {
	branch := uuid.MustParse("cccccccc-0000-0000-0000-000000000001")
	otherBranch := uuid.MustParse("cccccccc-0000-0000-0000-000000000002")

	tests := []struct {
		name     string
		check    domain.Check
		branchID uuid.UUID
		wantErr  error
	}{
		{
			name:     "open check in the same branch",
			check:    domain.Check{BranchID: branch, Status: domain.CheckStatusOpen},
			branchID: branch,
		},
		{
			name:     "open check, branch not supplied",
			check:    domain.Check{BranchID: branch, Status: domain.CheckStatusOpen},
			branchID: uuid.Nil,
		},
		{
			name:     "closed check",
			check:    domain.Check{BranchID: branch, Status: domain.CheckStatusClosed},
			branchID: branch,
			wantErr:  pub.ErrCheckNotOpen,
		},
		{
			name:     "cancelled check",
			check:    domain.Check{BranchID: branch, Status: domain.CheckStatusCancelled},
			branchID: branch,
			wantErr:  pub.ErrCheckNotOpen,
		},
		{
			name:     "open check in another branch",
			check:    domain.Check{BranchID: otherBranch, Status: domain.CheckStatusOpen},
			branchID: branch,
			wantErr:  pub.ErrCheckBranchMismatch,
		},
		{
			// Branch is decided before status so a caller probing another
			// branch's check never learns whether it is still open.
			name:     "closed check in another branch reports the branch",
			check:    domain.Check{BranchID: otherBranch, Status: domain.CheckStatusClosed},
			branchID: branch,
			wantErr:  pub.ErrCheckBranchMismatch,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := assertCheckWritable(tt.check, tt.branchID)
			if tt.wantErr == nil {
				require.NoError(t, err)
				return
			}
			assert.ErrorIs(t, err, tt.wantErr)
		})
	}
}
