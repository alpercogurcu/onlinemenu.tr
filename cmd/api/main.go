package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.uber.org/fx"
	"go.uber.org/fx/fxevent"
	"go.uber.org/zap"

	"onlinemenu.tr/internal/modules/identity"
	"onlinemenu.tr/internal/modules/tenant"
	"onlinemenu.tr/internal/platform/auth"
	"onlinemenu.tr/internal/platform/cache"
	"onlinemenu.tr/internal/platform/db"
	"onlinemenu.tr/internal/platform/eventbus"
	platformotel "onlinemenu.tr/internal/platform/otel"
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

		db.Module,
		eventbus.Module,
		platformotel.Module,
		vault.Module,
		cache.Module,
		fx.Provide(auth.NewEngine),

		// Domain modules
		identity.Module,
		tenant.Module,

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

func newRouter() *chi.Mux {
	r := chi.NewRouter()
	r.Use(chimiddleware.RequestID)
	r.Use(chimiddleware.RealIP)
	r.Use(chimiddleware.Recoverer)
	r.Use(chimiddleware.StripSlashes)

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	return r
}

func registerHTTPServer(lc fx.Lifecycle, cfg httpConfig, router *chi.Mux, logger *zap.Logger) {
	srv := &http.Server{
		Addr:         cfg.Addr,
		Handler:      otelhttp.NewHandler(router, "api"),
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
		Subjects:   []string{"tenant.>", "identity.>", "pos.>", "inventory.>"},
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
