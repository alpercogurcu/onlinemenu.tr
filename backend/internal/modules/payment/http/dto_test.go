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

// TestToPaymentResponse_PendingIsVisibleToClients pins the wire contract POS
// depends on after ADR-FISCAL-002: registerSale answers before the device has
// confirmed, so clients must be able to read the pending state (and the absent
// receipt) to render a "fiscal registration in progress" indicator.
func TestToPaymentResponse_PendingIsVisibleToClients(t *testing.T) {
	p := domain.Payment{
		ID:          uuid.New(),
		TenantID:    uuid.New(),
		BranchID:    uuid.New(),
		Method:      domain.PaymentMethodMealCard,
		Status:      domain.PaymentStatusPending,
		AmountTotal: 2000,
		Currency:    "TRY",
		CreatedAt:   time.Now().UTC(),
	}

	body, err := json.Marshal(toPaymentResponse(p))
	require.NoError(t, err)
	var asMap map[string]any
	require.NoError(t, json.Unmarshal(body, &asMap))

	assert.Equal(t, "pending", asMap["status"])
	assert.Equal(t, "meal_card", asMap["method"])
	assert.Nil(t, asMap["fiscal_receipt_id"])
}

// TestToPaymentResponse_VoidedStatus covers the fiş iptali state added with the
// asynchronous fiscal flow.
func TestToPaymentResponse_VoidedStatus(t *testing.T) {
	p := domain.Payment{
		ID:          uuid.New(),
		Method:      domain.PaymentMethodComp,
		Status:      domain.PaymentStatusVoided,
		AmountTotal: 100,
		CreatedAt:   time.Now().UTC(),
	}
	assert.Equal(t, "voided", toPaymentResponse(p).Status)
	assert.Equal(t, "comp", toPaymentResponse(p).Method)
}

// TestRegisterSaleRequest_DecodesFiscalBasket verifies the optional lines/meta
// payload decodes into the vendor-neutral domain types with the resolutions the
// adapters expect (quantity in thousandths, tax rate in permyriad).
func TestRegisterSaleRequest_DecodesFiscalBasket(t *testing.T) {
	categoryID := uuid.New()
	raw := `{
		"branch_id": "cccccccc-0000-0000-0000-000000000001",
		"method": "cash",
		"amount_total": 4200,
		"currency": "TRY",
		"terminal_serial": "AV0000000658",
		"lines": [
			{"name":"Lahmacun","unit_price_minor":2100,"quantity_milli":2000,
			 "tax_rate_permyriad":1000,"category_id":"` + categoryID.String() + `","unit":"C62"}
		],
		"meta": {"table_label":"Masa 5","waiter_name":"Ayse","check_number":12}
	}`

	var req registerSaleRequest
	require.NoError(t, json.Unmarshal([]byte(raw), &req))

	assert.Equal(t, "AV0000000658", req.TerminalSerial)

	lines := toFiscalLines(req.Lines)
	require.Len(t, lines, 1)
	assert.Equal(t, "Lahmacun", lines[0].Name)
	assert.Equal(t, int64(2100), lines[0].UnitPriceMinor)
	assert.Equal(t, int64(2000), lines[0].QuantityMilli, "2 adet = 2000 milli")
	assert.Equal(t, 1000, lines[0].TaxRatePermyriad, "%10 KDV = 1000 permyriad")
	assert.Equal(t, categoryID, lines[0].CategoryID)
	assert.Equal(t, "C62", lines[0].Unit)

	meta := req.Meta.toDomain()
	assert.Equal(t, "Masa 5", meta.TableLabel)
	assert.Equal(t, "Ayse", meta.WaiterName)
	assert.Equal(t, 12, meta.CheckNumber)
}

// TestRegisterSaleRequest_OmittedBasketIsNil documents that omitting lines is
// legal: the service then synthesizes a single-line basket for the total.
func TestRegisterSaleRequest_OmittedBasketIsNil(t *testing.T) {
	var req registerSaleRequest
	require.NoError(t, json.Unmarshal([]byte(`{"method":"cash","amount_total":100}`), &req))
	assert.Nil(t, toFiscalLines(req.Lines))
	assert.Equal(t, domain.FiscalMeta{}, req.Meta.toDomain())
}
