// Package db provides the PostgreSQL connection pool configured for pgBouncer
// transaction-mode compatibility and multi-tenant RLS enforcement.
package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

// Config holds database connection parameters supplied via fx.Provide.
type Config struct {
	DSN             string
	MaxConns        int32
	MinConns        int32
	MaxConnLifetime time.Duration
	MaxConnIdleTime time.Duration
}

// Pool wraps pgxpool.Pool to prevent direct query execution from module code.
// All queries must go through WithTenantTx to ensure RLS enforcement.
type Pool struct {
	inner *pgxpool.Pool
}

// Module registers the DB pool with fx lifecycle.
var Module = fx.Module("db",
	fx.Provide(NewPool),
)

// NewPool constructs and validates a pgxpool.Pool with pgBouncer-compatible settings.
func NewPool(lc fx.Lifecycle, cfg Config, log *zap.Logger) (*Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("db: parse dsn: %w", err)
	}

	// SimpleProtocol is required for pgBouncer transaction-mode compatibility.
	// Named prepared statements are not supported in transaction-mode pgBouncer.
	poolCfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol

	if cfg.MaxConns > 0 {
		poolCfg.MaxConns = cfg.MaxConns
	}
	if cfg.MinConns > 0 {
		poolCfg.MinConns = cfg.MinConns
	}
	if cfg.MaxConnLifetime > 0 {
		poolCfg.MaxConnLifetime = cfg.MaxConnLifetime
	}
	if cfg.MaxConnIdleTime > 0 {
		poolCfg.MaxConnIdleTime = cfg.MaxConnIdleTime
	}

	pool, err := pgxpool.NewWithConfig(context.Background(), poolCfg)
	if err != nil {
		return nil, fmt.Errorf("db: create pool: %w", err)
	}

	p := &Pool{inner: pool}

	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			if err := pool.Ping(ctx); err != nil {
				return fmt.Errorf("db: ping: %w", err)
			}
			log.Info("database pool connected")
			return nil
		},
		OnStop: func(ctx context.Context) error {
			pool.Close()
			log.Info("database pool closed")
			return nil
		},
	})

	return p, nil
}

// Inner returns the underlying pgxpool for use by platform-level infrastructure only.
// Module code must not call this — use WithTenantTx instead.
func (p *Pool) Inner() *pgxpool.Pool {
	return p.inner
}

// NewPoolFromConfig constructs a Pool directly from a pgxpool.Config.
// Intended for integration tests that need to override user/password after parsing.
// Production code must use NewPool via fx.
func NewPoolFromConfig(ctx context.Context, cfg *pgxpool.Config) (*Pool, error) {
	inner, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("db: create pool from config: %w", err)
	}
	if err := inner.Ping(ctx); err != nil {
		inner.Close()
		return nil, fmt.Errorf("db: ping pool: %w", err)
	}
	return &Pool{inner: inner}, nil
}

// Close closes the underlying connection pool. Use in tests and graceful shutdown.
func (p *Pool) Close() {
	p.inner.Close()
}
