package outbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"go.uber.org/goleak"
	"go.uber.org/zap"
)

var sharedPool *pgxpool.Pool

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

	if err := bootstrapRoles(ctx, superDSN); err != nil {
		fmt.Fprintf(os.Stderr, "bootstrap roles: %v\n", err)
		_ = ctr.Terminate(ctx)
		os.Exit(1)
	}

	if err := runMigrations(superDSN); err != nil {
		fmt.Fprintf(os.Stderr, "run migrations: %v\n", err)
		_ = ctr.Terminate(ctx)
		os.Exit(1)
	}

	// The dispatcher reads across all tenants using the BYPASSRLS migrator role,
	// exactly as it does in production (see Register in dispatcher.go).
	sharedPool = newBypassPool(ctx, superDSN, "app_migrator", "migrator_secret")

	rc := m.Run()

	sharedPool.Close()
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

// migrationsBase returns the absolute path to backend/migrations.
// File: .../backend/internal/platform/outbox/dispatcher_test.go
// Walk up 3 directories: outbox/ → platform/ → internal/ → backend/
func migrationsBase() string {
	_, file, _, _ := runtime.Caller(0)
	base := filepath.Dir(file)
	for range 3 {
		base = filepath.Dir(base)
	}
	return filepath.Join(base, "migrations")
}

func runMigrations(superDSN string) error {
	cfg, err := pgxpool.ParseConfig(superDSN)
	if err != nil {
		return fmt.Errorf("parse dsn: %w", err)
	}
	migratorDSN := fmt.Sprintf("pgx5://%s:%s@%s/%s?sslmode=disable",
		"app_migrator", "migrator_secret",
		cfg.ConnConfig.Host+fmt.Sprintf(":%d", cfg.ConnConfig.Port),
		cfg.ConnConfig.Database,
	)

	for _, mod := range []string{"tenant", "identity", "catalog", "pos", "payment", "party", "billing"} {
		absPath := filepath.Join(migrationsBase(), mod)
		src := fmt.Sprintf("file://%s", absPath)
		dsn := fmt.Sprintf("%s&x-migrations-table=schema_migrations_%s", migratorDSN, mod)

		m, err := migrate.New(src, dsn)
		if err != nil {
			return fmt.Errorf("migrate open %s: %w", mod, err)
		}
		if err := m.Up(); err != nil && err != migrate.ErrNoChange {
			m.Close()
			return fmt.Errorf("migrate up %s: %w", mod, err)
		}
		m.Close()
	}
	return nil
}

func bootstrapRoles(ctx context.Context, superDSN string) error {
	conn, err := pgx.Connect(ctx, superDSN)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer conn.Close(ctx)

	stmts := []string{
		`DO $$ BEGIN CREATE ROLE app_migrator LOGIN PASSWORD 'migrator_secret' BYPASSRLS;
		 EXCEPTION WHEN duplicate_object THEN NULL; END $$`,
		`DO $$ BEGIN CREATE ROLE app_runtime LOGIN PASSWORD 'runtime_secret' NOINHERIT;
		 EXCEPTION WHEN duplicate_object THEN NULL; END $$`,
		`GRANT USAGE ON SCHEMA public TO app_migrator, app_runtime`,
		`CREATE EXTENSION IF NOT EXISTS "uuid-ossp"`,
		`CREATE EXTENSION IF NOT EXISTS vector`,
		`ALTER SCHEMA public OWNER TO app_migrator`,
		`GRANT ALL ON SCHEMA public TO app_migrator`,
		`ALTER DEFAULT PRIVILEGES FOR ROLE app_migrator IN SCHEMA public
		 GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO app_runtime`,
		`ALTER DEFAULT PRIVILEGES FOR ROLE app_migrator IN SCHEMA public
		 GRANT USAGE ON SEQUENCES TO app_runtime`,
	}
	for _, s := range stmts {
		if _, err := conn.Exec(ctx, s); err != nil {
			end := len(s)
			if end > 60 {
				end = 60
			}
			return fmt.Errorf("stmt failed %q: %w", s[:end], err)
		}
	}
	return nil
}

func newBypassPool(ctx context.Context, baseDSN, user, password string) *pgxpool.Pool {
	cfg, err := pgxpool.ParseConfig(baseDSN)
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse pool config: %v\n", err)
		os.Exit(1)
	}
	cfg.ConnConfig.User = user
	cfg.ConnConfig.Password = password
	cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	cfg.MaxConns = 5

	p, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create pool (%s): %v\n", user, err)
		os.Exit(1)
	}
	return p
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// fakePublisher is a test double for msgPublisher recording every publish
// attempt and optionally failing or stalling to exercise timeout handling.
type fakePublisher struct {
	mu       sync.Mutex
	received []*nats.Msg
	fail     error
	delay    time.Duration
}

func (f *fakePublisher) PublishMsg(ctx context.Context, msg *nats.Msg) error {
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	f.mu.Lock()
	f.received = append(f.received, msg)
	f.mu.Unlock()
	return f.fail
}

func (f *fakePublisher) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.received)
}

func newTestDispatcher(pub msgPublisher, cfg Config) *Dispatcher {
	publishTimeout := cfg.PublishTimeout
	if publishTimeout <= 0 {
		publishTimeout = 5 * time.Second
	}
	staleClaimAfter := cfg.StaleClaimAfter
	if staleClaimAfter <= 0 {
		staleClaimAfter = 5 * time.Minute
	}
	return &Dispatcher{
		pool:            sharedPool,
		pub:             pub,
		cfg:             cfg,
		logger:          zap.NewNop(),
		done:            make(chan struct{}),
		publishTimeout:  publishTimeout,
		staleClaimAfter: staleClaimAfter,
	}
}

// insertOutboxRow inserts a row directly (bypassing RLS, as the dispatcher's
// own pool does) so tests can set up fixtures without going through a
// module's tenant-scoped repo.
func insertOutboxRow(t *testing.T, ctx context.Context, tenantID, aggregateID uuid.UUID, eventType string) uuid.UUID {
	t.Helper()
	eventID := uuid.New()
	_, err := sharedPool.Exec(ctx, `
		INSERT INTO pos_outbox (event_id, tenant_id, aggregate_type, aggregate_id, event_type, payload)
		VALUES ($1, $2, 'order', $3, $4, '{}')
	`, eventID, tenantID, aggregateID.String(), eventType)
	require.NoError(t, err)
	return eventID
}

func rowState(t *testing.T, ctx context.Context, eventID uuid.UUID) (processed bool, claimed bool, retryCount int, isDead bool) {
	t.Helper()
	var processedAt, claimedAt *time.Time
	err := sharedPool.QueryRow(ctx, `
		SELECT processed_at, claimed_at, retry_count, is_dead FROM pos_outbox WHERE event_id = $1
	`, eventID).Scan(&processedAt, &claimedAt, &retryCount, &isDead)
	require.NoError(t, err)
	return processedAt != nil, claimedAt != nil, retryCount, isDead
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestDispatchTable_PublishSuccess_MarksProcessed(t *testing.T) {
	ctx := context.Background()
	tenantID := uuid.New()
	eventID := insertOutboxRow(t, ctx, tenantID, uuid.New(), "order.placed")

	pub := &fakePublisher{}
	d := newTestDispatcher(pub, Config{BatchSize: 10, MaxRetries: 3})

	err := d.dispatchTable(ctx, TableSpec{Table: "pos_outbox", Module: "pos"})
	require.NoError(t, err)

	processed, claimed, retryCount, isDead := rowState(t, ctx, eventID)
	assert.True(t, processed, "expected row to be marked processed")
	assert.False(t, isDead)
	assert.Equal(t, 0, retryCount)
	_ = claimed

	require.Equal(t, 1, pub.count())
	assert.Equal(t, eventID.String(), pub.received[0].Header.Get("Nats-Msg-Id"),
		"Nats-Msg-Id header must carry the event id for JetStream dedup")
}

// TestDispatchTable_BillingOutbox proves billing_outbox is schema-compatible
// with the shared dispatcher path (billing/000002 aligned it with pos/payment;
// billing's aggregate_id is UUID-typed, the others are TEXT — the scan must
// tolerate both).
func TestDispatchTable_BillingOutbox_PublishSuccess(t *testing.T) {
	ctx := context.Background()
	eventID := uuid.New()
	_, err := sharedPool.Exec(ctx, `
		INSERT INTO billing_outbox (event_id, tenant_id, aggregate_type, aggregate_id, event_type, payload)
		VALUES ($1, $2, 'invoice', $3, 'invoice.issued', '{}')
	`, eventID, uuid.New(), uuid.New())
	require.NoError(t, err)

	pub := &fakePublisher{}
	d := newTestDispatcher(pub, Config{BatchSize: 10, MaxRetries: 3})

	require.NoError(t, d.dispatchTable(ctx, TableSpec{Table: "billing_outbox", Module: "billing"}))

	var processedAt *time.Time
	require.NoError(t, sharedPool.QueryRow(ctx,
		`SELECT processed_at FROM billing_outbox WHERE event_id = $1`, eventID).Scan(&processedAt))
	assert.NotNil(t, processedAt, "expected billing row to be marked processed")
	require.Equal(t, 1, pub.count())
	assert.Contains(t, pub.received[0].Subject, "billing.")
}

func TestDispatchTable_PublishFailure_ReleasesClaimAndSchedulesRetry(t *testing.T) {
	ctx := context.Background()
	tenantID := uuid.New()
	eventID := insertOutboxRow(t, ctx, tenantID, uuid.New(), "order.placed")

	pub := &fakePublisher{fail: errors.New("nats: no responders")}
	d := newTestDispatcher(pub, Config{BatchSize: 10, MaxRetries: 3})

	err := d.dispatchTable(ctx, TableSpec{Table: "pos_outbox", Module: "pos"})
	require.NoError(t, err, "dispatch cycle itself must not error on a publish failure")

	processed, claimed, retryCount, isDead := rowState(t, ctx, eventID)
	assert.False(t, processed)
	assert.False(t, claimed, "claimed_at must be released so the row is retry-eligible on its own schedule")
	assert.Equal(t, 1, retryCount)
	assert.False(t, isDead)
}

func TestDispatchTable_ExceedsMaxRetries_MarksDead(t *testing.T) {
	ctx := context.Background()
	tenantID := uuid.New()
	eventID := insertOutboxRow(t, ctx, tenantID, uuid.New(), "order.placed")

	// Pre-seed retry_count at the limit so the next failure tips it over.
	_, err := sharedPool.Exec(ctx, `UPDATE pos_outbox SET retry_count = 3 WHERE event_id = $1`, eventID)
	require.NoError(t, err)

	pub := &fakePublisher{fail: errors.New("permanent failure")}
	d := newTestDispatcher(pub, Config{BatchSize: 10, MaxRetries: 3})

	err = d.dispatchTable(ctx, TableSpec{Table: "pos_outbox", Module: "pos"})
	require.NoError(t, err)

	processed, _, retryCount, isDead := rowState(t, ctx, eventID)
	assert.False(t, processed)
	assert.Equal(t, 4, retryCount)
	assert.True(t, isDead)
}

func TestDispatchTable_PublishTimeout_DoesNotBlockDispatchCycle(t *testing.T) {
	ctx := context.Background()
	tenantID := uuid.New()
	eventID := insertOutboxRow(t, ctx, tenantID, uuid.New(), "order.placed")

	pub := &fakePublisher{delay: 200 * time.Millisecond}
	d := newTestDispatcher(pub, Config{BatchSize: 10, MaxRetries: 3, PublishTimeout: 20 * time.Millisecond})

	start := time.Now()
	err := d.dispatchTable(ctx, TableSpec{Table: "pos_outbox", Module: "pos"})
	elapsed := time.Since(start)
	require.NoError(t, err)
	assert.Less(t, elapsed, 200*time.Millisecond, "publish timeout must cut the slow publish short")

	processed, claimed, retryCount, _ := rowState(t, ctx, eventID)
	assert.False(t, processed)
	assert.False(t, claimed)
	assert.Equal(t, 1, retryCount, "timed-out publish must be treated as a retryable failure")
}

func TestClaimBatch_SkipsRowClaimedByAnotherInFlightWorker(t *testing.T) {
	ctx := context.Background()
	tenantID := uuid.New()
	eventID := insertOutboxRow(t, ctx, tenantID, uuid.New(), "order.placed")

	// Simulate another dispatcher instance having just claimed this row.
	_, err := sharedPool.Exec(ctx, `UPDATE pos_outbox SET claimed_at = NOW() WHERE event_id = $1`, eventID)
	require.NoError(t, err)

	d := newTestDispatcher(&fakePublisher{}, Config{BatchSize: 10, StaleClaimAfter: time.Hour})

	batch, err := d.claimBatch(ctx, "pos_outbox")
	require.NoError(t, err)
	for _, r := range batch {
		assert.NotEqual(t, eventID, r.eventID, "a freshly claimed row must not be reclaimed")
	}
}

func TestClaimBatch_ReclaimsStaleClaim(t *testing.T) {
	ctx := context.Background()
	tenantID := uuid.New()
	eventID := insertOutboxRow(t, ctx, tenantID, uuid.New(), "order.placed")

	// Simulate a dispatcher that crashed after claiming but before applying results:
	// claimed_at is set far enough in the past to exceed StaleClaimAfter.
	_, err := sharedPool.Exec(ctx, `UPDATE pos_outbox SET claimed_at = NOW() - INTERVAL '10 minutes' WHERE event_id = $1`, eventID)
	require.NoError(t, err)

	d := newTestDispatcher(&fakePublisher{}, Config{BatchSize: 10, StaleClaimAfter: time.Second})

	batch, err := d.claimBatch(ctx, "pos_outbox")
	require.NoError(t, err)

	var found bool
	for _, r := range batch {
		if r.eventID == eventID {
			found = true
		}
	}
	assert.True(t, found, "a stale claim past StaleClaimAfter must be reclaimed")
}

func TestRun_StopsCleanlyOnCancel(t *testing.T) {
	d := newTestDispatcher(&fakePublisher{}, Config{PollInterval: 10 * time.Millisecond, BatchSize: 10})
	ctx, cancel := context.WithCancel(context.Background())

	go d.run(ctx)
	time.Sleep(30 * time.Millisecond)
	cancel()

	select {
	case <-d.done:
	case <-time.After(2 * time.Second):
		t.Fatal("run() did not exit after context cancellation — goroutine leak")
	}
}
