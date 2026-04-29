// Package cache provides a Redis client wrapper for platform-level caching needs:
// OPA decision cache, idempotency store, and rate-limit counters.
package cache

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

// Config holds Redis connection settings injected via fx.
type Config struct {
	Addr     string
	Password string
	DB       int

	// PoolSize controls the number of socket connections kept in the pool.
	PoolSize int
}

// Module registers the Redis client with fx lifecycle.
var Module = fx.Module("cache",
	fx.Provide(NewClient),
)

// NewClient constructs and validates a Redis client connection.
func NewClient(lc fx.Lifecycle, cfg Config, logger *zap.Logger) (*redis.Client, error) {
	opts := &redis.Options{
		Addr:     cfg.Addr,
		Password: cfg.Password,
		DB:       cfg.DB,
	}
	if cfg.PoolSize > 0 {
		opts.PoolSize = cfg.PoolSize
	}

	client := redis.NewClient(opts)

	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			if err := client.Ping(ctx).Err(); err != nil {
				return fmt.Errorf("cache: redis ping: %w", err)
			}
			logger.Info("redis cache connected", zap.String("addr", cfg.Addr))
			return nil
		},
		OnStop: func(ctx context.Context) error {
			if err := client.Close(); err != nil {
				logger.Warn("cache: redis close error", zap.Error(err))
			}
			logger.Info("redis cache closed")
			return nil
		},
	})

	return client, nil
}
