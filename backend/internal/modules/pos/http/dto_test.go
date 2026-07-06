package http

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"onlinemenu.tr/internal/modules/pos/domain"
)

// TestToCheckResponse_PaxAlwaysPresent_TotalOmittedByDefault documents the
// checkResponse contract added for pax/total (see checkResponse's doc
// comment): pax comes straight from domain.Check with no extra query, so
// toCheckResponse always sets it; total is deliberately left nil (and thus
// omitted from the JSON body, via `omitempty`) until a caller opts in by
// assigning resp.Total explicitly — which only listChecks and getCheck do.
func TestToCheckResponse_PaxAlwaysPresent_TotalOmittedByDefault(t *testing.T) {
	c := domain.Check{
		ID:         uuid.New(),
		TenantID:   uuid.New(),
		BranchID:   uuid.New(),
		TableLabel: "Masa 3",
		Pax:        4,
		Status:     domain.CheckStatusOpen,
		OpenedAt:   time.Now(),
	}

	resp := toCheckResponse(c)
	assert.Equal(t, 4, resp.Pax)
	assert.Nil(t, resp.Total, "total must be nil until a handler explicitly sets it (open/close/cancel never do)")

	body, err := json.Marshal(resp)
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(body, &decoded))
	assert.Contains(t, decoded, "pax")
	assert.NotContains(t, decoded, "total", "total must be omitted (omitempty), not emitted as 0/null")
}

// TestToCheckResponse_TotalSet_SerializesEvenWhenZero guards the pointer
// choice: a genuinely zero total (an open check with no orders yet) must
// still serialize as `"total":0`, not be dropped by omitempty — omitempty on
// a *int64 only omits a nil pointer, never a pointer to the zero value.
func TestToCheckResponse_TotalSet_SerializesEvenWhenZero(t *testing.T) {
	c := domain.Check{
		ID:     uuid.New(),
		Pax:    2,
		Status: domain.CheckStatusOpen,
	}

	resp := toCheckResponse(c)
	var zero int64
	resp.Total = &zero

	body, err := json.Marshal(resp)
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(body, &decoded))
	total, ok := decoded["total"]
	require.True(t, ok, "a genuinely-zero total must still be present in the JSON body")
	assert.Equal(t, float64(0), total)
}
