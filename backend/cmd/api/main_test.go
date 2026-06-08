package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"

	"onlinemenu.tr/internal/modules/catalog"
	"onlinemenu.tr/internal/modules/identity"
	"onlinemenu.tr/internal/modules/payment"
	"onlinemenu.tr/internal/modules/pos"
	"onlinemenu.tr/internal/modules/tenant"
	"onlinemenu.tr/internal/platform/auth"
	"onlinemenu.tr/internal/platform/cache"
	"onlinemenu.tr/internal/platform/db"
	"onlinemenu.tr/internal/platform/eventbus"
	platformotel "onlinemenu.tr/internal/platform/otel"
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

		db.Module,
		eventbus.Module,
		platformotel.Module,
		vault.Module,
		cache.Module,
		fx.Provide(auth.NewEngine),
		fx.Provide(newContextTokenSigner),
		fx.Provide(newTokenVerifier),

		identity.Module,
		tenant.Module,
		catalog.Module,
		pos.Module,
		payment.Module,

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

	router := newRouter(signer, verifier)

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

// Silence fxtest logger to keep test output clean.
var _ = fxtest.New
