package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
	"go.uber.org/zap"

	"onlinemenu.tr/internal/modules/catalog"
	"onlinemenu.tr/internal/modules/identity"
	"onlinemenu.tr/internal/modules/inventory"
	"onlinemenu.tr/internal/modules/payment"
	paymenthttp "onlinemenu.tr/internal/modules/payment/http"
	"onlinemenu.tr/internal/modules/pos"
	"onlinemenu.tr/internal/modules/storefront"
	"onlinemenu.tr/internal/modules/tenant"
	"onlinemenu.tr/internal/platform/auth"
	"onlinemenu.tr/internal/platform/cache"
	"onlinemenu.tr/internal/platform/db"
	"onlinemenu.tr/internal/platform/eventbus"
	"onlinemenu.tr/internal/platform/keycloak"
	platformotel "onlinemenu.tr/internal/platform/otel"
	"onlinemenu.tr/internal/platform/outbox"
	"onlinemenu.tr/internal/platform/vault"
)

// TestFxGraphValidation verifies the dependency injection graph is satisfiable
// without starting any lifecycle hooks (no network, no DB required).
func TestFxGraphValidation(t *testing.T) {
	t.Setenv("CTX_TOKEN_SECRET", "test-secret-32-bytes-long-padding!")
	t.Setenv("STOREFRONT_GUEST_TOKEN_SECRET", "guest-secret-32-bytes-long-paddin!")
	t.Setenv("DATABASE_URL", "pgx5://app_runtime:runtime@localhost:5432/testdb?sslmode=disable")
	t.Setenv("NATS_URL", "nats://localhost:4222")
	t.Setenv("VAULT_ADDR", "http://localhost:8200")
	t.Setenv("VAULT_TOKEN", "test-token")

	err := fx.ValidateApp(
		fx.Provide(newLogger),
		fx.Provide(newDBConfig),
		fx.Provide(newEventBusConfig),
		fx.Provide(newOTelConfig),
		fx.Provide(newVaultConfig),
		fx.Provide(newCacheConfig),
		fx.Provide(newOPAConfig),
		fx.Provide(newHTTPConfig),
		fx.Provide(newOutboxConfig),
		fx.Provide(newPosWSConfig),
		fx.Provide(newFiscalConfig),
		fx.Provide(newKeycloakConfig),

		db.Module,
		eventbus.Module,
		platformotel.Module,
		vault.Module,
		cache.Module,
		outbox.Module,
		keycloak.Module,
		fx.Provide(auth.NewEngine),
		fx.Provide(newContextTokenSigner),
		fx.Provide(newGuestTokenSigner),
		fx.Provide(newTokenVerifier),

		identity.Module,
		tenant.Module,
		catalog.Module,
		pos.Module,
		payment.Module,
		inventory.Module,
		storefront.Module,

		fx.Provide(newRouter),
		fx.Invoke(registerHTTPServer),
	)
	require.NoError(t, err, "fx dependency graph must be satisfiable")
}

// TestRouterMiddleware verifies auth middleware is mounted correctly:
//   - /healthz responds 200 without a token
//   - any other path without a token responds 401
func TestRouterMiddleware(t *testing.T) {
	const secret = "test-secret-32-bytes-long-padding!"

	t.Setenv("APP_ENV", "dev")

	signer, err := auth.NewContextTokenSigner([]byte(secret))
	require.NoError(t, err)

	verifier, err := newTokenVerifier()
	require.NoError(t, err)

	router := newRouter(routerParams{
		Signer:   signer,
		Verifier: verifier,
		Pool:     nil,
		Logger:   zap.NewNop(),
	})

	t.Run("healthz_no_auth_200", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("protected_no_token_401", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/catalog/products", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	// The public storefront prefix skips the STAFF auth chain: its callers
	// are anonymous diners with no Keycloak identity, and authMW would reject
	// every one of them. Reaching the router's own 404 (rather than a 401)
	// proves the exemption is in place; that the routes behind it are still
	// guarded is proven by storefront/http/public_guard_smoke_test.go, which
	// walks the real registrar.
	t.Run("public_prefix_bypasses_staff_auth", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/public/v1/menu", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		assert.NotEqual(t, http.StatusUnauthorized, w.Code,
			"the staff auth middleware must not intercept the public surface")
		assert.Equal(t, http.StatusNotFound, w.Code,
			"no storefront routes are registered on this bare router")
	})

	// Only the exact prefix is exempt: a lookalike path must stay protected,
	// or the exemption becomes a hole anyone can walk through by prefixing.
	t.Run("public_lookalike_path_still_protected", func(t *testing.T) {
		for _, path := range []string{"/api/public", "/api/publicx/v1/menu", "/api/v1/public/menu"} {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			assert.Equalf(t, http.StatusUnauthorized, w.Code, "path %q must require auth", path)
		}
	})
}

// TestTracedHandlerDoesNotLeakWebhookSecretIntoSpans proves the otelhttp
// wiring no longer records the TokenX webhook's path-embedded secret. Before
// the fix, otelhttp.NewHandler(router, "api") had no filter, so the literal
// request path — including the secret — was captured as the http.target /
// url.path span attribute for every webhook delivery.
func TestTracedHandlerDoesNotLeakWebhookSecretIntoSpans(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	t.Cleanup(func() { require.NoError(t, tp.Shutdown(context.Background())) })

	const secret = "s3cr3t-must-not-leak-into-any-span"

	router := chi.NewRouter()
	router.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	router.Post(paymenthttp.WebhookPathPrefix+"{secret}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := tracedHandler(router, otelhttp.WithTracerProvider(tp))

	// A traced, non-secret route must still produce a span...
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	// ...but the webhook route, whose path carries the secret, must not be
	// traced at all: that is the only way to guarantee the secret never
	// reaches a span attribute, span name, or (downstream) a log line.
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, paymenthttp.WebhookPathPrefix+secret, nil))
	require.Equal(t, http.StatusOK, rec.Code)

	require.NoError(t, tp.ForceFlush(context.Background()))
	spans := exporter.GetSpans()

	require.Len(t, spans, 1, "only the non-webhook route should produce a span")
	for _, span := range spans {
		assert.NotContains(t, span.Name, secret, "span name must not carry the webhook secret")
		for _, attr := range span.Attributes {
			assert.NotContains(t, attr.Value.Emit(), secret,
				"span attribute %s must not carry the webhook secret", attr.Key)
		}
	}
}

// TestTracedHandlerDoesNotTracePublicStorefrontPaths is the defense-in-depth
// half of the QR-token leak prevention (ADR-ARCH-006 §3 / plan note D1). The
// token is sent in the request body precisely so it never lands in a span,
// but otelhttp records the raw path before chi routes it — so if anyone ever
// adds /api/public/v1/q/{token}, the exclusion must already be in place.
func TestTracedHandlerDoesNotTracePublicStorefrontPaths(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	t.Cleanup(func() { require.NoError(t, tp.Shutdown(context.Background())) })

	const token = "qr-token-must-not-leak-into-any-span"

	router := chi.NewRouter()
	router.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	router.Post("/api/public/v1/sessions", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	router.Get("/api/public/v1/q/{token}", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	handler := tracedHandler(router, otelhttp.WithTracerProvider(tp))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/public/v1/sessions", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/public/v1/q/"+token, nil))
	require.Equal(t, http.StatusOK, rec.Code)

	require.NoError(t, tp.ForceFlush(context.Background()))
	spans := exporter.GetSpans()

	require.Len(t, spans, 1, "only the non-public route should produce a span")
	for _, span := range spans {
		for _, attr := range span.Attributes {
			assert.NotContains(t, attr.Value.Emit(), token)
			assert.NotContains(t, attr.Value.Emit(), "/api/public/v1")
		}
	}
}

// Silence fxtest logger to keep test output clean.
var _ = fxtest.New
