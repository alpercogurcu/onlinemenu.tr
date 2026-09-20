package http_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	paymenthttp "onlinemenu.tr/internal/modules/payment/http"
)

// The TokenX webhook is the one payment route with no principal behind it
// (ADR-FISCAL-002: the ÖKC cloud has no Keycloak identity), so it is
// deliberately absent from authz_smoke_test.go's chi.Walk. That absence is
// exactly the shape of b2b's "middleware wired to zero routes" bug, so the
// guard it uses instead needs its own regression test — this file. It is the
// proof referenced by platform/httpx/route_guard_coverage_test.go's
// routeRegistrars entry for TokenXWebhookHandler.RegisterRoutes.
//
// A nil routing store and nil sink are safe here: every case below is rejected
// before the handler reaches persistence. A case that did reach it would panic
// rather than pass silently, which is the behaviour we want from this test.

// TestTokenXWebhook_NoSecretConfigured_RouteNotMounted proves the endpoint does
// not exist at all without a configured secret. An always-mounted webhook with
// an empty secret would accept "" as the path segment, i.e. be world-writable.
func TestTokenXWebhook_NoSecretConfigured_RouteNotMounted(t *testing.T) {
	h := paymenthttp.NewTokenXWebhookHandler(nil, nil, nil, "", zap.NewNop())
	mux := chi.NewMux()
	h.RegisterRoutes(mux)

	for _, path := range []string{
		paymenthttp.WebhookPathPrefix,
		paymenthttp.WebhookPathPrefix + "anything",
	} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`)))
		assert.Equalf(t, http.StatusNotFound, rec.Code,
			"with no secret configured the webhook route must not be mounted (%s)", path)
	}
}

// TestTokenXWebhook_WrongSecret_Returns404 pins two properties at once: a
// wrong secret is refused, and it is refused with 404 rather than 401/403 — a
// 401 would confirm to a path scanner that the endpoint is real.
func TestTokenXWebhook_WrongSecret_Returns404(t *testing.T) {
	const secret = "s3cret-path-segment"
	h := paymenthttp.NewTokenXWebhookHandler(nil, nil, nil, secret, zap.NewNop())
	mux := chi.NewMux()
	h.RegisterRoutes(mux)

	cases := []struct{ name, segment string }{
		{"empty-ish", "x"},
		{"prefix of the real secret", secret[:len(secret)-1]},
		{"case-flipped", strings.ToUpper(secret)},
		{"secret plus suffix", secret + "x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, paymenthttp.WebhookPathPrefix+tc.segment, strings.NewReader(`{}`))
			mux.ServeHTTP(rec, req)
			require.Equal(t, http.StatusNotFound, rec.Code,
				"a wrong secret must answer 404 (not 401/403, which confirms the endpoint exists)")
		})
	}
}

// TestTokenXWebhook_CorrectSecret_ReachesPayloadParsing proves the guard is a
// guard and not a wall: the right secret gets past the path check and is then
// rejected on payload grounds (400), never 404. Without this, a typo that made
// every request 404 would leave the tests above passing and fiscal
// registration silently dead — the ADR-FISCAL-001 "sessiz başarı = yasal
// risk" case, inverted.
func TestTokenXWebhook_CorrectSecret_ReachesPayloadParsing(t *testing.T) {
	const secret = "s3cret-path-segment"
	h := paymenthttp.NewTokenXWebhookHandler(nil, nil, nil, secret, zap.NewNop())
	mux := chi.NewMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, paymenthttp.WebhookPathPrefix+secret, strings.NewReader(`not json`))
	mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code,
		"the correct secret must reach payload decoding (400 on garbage), not 404")
}
