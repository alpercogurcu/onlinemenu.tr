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

	"onlinemenu.tr/internal/modules/catalog"
	"onlinemenu.tr/internal/modules/identity"
	"onlinemenu.tr/internal/modules/inventory"
	"onlinemenu.tr/internal/modules/payment"
	paymenthttp "onlinemenu.tr/internal/modules/payment/http"
	"onlinemenu.tr/internal/modules/pos"
	"onlinemenu.tr/internal/modules/tenant"
	"onlinemenu.tr/internal/platform/auth"
	"onlinemenu.tr/internal/platform/cache"
	"onlinemenu.tr/internal/platform/db"
	"onlinemenu.tr/internal/platform/eventbus"
	platformotel "onlinemenu.tr/internal/platform/otel"
	"onlinemenu.tr/internal/platform/outbox"
	"onlinemenu.tr/internal/platform/vault"
)

// TestFxGraphValidation verifies the dependency injection graph is satisfiable
// without starting any lifecycle hooks (no network, no DB required).
func TestFxGraphValidation(t *testing.T) {
	t.Setenv("CTX_TOKEN_SECRET", "test-secret-32-bytes-long-padding!")
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

		db.Module,
		eventbus.Module,
		platformotel.Module,
		vault.Module,
		cache.Module,
		outbox.Module,
		fx.Provide(auth.NewEngine),
		fx.Provide(newContextTokenSigner),
		fx.Provide(newTokenVerifier),

		identity.Module,
		tenant.Module,
		catalog.Module,
		pos.Module,
		payment.Module,
		inventory.Module,

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

	router := newRouter(signer, verifier, nil)

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

// Silence fxtest logger to keep test output clean.
var _ = fxtest.New
