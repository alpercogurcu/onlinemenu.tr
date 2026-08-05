package http_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	catalogpub "onlinemenu.tr/internal/modules/catalog/public"
	pospub "onlinemenu.tr/internal/modules/pos/public"
)

// TestPlaceOrder_ClientSuppliedPriceIsIgnored is the transport-level half of
// the anti-tampering guarantee: a body that carries unit_price_amount,
// price_amount and product_name still results in an order priced from the
// catalog read model, because the request DTO has no field to decode any of
// them into.
func TestPlaceOrder_ClientSuppliedPriceIsIgnored(t *testing.T) {
	productID := uuid.New()
	deps := &testDeps{
		menuReader: &stubMenuReader{priced: []catalogpub.PricedLine{{
			ProductID:       productID,
			ProductName:     "Latte",
			BasePriceAmount: 4500,
			UnitPriceAmount: 4500,
			Currency:        "TRY",
			TaxRateBPS:      1000,
			Quantity:        2,
		}}},
	}
	router := newPublicRouter(t, deps)

	body := `{"lines":[{"product_id":"` + productID.String() + `","quantity":2,` +
		`"unit_price_amount":1,"price_amount":1,"product_name":"Bedava Latte","total":1}]}`

	req := httptest.NewRequest(http.MethodPost, "/api/public/v1/orders", strings.NewReader(body))
	req.AddCookie(guestCookie(t, deps.signer))
	req.Header.Set("Idempotency-Key", uuid.NewString())
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	require.Len(t, deps.orders.gotRequest.Lines, 1)
	line := deps.orders.gotRequest.Lines[0]
	assert.Equal(t, int64(4500), line.UnitPriceAmount, "the client's 1 kuruş must not survive")
	assert.Equal(t, "Latte", line.Name, "the client's product name must not survive")

	var resp struct {
		OrderID string `json:"order_id"`
		CheckID string `json:"check_id"`
		Status  string `json:"status"`
		Total   int64  `json:"total"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, int64(9000), resp.Total)
	assert.Equal(t, "pending", resp.Status)
}

// TestPlaceOrder_MissingIdempotencyKey_Returns422 keeps ADR-SEC-003 wired on
// the one mutating public endpoint: without it, a retried cart submission on
// a flaky mobile connection becomes a second order.
func TestPlaceOrder_MissingIdempotencyKey_Returns422(t *testing.T) {
	deps := &testDeps{}
	router := newPublicRouter(t, deps)

	req := httptest.NewRequest(http.MethodPost, "/api/public/v1/orders",
		strings.NewReader(`{"lines":[{"product_id":"`+uuid.NewString()+`","quantity":1}]}`))
	req.AddCookie(guestCookie(t, deps.signer))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Empty(t, deps.orders.gotRequest.Lines, "the order must not be placed without an idempotency key")
}

// TestPlaceOrder_SameKeyReplays proves the guest idempotency scope is wired
// (httpx.GuestSessionScope), not just the header check.
func TestPlaceOrder_SameKeyReplays(t *testing.T) {
	productID := uuid.New()
	deps := &testDeps{
		menuReader: &stubMenuReader{priced: []catalogpub.PricedLine{{
			ProductID: productID, ProductName: "Çay", UnitPriceAmount: 1000, Quantity: 1,
		}}},
	}
	router := newPublicRouter(t, deps)

	cookie := guestCookie(t, deps.signer)
	key := uuid.NewString()
	body := `{"lines":[{"product_id":"` + productID.String() + `","quantity":1}]}`

	send := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/public/v1/orders", strings.NewReader(body))
		req.AddCookie(cookie)
		req.Header.Set("Idempotency-Key", key)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	first := send()
	require.Equal(t, http.StatusCreated, first.Code, first.Body.String())
	second := send()

	assert.Equal(t, http.StatusCreated, second.Code)
	assert.Equal(t, "true", second.Header().Get("Idempotency-Replayed"))
	assert.JSONEq(t, first.Body.String(), second.Body.String(),
		"a retried submission must replay the first order, not create a second")
}

func TestPlaceOrder_TableBeingCleaned_Returns409WithDistinctCode(t *testing.T) {
	productID := uuid.New()
	deps := &testDeps{
		menuReader: &stubMenuReader{priced: []catalogpub.PricedLine{{
			ProductID: productID, ProductName: "Çay", UnitPriceAmount: 1000, Quantity: 1,
		}}},
		orders: &stubOrderGateway{placeErr: pospub.ErrTableNotReady},
	}
	router := newPublicRouter(t, deps)

	req := httptest.NewRequest(http.MethodPost, "/api/public/v1/orders",
		strings.NewReader(`{"lines":[{"product_id":"`+productID.String()+`","quantity":1}]}`))
	req.AddCookie(guestCookie(t, deps.signer))
	req.Header.Set("Idempotency-Key", uuid.NewString())
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusConflict, rec.Code)

	var problem struct {
		Code   string `json:"code"`
		Detail string `json:"detail"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &problem))
	assert.Equal(t, "table_not_ready", problem.Code,
		"a table being cleaned must be distinguishable from an occupied one — the advice differs")
	assert.NotContains(t, strings.ToLower(problem.Detail), "dolu")
}

// TestPlaceOrder_TableMovedToAnotherBranch_Returns409WithDistinctCode covers
// the sticker that outlived its table: the code was printed for one branch and
// the table now belongs to another. Unlike a scan (POST /sessions), which
// collapses every failure into 404 to avoid a token oracle, an established
// session that already passed the scan gets the actionable answer — the diner
// must be told to call staff instead of retrying.
func TestPlaceOrder_TableMovedToAnotherBranch_Returns409WithDistinctCode(t *testing.T) {
	productID := uuid.New()
	deps := &testDeps{
		menuReader: &stubMenuReader{priced: []catalogpub.PricedLine{{
			ProductID: productID, ProductName: "Çay", UnitPriceAmount: 1000, Quantity: 1,
		}}},
		orders: &stubOrderGateway{placeErr: pospub.ErrTableBranchMismatch},
	}
	router := newPublicRouter(t, deps)

	req := httptest.NewRequest(http.MethodPost, "/api/public/v1/orders",
		strings.NewReader(`{"lines":[{"product_id":"`+productID.String()+`","quantity":1}]}`))
	req.AddCookie(guestCookie(t, deps.signer))
	req.Header.Set("Idempotency-Key", uuid.NewString())
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())

	var problem struct {
		Code string `json:"code"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &problem))
	assert.Equal(t, "table_branch_mismatch", problem.Code)
}

// TestPlaceOrder_ProductNotOrderableHere_Returns422 is the transport half of
// the cross-tenant/off-menu product rejection: catalog refuses to price a
// product that is not on this branch's dine-in menu (another tenant's product
// included), and that refusal must reach the diner as a 422, never as a 500.
func TestPlaceOrder_ProductNotOrderableHere_Returns422(t *testing.T) {
	deps := &testDeps{
		menuReader: &stubMenuReader{
			priceErr: &catalogpub.ValidationError{Msg: "ürün bu şubede sipariş edilemiyor"},
		},
	}
	router := newPublicRouter(t, deps)

	req := httptest.NewRequest(http.MethodPost, "/api/public/v1/orders",
		strings.NewReader(`{"lines":[{"product_id":"`+uuid.NewString()+`","quantity":1}]}`))
	req.AddCookie(guestCookie(t, deps.signer))
	req.Header.Set("Idempotency-Key", uuid.NewString())
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())

	var problem struct {
		Code   string `json:"code"`
		Detail string `json:"detail"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &problem))
	assert.Equal(t, "validation_failed", problem.Code)
	assert.Contains(t, problem.Detail, "sipariş edilemiyor")
}

func TestPlaceOrder_EmptyCart_Returns422(t *testing.T) {
	deps := &testDeps{}
	router := newPublicRouter(t, deps)

	req := httptest.NewRequest(http.MethodPost, "/api/public/v1/orders", strings.NewReader(`{"lines":[]}`))
	req.AddCookie(guestCookie(t, deps.signer))
	req.Header.Set("Idempotency-Key", uuid.NewString())
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
}
