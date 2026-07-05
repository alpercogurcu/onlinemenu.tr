package service_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	pgxpool "github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"go.uber.org/goleak"
	"go.uber.org/zap"

	"onlinemenu.tr/internal/modules/payment/domain"
	"onlinemenu.tr/internal/modules/payment/repo"
	"onlinemenu.tr/internal/modules/payment/service"
	"onlinemenu.tr/internal/platform/db"
)

var (
	sharedPool *db.Pool
	tenantA    = uuid.MustParse("aaaaaaaa-0000-0000-0000-000000000001")
	branchA    = uuid.MustParse("cccccccc-0000-0000-0000-000000000001")
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

	sharedPool = newPool(ctx, superDSN, "app_runtime", "runtime_secret")

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
// File: .../backend/internal/modules/payment/service/integration_test.go
// Walk up 4: service→payment→modules→internal→backend
func migrationsBase() string {
	_, file, _, _ := runtime.Caller(0)
	base := filepath.Dir(file)
	for range 4 {
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

	for _, mod := range []string{"tenant", "identity", "catalog", "pos", "payment"} {
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

func newPool(ctx context.Context, baseDSN, user, password string) *db.Pool {
	cfg, err := pgxpool.ParseConfig(baseDSN)
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse pool config: %v\n", err)
		os.Exit(1)
	}
	cfg.ConnConfig.User = user
	cfg.ConnConfig.Password = password
	cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	cfg.MaxConns = 10

	p, err := db.NewPoolFromConfig(ctx, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create pool (%s): %v\n", user, err)
		os.Exit(1)
	}
	return p
}

func newPaymentService() *service.PaymentService {
	return service.NewPaymentService(service.Params{
		DB:          sharedPool,
		PaymentRepo: repo.NewPaymentRepo(),
		Fiscal:      domain.MockFiscalAdapter{},
		Logger:      zap.NewNop(),
	})
}

// ---------------------------------------------------------------------------
// ListByCheck (double-payment guard)
// ---------------------------------------------------------------------------

// TestListByCheck_ReturnsOnlyCompletedForCheck exercises the service surface
// POS uses to show already-recorded payments when a cashier reopens a check.
// A completed payment on the target check, a pending payment on the same
// check (idempotency reservation without a completed fiscal cycle — not
// modeled directly here since RegisterSale always completes synchronously in
// this service, so the "second check has none" case stands in for the
// tenant/check scoping guarantee), and a completed payment on a different
// check must not bleed into each other's result.
func TestListByCheck_ReturnsOnlyCompletedForCheck(t *testing.T) {
	ctx := context.Background()
	svc := newPaymentService()

	checkID := uuid.New()
	otherCheckID := uuid.New()

	paid, err := svc.RegisterSale(ctx, service.RegisterSaleRequest{
		TenantID:       tenantA,
		BranchID:       branchA,
		CheckID:        &checkID,
		IdempotencyKey: uuid.New().String(),
		Method:         domain.PaymentMethodCash,
		AmountTotal:    4200,
		Currency:       "TRY",
	})
	require.NoError(t, err)
	require.Equal(t, domain.PaymentStatusCompleted, paid.Status)

	_, err = svc.RegisterSale(ctx, service.RegisterSaleRequest{
		TenantID:       tenantA,
		BranchID:       branchA,
		CheckID:        &otherCheckID,
		IdempotencyKey: uuid.New().String(),
		Method:         domain.PaymentMethodCash,
		AmountTotal:    9900,
		Currency:       "TRY",
	})
	require.NoError(t, err)

	payments, err := svc.ListByCheck(ctx, tenantA, checkID)
	require.NoError(t, err)
	require.Len(t, payments, 1, "must only return payments for the requested check")
	assert.Equal(t, paid.ID, payments[0].ID)
	assert.Equal(t, int64(4200), payments[0].AmountTotal)
}

// ---------------------------------------------------------------------------
// Idempotency race tests
// ---------------------------------------------------------------------------

func TestRegisterSale_SameKeySequential_ReturnsSamePayment(t *testing.T) {
	ctx := context.Background()
	svc := newPaymentService()
	key := uuid.New().String()

	req := service.RegisterSaleRequest{
		TenantID:       tenantA,
		BranchID:       branchA,
		IdempotencyKey: key,
		Method:         domain.PaymentMethodCash,
		AmountTotal:    12345,
		Currency:       "TRY",
	}

	first, err := svc.RegisterSale(ctx, req)
	require.NoError(t, err)

	second, err := svc.RegisterSale(ctx, req)
	require.NoError(t, err)

	assert.Equal(t, first.ID, second.ID, "second call with the same key must return the original payment")
	assert.Equal(t, domain.PaymentStatusCompleted, second.Status)
}

// TestRegisterSale_ConcurrentSameKey_NoDuplicateOrError exercises the race the
// HTTP idempotency middleware does not fully close at the service layer:
// two goroutines calling RegisterSale with the same key can both pass the
// in-transaction pre-check before either commits. The loser must recover via
// GetByIdempotencyKey in a fresh transaction instead of surfacing a 500.
func TestRegisterSale_ConcurrentSameKey_NoDuplicateOrError(t *testing.T) {
	ctx := context.Background()
	svc := newPaymentService()
	key := uuid.New().String()

	const n = 8
	results := make([]domain.Payment, n)
	errs := make([]error, n)

	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = svc.RegisterSale(ctx, service.RegisterSaleRequest{
				TenantID:       tenantA,
				BranchID:       branchA,
				IdempotencyKey: key,
				Method:         domain.PaymentMethodTerminal,
				AmountTotal:    5000,
				Currency:       "TRY",
			})
		}(i)
	}
	wg.Wait()

	var firstID uuid.UUID
	for i, err := range errs {
		require.NoError(t, err, "call %d must not surface the unique-violation race as an error", i)
		if firstID == uuid.Nil {
			firstID = results[i].ID
		}
		assert.Equal(t, firstID, results[i].ID, "all concurrent calls with the same key must resolve to one payment")
	}

	total, err := svc.TotalPaidForCheck(ctx, tenantA, uuid.New())
	require.NoError(t, err)
	assert.Equal(t, int64(0), total, "sanity: unrelated check must remain unaffected")

	var count int
	err = sharedPool.WithTenantReadTx(ctx, tenantA, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT COUNT(*) FROM payments WHERE idempotency_key = $1`, key).Scan(&count)
	})
	require.NoError(t, err)
	assert.Equal(t, 1, count, "exactly one payment row must exist for the idempotency key")
}
