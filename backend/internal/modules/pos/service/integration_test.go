package service_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
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

	paymentpub "onlinemenu.tr/internal/modules/payment/public"
	"onlinemenu.tr/internal/modules/pos/domain"
	pub "onlinemenu.tr/internal/modules/pos/public"
	"onlinemenu.tr/internal/modules/pos/repo"
	"onlinemenu.tr/internal/modules/pos/service"
	"onlinemenu.tr/internal/platform/db"
)

var (
	sharedPool *db.Pool
	tenantA    = uuid.MustParse("aaaaaaaa-0000-0000-0000-000000000001")
	branchA    = uuid.MustParse("cccccccc-0000-0000-0000-000000000001")
	staffA     = uuid.New()
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
// File: .../backend/internal/modules/pos/service/integration_test.go
// Walk up 4: service→pos→modules→internal→backend
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

// zeroSaleReader always reports a check as fully unpaid-free (0 due), so Close
// never fails on ErrInsufficientPayment in these concurrency tests.
type zeroSaleReader struct{}

func (zeroSaleReader) TotalPaidForCheck(_ context.Context, _, _ uuid.UUID) (int64, error) {
	return 0, nil
}

var _ paymentpub.SaleReader = zeroSaleReader{}

func newCheckService() *service.CheckService {
	return service.NewCheckService(service.CheckParams{
		DB:         sharedPool,
		CheckRepo:  repo.NewCheckRepo(),
		TableRepo:  repo.NewTableRepo(),
		SaleReader: zeroSaleReader{},
		Logger:     zap.NewNop(),
	})
}

func newTableService() *service.TableService {
	return service.NewTableService(service.TableParams{
		DB:        sharedPool,
		TableRepo: repo.NewTableRepo(),
		Logger:    zap.NewNop(),
	})
}

func openTestCheck(t *testing.T, ctx context.Context, svc *service.CheckService) domain.Check {
	t.Helper()
	c, err := svc.Open(ctx, tenantA, chainWidePrincipal(), domain.Check{
		BranchID:   branchA,
		TableLabel: "Masa Concurrency",
		OpenedBy:   staffA,
	})
	require.NoError(t, err)
	return c
}

// ---------------------------------------------------------------------------
// Check close/cancel race tests
// ---------------------------------------------------------------------------

func TestCheckService_Close_Idempotent_SecondCallConflicts(t *testing.T) {
	ctx := context.Background()
	svc := newCheckService()
	c := openTestCheck(t, ctx, svc)

	_, err := svc.Close(ctx, tenantA, chainWidePrincipal(), c.ID, staffA)
	require.NoError(t, err)

	_, err = svc.Close(ctx, tenantA, chainWidePrincipal(), c.ID, staffA)
	assert.ErrorIs(t, err, pub.ErrInvalidTransition, "closing an already-closed check must be rejected")
}

// TestCheckService_ConcurrentClose_EmitsExactlyOneEvent proves the row lock
// (CheckRepo.GetForUpdate), not just the UpdateStatus guard, is what prevents
// two concurrent Close calls on the same open check from both succeeding.
func TestCheckService_ConcurrentClose_EmitsExactlyOneEvent(t *testing.T) {
	ctx := context.Background()
	svc := newCheckService()
	c := openTestCheck(t, ctx, svc)

	const n = 6
	var successCount atomic.Int32
	var conflictCount atomic.Int32
	var otherErrCount atomic.Int32

	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			_, err := svc.Close(ctx, tenantA, chainWidePrincipal(), c.ID, staffA)
			switch {
			case err == nil:
				successCount.Add(1)
			case errors.Is(err, pub.ErrInvalidTransition):
				conflictCount.Add(1)
			default:
				otherErrCount.Add(1)
			}
		}()
	}
	wg.Wait()

	assert.Equal(t, int32(0), otherErrCount.Load(), "no unexpected errors")
	assert.Equal(t, int32(1), successCount.Load(), "exactly one Close call must succeed")
	assert.Equal(t, int32(n-1), conflictCount.Load(), "all other calls must observe the already-closed status")

	var eventCount int
	err := sharedPool.WithTenantReadTx(ctx, tenantA, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT COUNT(*) FROM pos_outbox
			WHERE aggregate_id = $1 AND event_type = 'check.closed'
		`, c.ID.String()).Scan(&eventCount)
	})
	require.NoError(t, err)
	assert.Equal(t, 1, eventCount, "exactly one check.closed outbox event must be recorded")
}

func TestCheckService_CloseThenCancel_SecondCallConflicts(t *testing.T) {
	ctx := context.Background()
	svc := newCheckService()
	c := openTestCheck(t, ctx, svc)

	_, err := svc.Close(ctx, tenantA, chainWidePrincipal(), c.ID, staffA)
	require.NoError(t, err)

	_, err = svc.Cancel(ctx, tenantA, chainWidePrincipal(), c.ID, staffA)
	assert.ErrorIs(t, err, pub.ErrInvalidTransition, "cancelling an already-closed check must be rejected")
}
