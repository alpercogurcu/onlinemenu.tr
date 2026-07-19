package db

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

// newSmallPool creates a *Pool with an explicit, small MaxConns against the
// shared test container's app_runtime role. A tiny pool makes a leaked
// connection observable directly: if a prior call failed to release its
// connection, a subsequent call blocks on Acquire instead of proceeding.
//
// This intentionally duplicates (rather than reuses/modifies) newBarePoolMain
// from rls_test.go: newBarePoolMain hardcodes MaxConns=10 for the shared
// package-level runtimePool/migratorPool used by every other test in this
// package, and this test needs a pool that is not shared with them.
func newSmallPool(t *testing.T, ctx context.Context, maxConns int32) *Pool {
	t.Helper()

	connStr, err := sharedCtr.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	cfg, err := pgxpool.ParseConfig(connStr)
	require.NoError(t, err)
	cfg.ConnConfig.User = "app_runtime"
	cfg.ConnConfig.Password = "runtime_secret"
	cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	cfg.MaxConns = maxConns

	inner, err := pgxpool.NewWithConfig(ctx, cfg)
	require.NoError(t, err)
	require.NoError(t, inner.Ping(ctx))

	t.Cleanup(inner.Close)
	return &Pool{inner: inner}
}

// TestWithTenantTxPanicReleasesConnection is a regression test for a
// connection leak: previously, WithTenantTx/WithTenantReadTx/WithAllTenantsTx/
// WithAllTenantsReadTx only rolled back the transaction on the branch where fn
// returned a non-nil error. A panic inside fn skipped straight past that
// rollback and past tx.Commit, leaving the transaction — and therefore its
// underlying connection — open server-side ("idle in transaction") and never
// returned to the pool.
//
// The fix defers tx.Rollback(ctx) immediately after a successful BeginTx.
// Per the Go spec, deferred functions run both on a normal/panicking return
// AND during runtime.Goexit unwinding, so this same deferred Rollback also
// covers the runtime.Goexit case (e.g. testify's require.* helpers called
// inside fn on a test goroutine) — see
// TestWithTenantTxGoexitReleasesConnection below, which reproduces that exact
// trigger directly rather than relying on the panic path as a proxy for it.
//
// A MaxConns=1 pool makes the leak observable mechanically: if the panicking
// call's connection was not released, the second WithTenantTx call below can
// never Acquire and the test times out instead of merely leaving behind a
// human-invisible idle-in-transaction row.
func TestWithTenantTxPanicReleasesConnection(t *testing.T) {
	startContainer(t)
	ctx := context.Background()
	tenant := uuid.New()

	pool := newSmallPool(t, ctx, 1)

	panicked := func() (didPanic bool) {
		defer func() {
			if r := recover(); r != nil {
				didPanic = true
			}
		}()
		_ = pool.WithTenantTx(ctx, tenant, func(pgx.Tx) error {
			panic("boom: simulated fn panic")
		})
		return false
	}()
	require.True(t, panicked, "panic inside fn must propagate to the caller, not be swallowed")

	// Bounded context: if the pool leaked its only connection above, Acquire
	// inside WithTenantTx blocks until this deadline instead of the pool
	// simply being unavailable forever (which would hang the test suite).
	acquireCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	err := pool.WithTenantTx(acquireCtx, tenant, func(tx pgx.Tx) error {
		insertItem(t, acquireCtx, tx, tenant, "post-panic")
		return nil
	})
	require.NoError(t, err, "connection from the panicking call must have been released back to the pool")
}

// TestWithTenantReadTxPanicReleasesConnection mirrors
// TestWithTenantTxPanicReleasesConnection for the read-only variant, which
// has its own independent defer site in tenant_tx.go.
func TestWithTenantReadTxPanicReleasesConnection(t *testing.T) {
	startContainer(t)
	ctx := context.Background()
	tenant := uuid.New()

	// Seed one row (via the shared pool) so the post-panic read has something
	// to observe, independent of the small pool under test.
	require.NoError(t, runtimePool.WithTenantTx(ctx, tenant, func(tx pgx.Tx) error {
		insertItem(t, ctx, tx, tenant, "seed-read-panic")
		return nil
	}))

	pool := newSmallPool(t, ctx, 1)

	panicked := func() (didPanic bool) {
		defer func() {
			if r := recover(); r != nil {
				didPanic = true
			}
		}()
		_ = pool.WithTenantReadTx(ctx, tenant, func(pgx.Tx) error {
			panic("boom: simulated fn panic (read)")
		})
		return false
	}()
	require.True(t, panicked, "panic inside fn must propagate to the caller, not be swallowed")

	acquireCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	err := pool.WithTenantReadTx(acquireCtx, tenant, func(tx pgx.Tx) error {
		n := countItems(t, acquireCtx, tx)
		require.GreaterOrEqual(t, n, 1)
		return nil
	})
	require.NoError(t, err, "connection from the panicking call must have been released back to the pool")
}

// TestWithTenantTxGoexitReleasesConnection reproduces the actual trigger
// observed in the wild: a call to testify's require.* (or any FailNow-based
// assertion) inside fn on a test goroutine, which internally calls
// runtime.Goexit rather than panicking. Per the Go spec, Goexit runs all
// deferred calls on the calling goroutine's stack before that goroutine
// exits, so the same defer tx.Rollback(ctx) fix that TestWithTenantTx*
// PanicReleasesConnection exercises also has to cover this path — this test
// proves it directly instead of treating the panic case as a stand-in for it.
//
// WithTenantTx must run on its own goroutine here: runtime.Goexit terminates
// only the calling goroutine, so calling it directly from the test's main
// goroutine would abort the whole test (and TestMain's m.Run) rather than
// simulate an isolated failing subtest.
func TestWithTenantTxGoexitReleasesConnection(t *testing.T) {
	startContainer(t)
	ctx := context.Background()
	tenant := uuid.New()

	pool := newSmallPool(t, ctx, 1)

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = pool.WithTenantTx(ctx, tenant, func(pgx.Tx) error {
			runtime.Goexit()
			return nil // unreachable
		})
	}()
	<-done

	acquireCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	err := pool.WithTenantTx(acquireCtx, tenant, func(tx pgx.Tx) error {
		insertItem(t, acquireCtx, tx, tenant, "post-goexit")
		return nil
	})
	require.NoError(t, err, "connection from the Goexit call must have been released back to the pool")
}
