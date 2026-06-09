// Command edge is the local branch server that runs on-premises hardware.
// It provides offline-capable POS operations via SQLite and syncs with the
// cloud API when connectivity is available (ADR-DATA-004, offline-sync.md).
//
// Phase 1: chi HTTP server + SQLite schema + outbox writer + heartbeat state machine.
// Phase 2: embedded NATS, bidirectional cloud sync, catalog delta apply.
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
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"onlinemenu.tr/internal/modules/edge-sync/outbox"
	edgesync "onlinemenu.tr/internal/modules/edge-sync/sync"
	"onlinemenu.tr/internal/platform/edgedb"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	logger, err := zap.NewProduction()
	if err != nil {
		fmt.Fprintf(os.Stderr, "edge: build logger: %v\n", err)
		os.Exit(1)
	}
	defer logger.Sync() //nolint:errcheck

	// ── SQLite ───────────────────────────────────────────────────────────────
	dbPath := envOr("EDGE_DB_PATH", "edge.db")
	edb, err := edgedb.Open(dbPath)
	if err != nil {
		logger.Fatal("edge: open sqlite", zap.Error(err))
	}
	defer edb.Close()

	if err := edb.Migrate(ctx); err != nil {
		logger.Fatal("edge: migrate schema", zap.Error(err))
	}
	logger.Info("edge: sqlite ready", zap.String("path", dbPath))

	// ── Workers ───────────────────────────────────────────────────────────────
	outboxWriter := outbox.NewWriter(edb.SQL())

	hbCfg := edgesync.HeartbeatConfig{
		CloudHealthURL: envOr("CLOUD_HEALTH_URL", ""),
		Interval:       30 * time.Second,
		DegradedAfter:  500 * time.Millisecond,
		OfflineAfter:   3,
	}
	// Heartbeat logs state transitions internally; no additional notify needed.
	hb := edgesync.NewHeartbeat(hbCfg, logger, nil)

	engine := edgesync.NewEngine(outboxWriter, hb, logger)

	// ── HTTP ─────────────────────────────────────────────────────────────────
	r := chi.NewRouter()
	r.Use(chimiddleware.RequestID)
	r.Use(chimiddleware.Recoverer)

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	r.Get("/edge/state", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"state":%q}`, hb.State().String())
	})

	addr := envOr("HTTP_ADDR", ":8082")
	srv := &http.Server{
		Addr:         addr,
		Handler:      r,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// ── Run ──────────────────────────────────────────────────────────────────
	g, gCtx := errgroup.WithContext(ctx)

	g.Go(func() error {
		logger.Info("edge: HTTP server starting", zap.String("addr", addr))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			return fmt.Errorf("edge: http: %w", err)
		}
		return nil
	})

	g.Go(func() error {
		hb.Run(gCtx)
		return nil
	})

	g.Go(func() error {
		engine.Run(gCtx)
		return nil
	})

	// Shutdown trigger: when context cancels, drain HTTP then return.
	g.Go(func() error {
		<-gCtx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		logger.Info("edge: graceful shutdown initiated")
		return srv.Shutdown(shutdownCtx)
	})

	if err := g.Wait(); err != nil {
		logger.Error("edge: exit with error", zap.Error(err))
		os.Exit(1)
	}
	logger.Info("edge: stopped cleanly")
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
