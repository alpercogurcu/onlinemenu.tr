package http

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"onlinemenu.tr/internal/modules/payment/service"
)

// TestToCheckSettlementResponse_EmptyCompletedMarshalsAsArray: a nil Go slice
// marshals to null. A client that reads null as "unknown" falls back to the
// full check balance — which is the double-charge this endpoint exists to
// prevent, reintroduced through the encoder. An unpaid check is also the most
// common case, so this is the response shape stations see most often.
func TestToCheckSettlementResponse_EmptyCompletedMarshalsAsArray(t *testing.T) {
	checkID := uuid.New()

	body, err := json.Marshal(toCheckSettlementResponse(service.CheckSettlement{
		CheckID: checkID,
		AsOf:    time.Date(2026, 7, 19, 10, 30, 0, 0, time.UTC),
	}))
	require.NoError(t, err)

	var decoded map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(body, &decoded))

	assert.JSONEq(t, `[]`, string(decoded["completed"]))
	assert.NotContains(t, string(body), "null")
	assert.JSONEq(t, `0`, string(decoded["pending_total"]),
		"an absent pending total must serialize as 0, never omitted")
}

// TestToCheckSettlementResponse_WireShape pins the exact contract the POS
// client is written against — every field name, the RFC3339 UTC as_of, and
// kuruş integers.
func TestToCheckSettlementResponse_WireShape(t *testing.T) {
	checkID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	paymentA := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	paymentB := uuid.MustParse("33333333-3333-3333-3333-333333333333")

	body, err := json.Marshal(toCheckSettlementResponse(service.CheckSettlement{
		CheckID: checkID,
		// Deliberately non-UTC: the handler must normalize before formatting,
		// or two stations in different zones would compare incomparable stamps.
		AsOf: time.Date(2026, 7, 19, 13, 30, 0, 0, time.FixedZone("+03", 3*60*60)),
		Completed: []service.CheckSettledPayment{
			{PaymentID: paymentA, AmountTotal: 12500},
			{PaymentID: paymentB, AmountTotal: 3000},
		},
		PendingTotal: 2500,
	}))
	require.NoError(t, err)

	assert.JSONEq(t, `{
		"check_id": "11111111-1111-1111-1111-111111111111",
		"as_of": "2026-07-19T10:30:00Z",
		"completed": [
			{"payment_id": "22222222-2222-2222-2222-222222222222", "amount_total": 12500},
			{"payment_id": "33333333-3333-3333-3333-333333333333", "amount_total": 3000}
		],
		"pending_total": 2500
	}`, string(body))
}

// TestCheckSettledPayment_ExposesOnlyIDAndAmount is a projection guard, not a
// formatting test (ADR-AUTH-001 layer 4).
//
// This DTO is visible to cashiers, who hold no payment.payment.read. Method,
// timestamps and fiscal receipt references are reconciliation data and must not
// ride along. JSONEq above would keep passing if someone added a field to the
// struct and updated the fixture; this asserts on the encoded key set directly,
// so a widening shows up as a failure naming the offending field.
func TestCheckSettledPayment_ExposesOnlyIDAndAmount(t *testing.T) {
	body, err := json.Marshal(checkSettledPayment{PaymentID: uuid.New(), AmountTotal: 100})
	require.NoError(t, err)

	var decoded map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(body, &decoded))

	keys := make([]string, 0, len(decoded))
	for k := range decoded {
		keys = append(keys, k)
	}
	assert.ElementsMatch(t, []string{"payment_id", "amount_total"}, keys,
		"cashier-visible projection: adding a field here widens counter staff's view of money with no permission change")
}
