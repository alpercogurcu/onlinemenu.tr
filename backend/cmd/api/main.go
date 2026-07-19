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
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.uber.org/fx"
	"go.uber.org/fx/fxevent"
	"go.uber.org/zap"

	"onlinemenu.tr/internal/modules/catalog"
	"onlinemenu.tr/internal/modules/identity"
	"onlinemenu.tr/internal/modules/inventory"
	"onlinemenu.tr/internal/modules/payment"
	"onlinemenu.tr/internal/modules/payment/fiscal/tokenx"
	paymenthttp "onlinemenu.tr/internal/modules/payment/http"
	"onlinemenu.tr/internal/modules/pos"
	posws "onlinemenu.tr/internal/modules/pos/ws"
	"onlinemenu.tr/internal/modules/tenant"
	"onlinemenu.tr/internal/platform/auth"
	"onlinemenu.tr/internal/platform/cache"
	"onlinemenu.tr/internal/platform/db"
	"onlinemenu.tr/internal/platform/eventbus"
	platformotel "onlinemenu.tr/internal/platform/otel"
	"onlinemenu.tr/internal/platform/outbox"
	"onlinemenu.tr/internal/platform/vault"
)

func main() {
	// Context is cancelled on SIGINT or SIGTERM, triggering graceful shutdown.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	app := fx.New(
		fx.WithLogger(func(log *zap.Logger) fxevent.Logger {
			return &fxevent.ZapLogger{Logger: log}
		}),

		// Platform layer
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

		// Domain modules
		identity.Module,
		tenant.Module,
		catalog.Module,
		pos.Module,
		payment.Module,
		inventory.Module,

		// HTTP server
		fx.Provide(newRouter),
		fx.Invoke(registerHTTPServer),
	)

	startCtx, startCancel := context.WithTimeout(ctx, 15*time.Second)
	defer startCancel()

	if err := app.Start(startCtx); err != nil {
		// os.Exit is allowed only in main().
		fmt.Fprintf(os.Stderr, "api: start: %v\n", err)
		os.Exit(1)
	}

	// Block until SIGINT/SIGTERM.
	<-app.Done()

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer stopCancel()

	if err := app.Stop(stopCtx); err != nil {
		fmt.Fprintf(os.Stderr, "api: stop: %v\n", err)
		os.Exit(1)
	}
}

// httpConfig groups HTTP server tunables provided via config injection.
type httpConfig struct {
	Addr         string
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	IdleTimeout  time.Duration
}

func newRouter(signer *auth.ContextTokenSigner, verifier auth.TokenVerifier, pool *db.Pool) *chi.Mux {
	r := chi.NewRouter()
	r.Use(chimiddleware.RequestID)
	r.Use(chimiddleware.RealIP)
	r.Use(chimiddleware.Recoverer)
	r.Use(chimiddleware.StripSlashes)

	isDev := envOr("APP_ENV", "") == "dev"

	// In dev, allow cross-origin requests from the local admin panel.
	if isDev {
		r.Use(devCORSMiddleware)
	}

	// Auth middleware is applied to every path except /healthz and /dev/*.
	// The middleware accepts both platform CTX tokens and Keycloak JWTs,
	// so identity pre-context endpoints work without a separate auth chain.
	authMW := auth.Middleware(verifier, signer)
	r.Use(func(next http.Handler) http.Handler {
		protected := authMW(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p := r.URL.Path
			if p == "/healthz" || (isDev && strings.HasPrefix(p, "/dev/")) {
				next.ServeHTTP(w, r)
				return
			}
			protected.ServeHTTP(w, r)
		})
	})

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	if isDev {
		r.Post("/dev/login", devLoginHandler(pool, signer))
	}

	return r
}

// devCORSMiddleware sets permissive CORS headers for local development only.
// Never include this in production builds.
func devCORSMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,PATCH,DELETE,OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization,Content-Type,X-Request-Id,Idempotency-Key")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type devLoginReq struct {
	Email string `json:"email"`
}

type devLoginResp struct {
	Token    string           `json:"token"`
	TenantID string           `json:"tenant_id"`
	User     devLoginRespUser `json:"user"`
}

type devLoginRespUser struct {
	ID       string `json:"id"`
	FullName string `json:"full_name"`
	Email    string `json:"email"`
}

// devLoginHandler issues a CTX staff token for any active membership of the
// given email address. Only registered when APP_ENV=dev.
func devLoginHandler(pool *db.Pool, signer *auth.ContextTokenSigner) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req devLoginReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Email == "" {
			http.Error(w, `{"error":"email required"}`, http.StatusBadRequest)
			return
		}

		ctx := r.Context()

		var personID uuid.UUID
		var fullName, email string

		err := pool.WithAllTenantsReadTx(ctx, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx,
				`SELECT id, full_name, email FROM persons WHERE email = $1`, req.Email,
			).Scan(&personID, &fullName, &email)
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				http.Error(w, `{"error":"person not found"}`, http.StatusUnauthorized)
				return
			}
			http.Error(w, `{"error":"db error"}`, http.StatusInternalServerError)
			return
		}

		// Find the first active membership (cross-tenant via the explicit
		// all-tenants scope; dev-only handler).
		var tenantID uuid.UUID
		var branchID uuid.UUID // uuid.Nil means chain-wide
		var membershipFound bool

		err = pool.WithAllTenantsReadTx(ctx, func(tx pgx.Tx) error {
			var rawBranchID *uuid.UUID
			err := tx.QueryRow(ctx,
				`SELECT tenant_id, branch_id FROM memberships
				 WHERE person_id = $1 AND status = 'active'
				 ORDER BY created_at LIMIT 1`,
				personID,
			).Scan(&tenantID, &rawBranchID)
			if err != nil {
				return err
			}
			if rawBranchID != nil {
				branchID = *rawBranchID
			}
			membershipFound = true
			return nil
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				http.Error(w, `{"error":"no active membership"}`, http.StatusUnauthorized)
				return
			}
			http.Error(w, `{"error":"db error"}`, http.StatusInternalServerError)
			return
		}
		if !membershipFound {
			http.Error(w, `{"error":"no active membership"}`, http.StatusUnauthorized)
			return
		}

		var roleIDs []uuid.UUID
		err = pool.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
			rows, err := tx.Query(ctx,
				`SELECT role_id FROM memberships
				 WHERE tenant_id = $1 AND person_id = $2 AND status = 'active'
				   AND (branch_id = $3 OR branch_id IS NULL)`,
				tenantID, personID, branchID,
			)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var id uuid.UUID
				if err := rows.Scan(&id); err != nil {
					return err
				}
				roleIDs = append(roleIDs, id)
			}
			return rows.Err()
		})
		if err != nil {
			http.Error(w, `{"error":"db error"}`, http.StatusInternalServerError)
			return
		}

		token, err := signer.IssueStaff(personID, tenantID, branchID, roleIDs)
		if err != nil {
			http.Error(w, `{"error":"token issue failed"}`, http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(devLoginResp{
			Token:    token,
			TenantID: tenantID.String(),
			User:     devLoginRespUser{ID: personID.String(), FullName: fullName, Email: email},
		})
	}
}

// webhookTracingFilter reports whether otelhttp should create a span for r.
// otelhttp records the raw, unrouted request path as a span attribute before
// chi even sees it, and the TokenX webhook's authenticity secret lives in
// that path (see payment/http.WebhookPathPrefix — the vendor supports no
// webhook signature, so the unguessable path segment is the only credential).
// Excluding the route from tracing is the only way to keep the secret out of
// every exported span.
func webhookTracingFilter(r *http.Request) bool {
	return !strings.HasPrefix(r.URL.Path, paymenthttp.WebhookPathPrefix)
}

// tracedHandler wraps router with the otelhttp instrumentation used by the
// production server, excluding routes that must not have their path recorded
// as a span attribute. Extra opts are appended for tests (e.g. a fixed
// TracerProvider so spans can be inspected without the global one).
func tracedHandler(router http.Handler, opts ...otelhttp.Option) http.Handler {
	return otelhttp.NewHandler(router, "api", append([]otelhttp.Option{otelhttp.WithFilter(webhookTracingFilter)}, opts...)...)
}

func registerHTTPServer(lc fx.Lifecycle, cfg httpConfig, router *chi.Mux, logger *zap.Logger) {
	srv := &http.Server{
		Addr:         cfg.Addr,
		Handler:      tracedHandler(router),
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		IdleTimeout:  cfg.IdleTimeout,
	}

	lc.Append(fx.Hook{
		OnStart: func(_ context.Context) error {
			go func() {
				logger.Info("http server listening", zap.String("addr", cfg.Addr))
				if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
					logger.Error("http server error", zap.Error(err))
				}
			}()
			return nil
		},
		OnStop: func(ctx context.Context) error {
			logger.Info("http server draining connections")
			if err := srv.Shutdown(ctx); err != nil {
				return fmt.Errorf("api: http shutdown: %w", err)
			}
			logger.Info("http server stopped")
			return nil
		},
	})
}

func newLogger() (*zap.Logger, error) {
	logger, err := zap.NewProduction()
	if err != nil {
		return nil, fmt.Errorf("api: build logger: %w", err)
	}
	return logger, nil
}

// Config provider functions below are the only places os.Getenv is called.
// Module code must not call os.Getenv — config flows through fx.In structs.

func newDBConfig() db.Config {
	return db.Config{
		DSN:             mustEnv("DATABASE_URL"),
		MaxConns:        20,
		MinConns:        2,
		MaxConnLifetime: 30 * time.Minute,
		MaxConnIdleTime: 5 * time.Minute,
	}
}

func newEventBusConfig() eventbus.Config {
	return eventbus.Config{
		URL:        mustEnv("NATS_URL"),
		StreamName: "DOMAIN_EVENTS",
		Subjects:   []string{"tenant.>", "identity.>", "pos.>", "payment.>", "inventory.>"},
	}
}

func newOTelConfig() platformotel.Config {
	return platformotel.Config{
		Endpoint:       envOr("OTEL_EXPORTER_OTLP_ENDPOINT", "localhost:4317"),
		ServiceName:    "onlinemenu-api",
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
		PoolSize: 10,
	}
}

func newOPAConfig() auth.EngineConfig {
	return auth.EngineConfig{
		BundlePath: envOr("OPA_BUNDLE_PATH", "configs/opa/bundles"),
	}
}

func newHTTPConfig() httpConfig {
	return httpConfig{
		Addr:         envOr("HTTP_ADDR", ":8080"),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}
}

// newPosWSConfig wires deployment-specific kitchen-WS settings.
// POS_WS_ALLOWED_ORIGINS: comma-separated origin patterns (host[:port]);
// empty keeps the strict same-origin default.
func newPosWSConfig() posws.Config {
	raw := strings.TrimSpace(envOr("POS_WS_ALLOWED_ORIGINS", ""))
	if raw == "" {
		return posws.Config{}
	}
	parts := strings.Split(raw, ",")
	patterns := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			patterns = append(patterns, p)
		}
	}
	return posws.Config{AllowedOriginPatterns: patterns}
}

// newFiscalConfig selects the fiscal device adapter (ADR-FISCAL-002).
// FISCAL_DEVICE_TYPE defaults to mock for dev/CI; production deployments set
// beko_x30tr_cloud plus the TOKENX_* credentials. The Token client secret will
// move to Vault dynamic secrets together with the other runtime credentials.
func newFiscalConfig() payment.FiscalConfig {
	cfg := payment.FiscalConfig{
		DeviceType:    envOr("FISCAL_DEVICE_TYPE", "mock"),
		WebhookSecret: envOr("TOKENX_WEBHOOK_SECRET", ""),
	}
	if cfg.DeviceType == tokenx.DeviceType {
		cfg.TokenX = tokenx.Config{
			APIURL:                mustEnv("TOKENX_API_URL"),
			AuthURL:               mustEnv("TOKENX_AUTH_URL"),
			ClientID:              mustEnv("TOKENX_CLIENT_ID"),
			ClientSecret:          mustEnv("TOKENX_CLIENT_SECRET"),
			BasketMode:            tokenx.BasketMode(envOr("TOKENX_BASKET_MODE", "instant")),
			DefaultTerminalSerial: envOr("TOKENX_DEFAULT_TERMINAL", ""),
		}
	}
	return cfg
}

func newOutboxConfig() outbox.Config {
	return outbox.Config{
		DSN:          envOr("OUTBOX_MIGRATOR_DSN", ""),
		PollInterval: 2 * time.Second,
		BatchSize:    100,
		MaxRetries:   10,
		// The monolith serves every module's outbox; partially-migrated dev
		// environments are handled by the dispatcher's missing-table disable.
		Tables: []outbox.TableSpec{
			{Table: "pos_outbox", Module: "pos"},
			{Table: "payment_outbox", Module: "payment"},
			{Table: "billing_outbox", Module: "billing"},
		},
	}
}

func newContextTokenSigner() (*auth.ContextTokenSigner, error) {
	secret := envOr("CTX_TOKEN_SECRET", "")
	if secret == "" {
		return nil, errors.New("api: CTX_TOKEN_SECRET env var is required")
	}
	return auth.NewContextTokenSigner([]byte(secret))
}

// devTokenVerifier parses JWT claims without signature verification.
// ONLY active when APP_ENV=dev. Returns an error on any other environment
// so it cannot accidentally reach production.
type devTokenVerifier struct{}

func newTokenVerifier() (auth.TokenVerifier, error) {
	env := envOr("APP_ENV", "")
	if env == "dev" {
		return devTokenVerifier{}, nil
	}

	issuerURL := envOr("KEYCLOAK_ISSUER_URL", "")
	audience := envOr("KEYCLOAK_AUDIENCE", "")
	if issuerURL == "" || audience == "" {
		return nil, fmt.Errorf("api: KEYCLOAK_ISSUER_URL and KEYCLOAK_AUDIENCE are required when APP_ENV=%q", env)
	}

	// A bounded background context is used for the initial JWKS fetch: fx does not
	// inject context.Context, and startup must fail fast rather than hang forever
	// if Keycloak is unreachable.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	verifier, err := auth.NewKeycloakVerifier(ctx, auth.KeycloakVerifierConfig{
		IssuerURL: issuerURL,
		JWKSURL:   envOr("KEYCLOAK_JWKS_URL", ""),
		Audience:  audience,
	})
	if err != nil {
		return nil, fmt.Errorf("api: build keycloak verifier: %w", err)
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

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		fmt.Fprintf(os.Stderr, "api: required env var %q is not set\n", key)
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
