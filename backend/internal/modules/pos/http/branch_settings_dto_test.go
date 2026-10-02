package http

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"onlinemenu.tr/internal/modules/pos/domain"
)

// TestBranchSettingsResponse_WireShape pins the GET/PUT body, including the
// rounding policy the POS payment screen and the admin card read.
func TestBranchSettingsResponse_WireShape(t *testing.T) {
	branchID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	s := domain.DefaultBranchSettings(uuid.New(), branchID)
	s.Rounding.CashEnabled = true

	body, err := json.Marshal(toBranchSettingsResponse(s))
	require.NoError(t, err)
	assert.JSONEq(t, `{
		"branch_id": "11111111-1111-1111-1111-111111111111",
		"waiter_category_layout": "top",
		"order_flow": "full",
		"rounding_cash_enabled": true,
		"rounding_card_enabled": false,
		"rounding_step_minor": 500,
		"rounding_max_per_check_minor": 1000
	}`, string(body))
}
