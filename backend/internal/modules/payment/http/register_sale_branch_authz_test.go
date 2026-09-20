package http_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	paymenthttp "onlinemenu.tr/internal/modules/payment/http"
	"onlinemenu.tr/internal/platform/auth"
)

// Regression for the 2026-09-20 branch-isolation finding: a cashier bound to
// branch B posted branch_id=A to POST /api/v1/payments and got 201 — the money
// landed in branch A's open drawer and a fiscal receipt was minted against it.
// OPA (layer 2) allows "register a sale" without ever seeing which branch the
// body names, and the check guard only compared the adisyon against that same
// attacker-supplied branch_id, so nothing compared it to the principal.
//
// The whole route chain is exercised (real OPA bundle + real Idempotency
// middleware over miniredis) rather than the handler alone, because the guard
// has to win against two middlewares that run first: a 500 from a dead cache
// or a 422 from the idempotency header check would hide the verdict under test.
//
// PaymentService is deliberately nil: a request that gets past the guard
// panics on it and recoverMiddleware turns that into a 500, so "not 403" reads
// as exactly "the branch guard let it through" — the same technique
// order_cancel_authz_test.go (pos) uses.
func newRegisterSaleMux(t *testing.T) *chi.Mux {
	t.Helper()
	engine := newSmokeTestEngine(t)
	mr := miniredis.RunT(t)
	hwc := paymenthttp.NewHandler(paymenthttp.Params{
		Logger: zap.NewNop(),
		Engine: engine,
		Cache:  redis.NewClient(&redis.Options{Addr: mr.Addr()}),
	})
	mux := chi.NewMux()
	mux.Use(recoverMiddleware)
	hwc.RegisterRoutes(mux)
	return mux
}

func branchPrincipal(roleID string, branchID uuid.UUID) auth.Principal {
	return auth.Principal{
		PersonID: uuid.New(),
		Ctx:      auth.ContextStaff,
		TenantID: uuid.MustParse("aaaaaaaa-0000-0000-0000-00000000aaaa"),
		BranchID: branchID,
		RoleIDs:  []uuid.UUID{uuid.MustParse(roleID)},
	}
}

func postSale(t *testing.T, mux http.Handler, p auth.Principal, branchID uuid.UUID, idemKey string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"branch_id":    branchID,
		"method":       "cash",
		"amount_total": 1500,
		"currency":     "TRY",
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/payments", bytes.NewReader(body))
	req.Header.Set("Idempotency-Key", idemKey)
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(auth.WithPrincipal(req.Context(), p))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestRegisterSale_CrossBranchRefused(t *testing.T) {
	mux := newRegisterSaleMux(t)
	branchA := uuid.MustParse("11111111-0000-0000-0000-0000000000aa")
	branchB := uuid.MustParse("22222222-0000-0000-0000-0000000000bb")

	rec := postSale(t, mux, branchPrincipal(cashierRoleID, branchB), branchA, "e2e-cross-branch")

	require.Equal(t, http.StatusForbidden, rec.Code, "branch B cashier must not register a sale for branch A")

	var body struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	// The POS client branches on the code, not the message: a misconfigured
	// station has to be told apart from a mistyped adisyon (check_branch_mismatch).
	assert.Equal(t, "branch_forbidden", body.Code)
}

func TestRegisterSale_OwnBranchAndChainManagerPassTheGuard(t *testing.T) {
	branchA := uuid.MustParse("11111111-0000-0000-0000-0000000000aa")
	branchB := uuid.MustParse("22222222-0000-0000-0000-0000000000bb")

	tests := []struct {
		name      string
		principal auth.Principal
		branchID  uuid.UUID
	}{
		{
			name:      "cashier sells at their own branch",
			principal: branchPrincipal(cashierRoleID, branchB),
			branchID:  branchB,
		},
		{
			// The chain manager resolves to OPA scope "tenant" and is exempt
			// by design — a multi-branch owner settles anywhere in the chain.
			name:      "chain manager sells at another branch",
			principal: branchPrincipal(managerRoleID, branchB),
			branchID:  branchA,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mux := newRegisterSaleMux(t)
			rec := postSale(t, mux, tt.principal, tt.branchID, "e2e-"+tt.name)
			assert.NotEqual(t, http.StatusForbidden, rec.Code,
				"the branch guard must not refuse this caller (got %d)", rec.Code)
		})
	}
}
