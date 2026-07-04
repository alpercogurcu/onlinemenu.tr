package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.uber.org/fx"
	"go.uber.org/fx/fxevent"
	"go.uber.org/zap"

	"onlinemenu.tr/internal/platform/auth"
	"onlinemenu.tr/internal/platform/cache"
	"onlinemenu.tr/internal/platform/db"
	"onlinemenu.tr/internal/platform/eventbus"
	platformotel "onlinemenu.tr/internal/platform/otel"
	"onlinemenu.tr/internal/platform/vault"
)

// api-pos serves the POS deployment group: pos, catalog, edge_sync.
// Faz 1: pos.Module, catalog.Module, edge_sync.Module are wired here when implemented.
func main() {
	// Context is cancelled on SIGINT or SIGTERM, triggering graceful shutdown.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	app := fx.New(
		fx.WithLogger(func(log *zap.Logger) fxevent.Logger {
			return &fxevent.ZapLogger{Logger: log}
		}),

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

		// pos.Module,       (Faz 1)
		// catalog.Module,   (Faz 1)
		// edge_sync.Module, (Faz 1)

		fx.Provide(newRouter),
		fx.Invoke(startHTTP),
	)

	// app.Run() is intentionally NOT used here: it registers its own signal
	// handler and blocks until shutdown, then returns — calling app.Stop()
	// again afterwards double-stops an already-stopped app. Start/Done/Stop
	// mirrors cmd/api/main.go's lifecycle exactly.
	startCtx, startCancel := context.WithTimeout(ctx, 15*time.Second)
	defer startCancel()

	if err := app.Start(startCtx); err != nil {
		fmt.Fprintf(os.Stderr, "api-pos: start: %v\n", err)
		os.Exit(1)
	}

	<-app.Done()

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer stopCancel()

	if err := app.Stop(stopCtx); err != nil {
		fmt.Fprintf(os.Stderr, "api-pos: stop: %v\n", err)
		os.Exit(1)
	}
}

type httpConfig struct {
	Addr         string
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	IdleTimeout  time.Duration
}

// newRouter wires the auth middleware onto every path except /healthz, matching
// cmd/api and cmd/api-core/api-finance. pos.Module/catalog.Module route
// registration is not yet wired here (Faz 1 TODO above) but will rely on this
// middleware being present from day one, not bolted on later.
func newRouter(signer *auth.ContextTokenSigner, verifier auth.TokenVerifier) *chi.Mux {
	r := chi.NewRouter()
	r.Use(chimiddleware.RequestID)
	r.Use(chimiddleware.RealIP)
	r.Use(chimiddleware.Recoverer)
	r.Use(func(next http.Handler) http.Handler {
		return otelhttp.NewHandler(next, "api-pos")
	})

	authMW := auth.Middleware(verifier, signer)
	r.Use(func(next http.Handler) http.Handler {
		protected := authMW(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/healthz" {
				next.ServeHTTP(w, r)
				return
			}
			protected.ServeHTTP(w, r)
		})
	})

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	return r
}

func startHTTP(lc fx.Lifecycle, cfg httpConfig, r *chi.Mux, log *zap.Logger) {
	srv := &http.Server{
		Addr:         cfg.Addr,
		Handler:      r,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		IdleTimeout:  cfg.IdleTimeout,
	}
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			log.Info("api-pos HTTP server starting", zap.String("addr", cfg.Addr))
			go func() {
				if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
					log.Error("api-pos HTTP server error", zap.Error(err))
				}
			}()
			return nil
		},
		OnStop: func(ctx context.Context) error {
			return srv.Shutdown(ctx)
		},
	})
}

func newLogger() (*zap.Logger, error) {
	logger, err := zap.NewProduction()
	if err != nil {
		return nil, fmt.Errorf("api-pos: build logger: %w", err)
	}
	return logger, nil
}

func newDBConfig() db.Config {
	return db.Config{
		DSN:             mustEnv("DATABASE_URL"),
		MaxConns:        20,
		MinConns:        4,
		MaxConnLifetime: 30 * time.Minute,
		MaxConnIdleTime: 5 * time.Minute,
	}
}

func newEventBusConfig() eventbus.Config {
	return eventbus.Config{
		URL:        mustEnv("NATS_URL"),
		StreamName: "DOMAIN_EVENTS",
		Subjects:   []string{"pos.>", "catalog.>", "inventory.>"},
	}
}

func newOTelConfig() platformotel.Config {
	return platformotel.Config{
		Endpoint:       envOr("OTEL_EXPORTER_OTLP_ENDPOINT", "localhost:4317"),
		ServiceName:    "onlinemenu-api-pos",
		ServiceVersion: envOr("APP_VERSION", "dev"),
	}
}

func newVaultConfig() vault.Config {
	return vault.Config{
		Address: mustEnv("VAULT_ADDR"),
		Token:   mustEnv("VAULT_TOKEN"),
	}
}

func newCacheConfig() cache.Config {
	return cache.Config{
		Addr:     envOr("REDIS_ADDR", "localhost:6379"),
		Password: envOr("REDIS_PASSWORD", ""),
		DB:       0,
		PoolSize: 20,
	}
}

func newOPAConfig() auth.EngineConfig {
	return auth.EngineConfig{
		BundlePath: envOr("OPA_BUNDLE_PATH", "configs/opa/bundles"),
	}
}

func newHTTPConfig() httpConfig {
	return httpConfig{
		Addr:         envOr("HTTP_ADDR", ":8082"),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		fmt.Fprintf(os.Stderr, "api-pos: required env var %q is not set\n", key)
		os.Exit(1)
	}
	return v
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func newContextTokenSigner() (*auth.ContextTokenSigner, error) {
	secret := envOr("CTX_TOKEN_SECRET", "")
	if secret == "" {
		return nil, errors.New("api-pos: CTX_TOKEN_SECRET env var is required")
	}
	return auth.NewContextTokenSigner([]byte(secret))
}

// devTokenVerifier parses JWT claims without signature verification.
// ONLY active when APP_ENV=dev. See cmd/api/main.go for the production
// (Keycloak JWKS) counterpart; this binary shares the same policy.
type devTokenVerifier struct{}

func newTokenVerifier() (auth.TokenVerifier, error) {
	env := envOr("APP_ENV", "")
	if env == "dev" {
		return devTokenVerifier{}, nil
	}

	issuerURL := envOr("KEYCLOAK_ISSUER_URL", "")
	audience := envOr("KEYCLOAK_AUDIENCE", "")
	if issuerURL == "" || audience == "" {
		return nil, fmt.Errorf("api-pos: KEYCLOAK_ISSUER_URL and KEYCLOAK_AUDIENCE are required when APP_ENV=%q", env)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	verifier, err := auth.NewKeycloakVerifier(ctx, auth.KeycloakVerifierConfig{
		IssuerURL: issuerURL,
		JWKSURL:   envOr("KEYCLOAK_JWKS_URL", ""),
		Audience:  audience,
	})
	if err != nil {
		return nil, fmt.Errorf("api-pos: build keycloak verifier: %w", err)
	}
	return verifier, nil
}

func (devTokenVerifier) Verify(_ context.Context, rawToken string) (*auth.KeycloakClaims, error) {
	parts := strings.SplitN(rawToken, ".", 3)
	if len(parts) != 3 {
		return nil, errors.New("auth: invalid JWT format")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("auth: decode JWT payload: %w", err)
	}
	var claims struct {
		Sub string `json:"sub"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, fmt.Errorf("auth: unmarshal JWT claims: %w", err)
	}
	if claims.Sub == "" {
		return nil, errors.New("auth: missing sub claim")
	}
	return &auth.KeycloakClaims{Sub: claims.Sub}, nil
}
