package http

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"onlinemenu.tr/internal/modules/payment/service"
)

// TestToFiscalStatusResponse_EmptyListsMarshalAsArrays: a nil Go slice
// marshals to null, and the POS client is written against a contract that
// guarantees []. An idle branch is the common case, so this is the response
// shape most stations see most of the time.
func TestToFiscalStatusResponse_EmptyListsMarshalAsArrays(t *testing.T) {
	branchID := uuid.New()

	body, err := json.Marshal(toFiscalStatusResponse(service.FiscalBranchStatus{
		BranchID: branchID,
		AsOf:     time.Date(2026, 7, 19, 10, 30, 0, 0, time.UTC),
	}))
	require.NoError(t, err)

	var decoded map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(body, &decoded))

	assert.JSONEq(t, `[]`, string(decoded["pending"]))
	assert.JSONEq(t, `[]`, string(decoded["recently_settled"]))
	assert.NotContains(t, string(body), "null")
	assert.Contains(t, string(body), `"as_of":"2026-07-19T10:30:00Z"`)
}

// TestToFiscalStatusResponse_WireShape pins every field name and format the
// POS client is being written against.
func TestToFiscalStatusResponse_WireShape(t *testing.T) {
	branchID := uuid.New()
	paymentID := uuid.New()
	checkID := uuid.New()
	settledPaymentID := uuid.New()
	reason := "E-1234: kagit yok"
	asOf := time.Date(2026, 7, 19, 10, 30, 13, 0, time.UTC)

	resp := toFiscalStatusResponse(service.FiscalBranchStatus{
		BranchID: branchID,
		AsOf:     asOf,
		Pending: []service.FiscalPendingItem{{
			PaymentID:    paymentID,
			CheckID:      &checkID,
			AmountTotal:  12500,
			RegisteredAt: asOf.Add(-13 * time.Second),
			AgeSeconds:   13,
		}},
		RecentlySettled: []service.FiscalSettledItem{{
			PaymentID:     settledPaymentID,
			CheckID:       nil,
			AmountTotal:   7350,
			Status:        "failed",
			FailureReason: &reason,
			SettledAt:     asOf.Add(-time.Minute),
		}},
	})

	body, err := json.Marshal(resp)
	require.NoError(t, err)
	out := string(body)

	assert.Contains(t, out, `"branch_id":"`+branchID.String()+`"`)
	assert.Contains(t, out, `"payment_id":"`+paymentID.String()+`"`)
	assert.Contains(t, out, `"check_id":"`+checkID.String()+`"`)
	assert.Contains(t, out, `"amount_total":12500`)
	assert.Contains(t, out, `"registered_at":"2026-07-19T10:30:00Z"`)
	assert.Contains(t, out, `"age_seconds":13`)
	assert.Contains(t, out, `"status":"failed"`)
	// Both entries carry amount_total in kuruş; the settled one lets the client
	// keep deducting a payment that has just left the pending list.
	assert.Contains(t, out, `"amount_total":7350`)
	require.Len(t, resp.RecentlySettled, 1)
	assert.Equal(t, int64(7350), resp.RecentlySettled[0].AmountTotal)
	// Raw device text passes through untranslated: the client owns the wording.
	assert.Contains(t, out, `"failure_reason":"E-1234: kagit yok"`)
	assert.Contains(t, out, `"settled_at":"2026-07-19T10:29:13Z"`)
	// A payment with no check (delivery order) still carries the key, as null.
	assert.True(t, strings.Contains(out, `"check_id":null`))
}

// TestToFiscalStatusResponse_TimestampsAreUTC: stations across a chain compare
// as_of against registered_at, so a mixed-offset response would make
// client-side arithmetic subtly wrong.
func TestToFiscalStatusResponse_TimestampsAreUTC(t *testing.T) {
	istanbul, err := time.LoadLocation("Europe/Istanbul")
	require.NoError(t, err)
	local := time.Date(2026, 7, 19, 13, 30, 0, 0, istanbul)

	resp := toFiscalStatusResponse(service.FiscalBranchStatus{
		BranchID: uuid.New(),
		AsOf:     local.UTC(),
		Pending:  []service.FiscalPendingItem{{PaymentID: uuid.New(), RegisteredAt: local}},
	})

	assert.True(t, strings.HasSuffix(resp.AsOf, "Z"))
	assert.Equal(t, "2026-07-19T10:30:00Z", resp.Pending[0].RegisteredAt)
}
