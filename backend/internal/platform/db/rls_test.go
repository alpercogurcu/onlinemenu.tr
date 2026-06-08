package db

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"go.uber.org/goleak"
)

// sharedCtr is the postgres container shared across all RLS tests.
// Initialised once in TestMain; never in per-test helpers.
var (
	sharedCtr    *tcpostgres.PostgresContainer
	runtimePool  *Pool // app_runtime — subject to RLS
	migratorPool *Pool // app_migrator — FORCE RLS test (no matching policy → 0 rows)
)

func TestMain(m *testing.M) {
	ctx := context.Background()

	ctr, err := tcpostgres.Run(ctx,
		"pgvector/pgvector:pg17",
		tcpostgres.WithDatabase("testdb"),
		tcpostgres.WithUsername("postgres"),
		tcpostgres.WithPassword("postgres"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "start postgres container: %v\n", err)
		os.Exit(1)
	}

	superDSN, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fmt.Fprintf(os.Stderr, "get connection string: %v\n", err)
		_ = ctr.Terminate(ctx)
		os.Exit(1)
	}

	bootstrapSchemaMain(ctx, superDSN)

	runtimePool = newBarePoolMain(ctx, superDSN, "app_runtime", "runtime_secret")
	migratorPool = newBarePoolMain(ctx, superDSN, "app_migrator", "migrator_secret")
	sharedCtr = ctr

	rc := m.Run()

	// Teardown before goroutine-leak check so testcontainers goroutines exit.
	migratorPool.inner.Close()
	runtimePool.inner.Close()
	_ = ctr.Terminate(ctx)

	if err := goleak.Find(
		goleak.IgnoreTopFunction("github.com/testcontainers/testcontainers-go.(*DockerContainer).followOutput"),
		goleak.IgnoreTopFunction("github.com/testcontainers/testcontainers-go.(*DockerContainer).tailOrFollowOutput"),
	); err != nil {
		fmt.Fprintf(os.Stderr, "goleak: %v\n", err)
		rc = 1
	}

	os.Exit(rc)
}

// bootstrapSchemaMain is the TestMain-level variant that uses fmt.Fprintf+os.Exit
// instead of t.Fatal (unavailable outside a test).
func bootstrapSchemaMain(ctx context.Context, superDSN string) {
	conn, err := pgx.Connect(ctx, superDSN)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bootstrap: superuser connect: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close(ctx)

	stmts := bootstrapStmts()
	for _, stmt := range stmts {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			fmt.Fprintf(os.Stderr, "bootstrap stmt failed: %s\n  error: %v\n", truncate(stmt, 80), err)
			os.Exit(1)
		}
	}
}

// newBarePoolMain creates a *Pool (no fx lifecycle) for tests.
// It accepts a base DSN plus explicit user and password so it can override the
// superuser credentials without going through a ConnString round-trip (which
// would discard the mutations — ConnString() returns the original parsed string).
func newBarePoolMain(ctx context.Context, baseDSN, user, password string) *Pool {
	cfg, err := pgxpool.ParseConfig(baseDSN)
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse pool config: %v\n", err)
		os.Exit(1)
	}
	// Mutate the struct fields directly; never call ConnString() after mutation.
	cfg.ConnConfig.User = user
	cfg.ConnConfig.Password = password
	cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	cfg.MaxConns = 10

	inner, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create pool (%s): %v\n", user, err)
		os.Exit(1)
	}
	if err := inner.Ping(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "ping pool (%s): %v\n", user, err)
		os.Exit(1)
	}
	return &Pool{inner: inner}
}

// startContainer is kept for backward compatibility; it is now a no-op
// because the container is always started in TestMain.
func startContainer(t *testing.T) {
	t.Helper()
	if sharedCtr == nil {
		t.Fatal("postgres container not initialised — TestMain setup failed")
	}
}

// bootstrapStmts returns the SQL statements needed to set up the test schema.
func bootstrapStmts() []string {
	return []string{
		`DO $$ BEGIN
			CREATE ROLE app_migrator LOGIN PASSWORD 'migrator_secret';
		EXCEPTION WHEN duplicate_object THEN NULL; END $$`,

		`DO $$ BEGIN
			CREATE ROLE app_runtime LOGIN PASSWORD 'runtime_secret' NOINHERIT;
		EXCEPTION WHEN duplicate_object THEN NULL; END $$`,

		`GRANT USAGE ON SCHEMA public TO app_migrator, app_runtime`,

		`CREATE TABLE IF NOT EXISTS test_items (
			id        UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			tenant_id UUID NOT NULL,
			name      TEXT NOT NULL
		)`,

		`ALTER TABLE test_items ENABLE ROW LEVEL SECURITY`,
		`ALTER TABLE test_items FORCE ROW LEVEL SECURITY`,

		`DROP POLICY IF EXISTS item_isolation ON test_items`,

		// NULLIF converts empty-string to NULL (PostgreSQL returns '' for unset custom GUC,
		// not NULL, after a pooled connection resets LOCAL state post-commit).
		// NULL::uuid → NULL; tenant_id = NULL is NULL (falsy) → row hidden.
		`CREATE POLICY item_isolation ON test_items
			FOR ALL TO app_runtime
			USING  (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
			WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)`,

		`GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE test_items TO app_runtime`,
		`GRANT SELECT ON TABLE test_items TO app_migrator`,
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func insertItem(t *testing.T, ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, name string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := tx.QueryRow(ctx,
		`INSERT INTO test_items (tenant_id, name) VALUES ($1, $2) RETURNING id`,
		tenantID, name,
	).Scan(&id)
	require.NoErrorf(t, err, "insertItem %s", name)
	return id
}

func countItems(t *testing.T, ctx context.Context, tx pgx.Tx) int {
	t.Helper()
	var n int
	require.NoError(t, tx.QueryRow(ctx, `SELECT COUNT(*) FROM test_items`).Scan(&n))
	return n
}

func selectByID(t *testing.T, ctx context.Context, tx pgx.Tx, id uuid.UUID) int {
	t.Helper()
	var n int
	require.NoError(t,
		tx.QueryRow(ctx, `SELECT COUNT(*) FROM test_items WHERE id = $1`, id).Scan(&n),
	)
	return n
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestRLSIsolation verifies that tenants see only their own rows and cannot
// reach rows belonging to other tenants via a direct ID lookup.
func TestRLSIsolation(t *testing.T) {
	startContainer(t)

	ctx := context.Background()
	tenantA := uuid.New()
	tenantB := uuid.New()

	// Insert 3 rows for tenantA.
	require.NoError(t, runtimePool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		for i := range 3 {
			insertItem(t, ctx, tx, tenantA, fmt.Sprintf("a-%d", i))
		}
		return nil
	}))

	// Insert 2 rows for tenantB.
	require.NoError(t, runtimePool.WithTenantTx(ctx, tenantB, func(tx pgx.Tx) error {
		for i := range 2 {
			insertItem(t, ctx, tx, tenantB, fmt.Sprintf("b-%d", i))
		}
		return nil
	}))

	// tenantA must see exactly 3 rows.
	require.NoError(t, runtimePool.WithTenantReadTx(ctx, tenantA, func(tx pgx.Tx) error {
		assert.Equal(t, 3, countItems(t, ctx, tx), "tenantA row count")
		return nil
	}))

	// tenantB must see exactly 2 rows.
	require.NoError(t, runtimePool.WithTenantReadTx(ctx, tenantB, func(tx pgx.Tx) error {
		assert.Equal(t, 2, countItems(t, ctx, tx), "tenantB row count")
		return nil
	}))

	// Cross-tenant: capture one of tenantB's row IDs, then look it up as tenantA.
	var tenantBRowID uuid.UUID
	require.NoError(t, runtimePool.WithTenantReadTx(ctx, tenantB, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id FROM test_items LIMIT 1`).Scan(&tenantBRowID)
	}))

	require.NoError(t, runtimePool.WithTenantReadTx(ctx, tenantA, func(tx pgx.Tx) error {
		assert.Equal(t, 0, selectByID(t, ctx, tx, tenantBRowID),
			"cross-tenant: tenantA must not see tenantB row by ID")
		return nil
	}))
}

// TestRLSInsertOwnTenantOnly verifies that the WITH CHECK clause prevents
// inserting a row whose tenant_id differs from the active RLS context.
func TestRLSInsertOwnTenantOnly(t *testing.T) {
	startContainer(t)

	ctx := context.Background()
	tenantA := uuid.New()
	tenantB := uuid.New()

	err := runtimePool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		_, execErr := tx.Exec(ctx,
			`INSERT INTO test_items (tenant_id, name) VALUES ($1, $2)`,
			tenantB, "cross-tenant-insert-attempt",
		)
		return execErr
	})

	require.Error(t, err, "WITH CHECK must reject inserting a row with a foreign tenant_id")
}

// TestRLSWithoutTenantContext verifies that when SET LOCAL is skipped
// (app.tenant_id GUC absent), no rows are visible because the policy
// evaluates to false (NULL = NULL is false in SQL).
func TestRLSWithoutTenantContext(t *testing.T) {
	startContainer(t)

	ctx := context.Background()

	// Ensure at least one row exists.
	seed := uuid.New()
	require.NoError(t, runtimePool.WithTenantTx(ctx, seed, func(tx pgx.Tx) error {
		insertItem(t, ctx, tx, seed, "seed-no-ctx")
		return nil
	}))

	// Acquire a raw connection without calling SET LOCAL.
	conn, err := runtimePool.inner.Acquire(ctx)
	require.NoError(t, err)
	defer conn.Release()

	var n int
	require.NoError(t, conn.QueryRow(ctx, `SELECT COUNT(*) FROM test_items`).Scan(&n))
	assert.Equal(t, 0, n, "without SET LOCAL, NULL tenant context must yield 0 rows")
}

// TestRLSForceRLSBypassAttempt verifies FORCE ROW LEVEL SECURITY behaviour:
//
//   - app_runtime without SET LOCAL → 0 rows (no GUC, policy false)
//   - app_migrator without SET LOCAL → 0 rows (FORCE RLS; no matching policy
//     means default-deny applies even to the table owner)
func TestRLSForceRLSBypassAttempt(t *testing.T) {
	startContainer(t)

	ctx := context.Background()

	// Seed a row via the runtime pool.
	tenant := uuid.New()
	require.NoError(t, runtimePool.WithTenantTx(ctx, tenant, func(tx pgx.Tx) error {
		insertItem(t, ctx, tx, tenant, "seed-force-rls")
		return nil
	}))

	t.Run("runtime_role_no_context", func(t *testing.T) {
		conn, err := runtimePool.inner.Acquire(ctx)
		require.NoError(t, err)
		defer conn.Release()

		var n int
		require.NoError(t, conn.QueryRow(ctx, `SELECT COUNT(*) FROM test_items`).Scan(&n))
		assert.Equal(t, 0, n, "app_runtime without SET LOCAL must see 0 rows")
	})

	t.Run("migrator_role_force_rls", func(t *testing.T) {
		// item_isolation policy is FOR ALL TO app_runtime only.
		// With FORCE ROW LEVEL SECURITY and no applicable policy, app_migrator
		// is subject to default-deny and sees 0 rows.
		conn, err := migratorPool.inner.Acquire(ctx)
		require.NoError(t, err)
		defer conn.Release()

		var n int
		require.NoError(t, conn.QueryRow(ctx, `SELECT COUNT(*) FROM test_items`).Scan(&n))
		assert.Equal(t, 0, n, "FORCE RLS: app_migrator with no matching policy must see 0 rows")
	})
}

// TestRLSConcurrent verifies that 10 concurrent tenant transactions do not
// bleed rows across tenant boundaries under parallel execution.
func TestRLSConcurrent(t *testing.T) {
	t.Parallel()
	startContainer(t)

	ctx := context.Background()

	const workers = 10
	tenantIDs := make([]uuid.UUID, workers)
	insertedIDs := make([]uuid.UUID, workers)

	// Sequential setup: one row per tenant.
	for i := range workers {
		tenantIDs[i] = uuid.New()
		err := runtimePool.WithTenantTx(ctx, tenantIDs[i], func(tx pgx.Tx) error {
			insertedIDs[i] = insertItem(t, ctx, tx, tenantIDs[i], fmt.Sprintf("concurrent-%d", i))
			return nil
		})
		require.NoErrorf(t, err, "setup insert for tenant %d", i)
	}

	// Concurrent reads: each goroutine must see exactly its own 1 row.
	var wg sync.WaitGroup
	readErrs := make([]error, workers)
	counts := make([]int, workers)

	for i := range workers {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			readErrs[idx] = runtimePool.WithTenantReadTx(ctx, tenantIDs[idx], func(tx pgx.Tx) error {
				var n int
				if err := tx.QueryRow(ctx,
					`SELECT COUNT(*) FROM test_items WHERE tenant_id = $1`,
					tenantIDs[idx],
				).Scan(&n); err != nil {
					return fmt.Errorf("count tenant %d: %w", idx, err)
				}
				counts[idx] = n
				return nil
			})
		}(i)
	}

	wg.Wait()

	for i := range workers {
		require.NoErrorf(t, readErrs[i], "read error for tenant %d", i)
		assert.Equalf(t, 1, counts[i], "tenant %d must see exactly 1 row", i)
	}

	// Cross-tenant check: each tenant must not see its neighbour's row.
	for i := range workers {
		neighbour := (i + 1) % workers
		neighbourID := insertedIDs[neighbour]

		err := runtimePool.WithTenantReadTx(ctx, tenantIDs[i], func(tx pgx.Tx) error {
			n := selectByID(t, ctx, tx, neighbourID)
			assert.Equalf(t, 0, n,
				"tenant %d must not see tenant %d row %s", i, neighbour, neighbourID)
			return nil
		})
		require.NoErrorf(t, err, "cross-tenant check for tenant %d → %d", i, neighbour)
	}
}
