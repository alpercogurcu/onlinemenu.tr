package tokenx

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"onlinemenu.tr/internal/modules/payment/domain"
)

const testBasketID = "6ba7b810-9dad-11d1-80b4-00c04fd430c8"

func completedPayload(status int, extra string) []byte {
	return fmt.Appendf(nil, `{
		"terminalId": "AV0000000658",
		"clientId": "acme-client",
		"operation": "BASKET_COMPLETED",
		"operationDate": "2026-07-10T15:04:05Z",
		"data": {
			"basketID": %q,
			"documentType": 0,
			"invoiceID": "",
			"message": "islem basarisiz",
			"paymentCount": 1,
			"paymentItems": [
				{"amount": 15000, "BatchNo": "12", "currencyId": 949, "description": "NAKIT",
				 "operatorId": 0, "status": 0, "TxnNo": "77", "type": 1}
			],
			"receiptNo": 1234,
			"status": %d,
			"UUID": "e6f1c1de-0000-4000-8000-abcdefabcdef",
			"zNo": 56
			%s
		}
	}`, testBasketID, status, extra)
}

func TestParseWebhookStatuses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		status            int
		wantStatus        domain.FiscalSubmissionStatus
		wantFailureReason string
	}{
		{name: "completed", status: 0, wantStatus: domain.FiscalSubmissionCompleted},
		{name: "failed", status: -1, wantStatus: domain.FiscalSubmissionFailed, wantFailureReason: "islem basarisiz"},
		{name: "voided", status: 99, wantStatus: domain.FiscalSubmissionVoided},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			payload := completedPayload(tc.status, "")

			parseStart := time.Now().UTC()
			res, err := ParseWebhook(payload)
			require.NoError(t, err)

			assert.Equal(t, uuid.MustParse(testBasketID), res.SubmissionID)
			assert.Equal(t, tc.wantStatus, res.Status)
			assert.Equal(t, tc.wantFailureReason, res.FailureReason,
				"only a failed registration carries a failure reason")
			assert.Equal(t, DeviceType, res.DeviceType)
			assert.Equal(t, "1234", res.ReceiptNo, "receiptNo int must be stringified")
			assert.Equal(t, "56", res.ZNo, "zNo int must be stringified")
			assert.Equal(t, "e6f1c1de-0000-4000-8000-abcdefabcdef", res.VendorRef)
			// Two clocks, kept apart on purpose: operationDate is the device's
			// own time and lands in DeviceOperationAt (the receipt's legal
			// stamp), while CompletedAt records when THIS server learned the
			// outcome and drives the fiscal status poll's recency window.
			assert.Equal(t, time.Date(2026, 7, 10, 15, 4, 5, 0, time.UTC), res.DeviceOperationAt)
			assert.False(t, res.CompletedAt.Before(parseStart), "CompletedAt must be the server clock, not the device's")
			assert.False(t, res.CompletedAt.After(time.Now().UTC().Add(time.Minute)))
			assert.JSONEq(t, string(payload), string(res.Raw), "raw payload must be preserved for audit")

			require.Len(t, res.Payments, 1)
			assert.Equal(t, domain.FiscalConfirmedPayment{
				Method: domain.PaymentMethodCash, VendorType: 1, AmountMinor: 15000, Description: "NAKIT",
			}, res.Payments[0])

			// The webhook is tenant-agnostic; the worker resolves these from
			// fiscal_submissions keyed by SubmissionID.
			assert.Equal(t, uuid.Nil, res.TenantID)
			assert.Equal(t, uuid.Nil, res.BranchID)
			assert.Equal(t, uuid.Nil, res.PaymentID)
		})
	}
}

func TestParseWebhookOmitsReceiptNumbersWhenAbsent(t *testing.T) {
	t.Parallel()
	payload := []byte(fmt.Sprintf(`{"operation":"BASKET_COMPLETED","operationDate":"2026-07-10T15:04:05Z",
		"data":{"basketID":%q,"status":-1,"message":"cihaz mesgul","receiptNo":0,"zNo":0}}`, testBasketID))

	res, err := ParseWebhook(payload)
	require.NoError(t, err)
	assert.Equal(t, domain.FiscalSubmissionFailed, res.Status)
	assert.Empty(t, res.ReceiptNo, "a failed sale must not look like it has receipt 0")
	assert.Empty(t, res.ZNo)
	assert.Equal(t, "cihaz mesgul", res.FailureReason)
	assert.Nil(t, res.Payments)
}

func TestParseWebhookPreservesUnknownPaymentType(t *testing.T) {
	t.Parallel()
	payload := []byte(fmt.Sprintf(`{"operation":"BASKET_COMPLETED","operationDate":"2026-07-10T15:04:05Z",
		"data":{"basketID":%q,"status":0,"receiptNo":1,"zNo":1,
		"paymentItems":[{"amount":500,"type":4,"description":"?"}]}}`, testBasketID))

	res, err := ParseWebhook(payload)
	require.NoError(t, err)
	require.Len(t, res.Payments, 1)
	assert.Equal(t, domain.PaymentMethod(""), res.Payments[0].Method, "unmapped code must not be guessed")
	assert.Equal(t, 4, res.Payments[0].VendorType, "raw vendor code must survive for audit")
	assert.Equal(t, int64(500), res.Payments[0].AmountMinor)
}

func TestParseWebhookErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		payload string
		wantErr error
	}{
		{name: "malformed json", payload: `{"operation":`},
		{name: "not json at all", payload: `<html>502 Bad Gateway</html>`},
		{name: "empty payload", payload: ``},
		{
			name:    "lock event routed to the wrong parser",
			payload: fmt.Sprintf(`{"operation":"BASKET_LOCKED","data":{"basketID":%q}}`, testBasketID),
			wantErr: ErrUnexpectedOperation,
		},
		{
			name:    "unknown status",
			payload: fmt.Sprintf(`{"operation":"BASKET_COMPLETED","data":{"basketID":%q,"status":7}}`, testBasketID),
			wantErr: ErrUnknownStatus,
		},
		{
			name:    "basket id is not a uuid",
			payload: `{"operation":"BASKET_COMPLETED","data":{"basketID":"not-a-uuid","status":0}}`,
		},
		{
			name:    "data is not an object",
			payload: `{"operation":"BASKET_COMPLETED","data":"broken"}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseWebhook([]byte(tc.payload))
			require.Error(t, err)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
			}
		})
	}
}

func TestParseLockEvent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		operation  string
		wantLocked bool
	}{
		{name: "locked", operation: OperationBasketLocked, wantLocked: true},
		{name: "unlocked", operation: OperationBasketUnlocked, wantLocked: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			payload := fmt.Appendf(nil, `{"terminalId":"AV0000000658","clientId":"acme-client",
				"operation":%q,"operationDate":"2026-07-10T15:04:05Z","data":{"basketID":%q}}`,
				tc.operation, testBasketID)

			ev, err := ParseLockEvent(payload)
			require.NoError(t, err)
			assert.Equal(t, tc.operation, ev.Operation)
			assert.Equal(t, tc.wantLocked, ev.Locked)
			assert.Equal(t, "AV0000000658", ev.TerminalID)
			assert.Equal(t, "acme-client", ev.ClientID)
			assert.Equal(t, uuid.MustParse(testBasketID), ev.SubmissionID)
			assert.Equal(t, time.Date(2026, 7, 10, 15, 4, 5, 0, time.UTC), ev.OccurredAt)
			assert.JSONEq(t, string(payload), string(ev.Raw))
		})
	}

	t.Run("rejects a completed payload", func(t *testing.T) {
		t.Parallel()
		_, err := ParseLockEvent(completedPayload(0, ""))
		require.ErrorIs(t, err, ErrUnexpectedOperation)
	})

	t.Run("rejects malformed json", func(t *testing.T) {
		t.Parallel()
		_, err := ParseLockEvent([]byte(`{"operation":`))
		require.Error(t, err)
	})
}

func TestWebhookOperation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		payload string
		want    string
		wantErr bool
	}{
		{name: "completed", payload: `{"operation":"BASKET_COMPLETED"}`, want: OperationBasketCompleted},
		{name: "locked", payload: `{"operation":"BASKET_LOCKED"}`, want: OperationBasketLocked},
		{name: "unlocked", payload: `{"operation":"BASKET_UNLOCKED"}`, want: OperationBasketUnlocked},
		{name: "missing operation", payload: `{"terminalId":"T1"}`, wantErr: true},
		{name: "malformed", payload: `nope`, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := WebhookOperation([]byte(tc.payload))
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestParseOperationDateUnparseableLeavesDeviceTimeZero: an unrecognised
// operationDate must not be papered over with time.Now(), which would be this
// server's clock masquerading as the device's. The zero value lets the caller
// see the gap (the webhook handler warns, the payment service falls back to
// CompletedAt for issued_at) instead of persisting a fabricated device time.
func TestParseOperationDateUnparseableLeavesDeviceTimeZero(t *testing.T) {
	t.Parallel()
	payload := fmt.Appendf(nil, `{"operation":"BASKET_COMPLETED","operationDate":"garbage",
		"data":{"basketID":%q,"status":0,"receiptNo":9,"zNo":1}}`, testBasketID)

	before := time.Now().UTC()
	res, err := ParseWebhook(payload)
	require.NoError(t, err)

	assert.True(t, res.DeviceOperationAt.IsZero(), "an unparseable device time must stay zero, not become now()")
	// An unparseable timestamp must not discard a legally registered receipt.
	assert.False(t, res.CompletedAt.Before(before), "the server still records when it learned the outcome")
	assert.Equal(t, "9", res.ReceiptNo)
}

// TestParseOperationDateMissingLeavesDeviceTimeZero covers the absent-field
// case separately from the malformed one: both must behave identically.
func TestParseOperationDateMissingLeavesDeviceTimeZero(t *testing.T) {
	t.Parallel()
	payload := fmt.Appendf(nil, `{"operation":"BASKET_COMPLETED",
		"data":{"basketID":%q,"status":0,"receiptNo":9,"zNo":1}}`, testBasketID)

	res, err := ParseWebhook(payload)
	require.NoError(t, err)

	assert.True(t, res.DeviceOperationAt.IsZero())
	assert.False(t, res.CompletedAt.IsZero())
}

func TestParseWebhookRawIsIndependentOfInput(t *testing.T) {
	t.Parallel()
	payload := completedPayload(0, "")
	res, err := ParseWebhook(payload)
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(res.Raw, &decoded))
	assert.Equal(t, OperationBasketCompleted, decoded["operation"])
}
