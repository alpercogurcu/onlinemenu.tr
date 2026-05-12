// Command worker runs asynq background job processors.
// It handles scheduled tasks, outbox relay, and async operations
// that must not block the HTTP request lifecycle (ADR-ARCH-002).
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/hibiken/asynq"
	"go.uber.org/zap"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	logger, err := zap.NewProduction()
	if err != nil {
		fmt.Fprintf(os.Stderr, "worker: build logger: %v\n", err)
		os.Exit(1)
	}
	defer logger.Sync() //nolint:errcheck

	redisAddr := envOr("REDIS_ADDR", "localhost:6379")

	srv := asynq.NewServer(
		asynq.RedisClientOpt{Addr: redisAddr},
		asynq.Config{
			Concurrency: 20,
			Queues: map[string]int{
				"critical": 6,
				"default":  3,
				"low":      1,
			},
		},
	)

	mux := asynq.NewServeMux()
	// Register task handlers here as modules are implemented.

	logger.Info("asynq worker starting", zap.String("redis", redisAddr))

	errCh := make(chan error, 1)
	go func() {
		if err := srv.Run(mux); err != nil {
			errCh <- fmt.Errorf("worker: asynq run: %w", err)
		}
	}()

	select {
	case <-ctx.Done():
		logger.Info("worker: draining queue before shutdown")
		srv.Shutdown()
		logger.Info("worker stopped")
	case err := <-errCh:
		logger.Error("worker: fatal error", zap.Error(err))
		os.Exit(1)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
