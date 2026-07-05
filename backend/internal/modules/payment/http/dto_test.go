package http

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"onlinemenu.tr/internal/modules/payment/domain"
)

// TestToPaymentResponse_SerializesSnakeCase is the regression test for the
// registerSale/getPayment vs listPayments casing inconsistency: before this
// fix, registerSale and getPayment wrote domain.Payment directly to the
// response body. domain.Payment carries no json tags, so its fields
// serialized verbatim in Go's default PascalCase (ID, BranchID, ...), while
// listPayments alone went through this same DTO in snake_case. Every payment
// handler now routes through toPaymentResponse, so this single test covers
// all three endpoints' wire shape by construction.
func TestToPaymentResponse_SerializesSnakeCase(t *testing.T) {
	checkID := uuid.New()
	receiptID := uuid.New()
	completedAt := time.Now().UTC()

	p := domain.Payment{
		ID:              uuid.New(),
		TenantID:        uuid.New(),
		BranchID:        uuid.New(),
		CheckID:         &checkID,
		IdempotencyKey:  "idem-1",
		Method:          domain.PaymentMethodCash,
		Status:          domain.PaymentStatusCompleted,
		AmountTotal:     1500,
		Currency:        "TRY",
		FiscalReceiptID: &receiptID,
		CreatedAt:       time.Now().UTC(),
		CompletedAt:     &completedAt,
	}

	body, err := json.Marshal(toPaymentResponse(p))
	require.NoError(t, err)

	var asMap map[string]any
	require.NoError(t, json.Unmarshal(body, &asMap))

	// Expected snake_case keys must be present.
	for _, key := range []string{
		"id", "tenant_id", "branch_id", "check_id", "method", "status",
		"amount_total", "currency", "fiscal_receipt_id", "created_at", "completed_at",
	} {
		_, ok := asMap[key]
		assert.Truef(t, ok, "expected snake_case key %q in response body: %s", key, body)
	}

	// The bug's PascalCase field names must never reappear.
	for _, key := range []string{
		"ID", "BranchID", "CheckID", "Method", "Status", "AmountTotal",
		"Currency", "FiscalReceiptID", "CreatedAt", "CompletedAt",
	} {
		_, ok := asMap[key]
		assert.Falsef(t, ok, "PascalCase key %q must not appear in response body: %s", key, body)
	}

	assert.Equal(t, float64(1500), asMap["amount_total"])
	assert.Equal(t, "cash", asMap["method"])
	assert.Equal(t, "completed", asMap["status"])
}

// TestToPaymentResponse_NilCompletedAt verifies a pending payment (no
// completed_at yet) marshals completed_at as JSON null rather than a
// zero-value timestamp string.
func TestToPaymentResponse_NilCompletedAt(t *testing.T) {
	p := domain.Payment{
		ID:          uuid.New(),
		TenantID:    uuid.New(),
		BranchID:    uuid.New(),
		Method:      domain.PaymentMethodTerminal,
		Status:      domain.PaymentStatusPending,
		AmountTotal: 2000,
		Currency:    "TRY",
		CreatedAt:   time.Now().UTC(),
	}

	resp := toPaymentResponse(p)
	assert.Nil(t, resp.CompletedAt)

	body, err := json.Marshal(resp)
	require.NoError(t, err)
	var asMap map[string]any
	require.NoError(t, json.Unmarshal(body, &asMap))
	assert.Nil(t, asMap["completed_at"])
}
