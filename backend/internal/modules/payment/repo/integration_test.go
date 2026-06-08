package repo_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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

	"onlinemenu.tr/internal/modules/payment/domain"
	"onlinemenu.tr/internal/modules/payment/repo"
	"onlinemenu.tr/internal/platform/db"
)

var (
	sharedPool *db.Pool
	tenantA    = uuid.MustParse("aaaaaaaa-0000-0000-0000-000000000001")
	tenantB    = uuid.MustParse("bbbbbbbb-0000-0000-0000-000000000001")
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
// File: .../backend/internal/modules/payment/repo/integration_test.go
// filepath.Dir(file) = .../backend/internal/modules/payment/repo
// Walk up 4: repo→payment→modules→internal→backend
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
	cfg.MaxConns = 5

	p, err := db.NewPoolFromConfig(ctx, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create pool (%s): %v\n", user, err)
		os.Exit(1)
	}
	return p
}

// ---------------------------------------------------------------------------
// PaymentRepo tests
// ---------------------------------------------------------------------------

func TestPaymentRepo_Create_GetByID(t *testing.T) {
	ctx := context.Background()
	r := repo.NewPaymentRepo()
	checkID := uuid.New()

	var created domain.Payment
	err := sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		var err error
		created, err = r.Create(ctx, tx, domain.Payment{
			TenantID:       tenantA,
			BranchID:       branchA,
			CheckID:        &checkID,
			IdempotencyKey: "idem-key-001",
			Method:         domain.PaymentMethodCash,
			AmountTotal:    15000,
			Currency:       "TRY",
		})
		return err
	})
	require.NoError(t, err)
	assert.NotEqual(t, uuid.Nil, created.ID)
	assert.Equal(t, domain.PaymentStatusPending, created.Status)
	assert.Equal(t, int64(15000), created.AmountTotal)

	var fetched domain.Payment
	err = sharedPool.WithTenantReadTx(ctx, tenantA, func(tx pgx.Tx) error {
		var err error
		fetched, err = r.GetByID(ctx, tx, created.ID)
		return err
	})
	require.NoError(t, err)
	assert.Equal(t, created.ID, fetched.ID)
	assert.Equal(t, "idem-key-001", fetched.IdempotencyKey)
}

func TestPaymentRepo_GetByIdempotencyKey(t *testing.T) {
	ctx := context.Background()
	r := repo.NewPaymentRepo()
	key := uuid.New().String()

	var created domain.Payment
	err := sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		var err error
		created, err = r.Create(ctx, tx, domain.Payment{
			TenantID:       tenantA,
			BranchID:       branchA,
			IdempotencyKey: key,
			Method:         domain.PaymentMethodTerminal,
			AmountTotal:    5000,
			Currency:       "TRY",
		})
		return err
	})
	require.NoError(t, err)

	var fetched domain.Payment
	err = sharedPool.WithTenantReadTx(ctx, tenantA, func(tx pgx.Tx) error {
		var err error
		fetched, err = r.GetByIdempotencyKey(ctx, tx, tenantA, key)
		return err
	})
	require.NoError(t, err)
	assert.Equal(t, created.ID, fetched.ID)
}

func TestPaymentRepo_Complete_WithReceipt(t *testing.T) {
	ctx := context.Background()
	r := repo.NewPaymentRepo()

	var paymentID uuid.UUID
	var receiptID uuid.UUID

	err := sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		p, err := r.Create(ctx, tx, domain.Payment{
			TenantID:       tenantA,
			BranchID:       branchA,
			IdempotencyKey: uuid.New().String(),
			Method:         domain.PaymentMethodCash,
			AmountTotal:    20000,
			Currency:       "TRY",
		})
		if err != nil {
			return err
		}
		paymentID = p.ID

		receiptID, err = r.InsertFiscalReceipt(ctx, tx, domain.FiscalReceipt{
			TenantID:      tenantA,
			PaymentID:     paymentID,
			DeviceType:    "mock",
			ReceiptNumber: "MOCK-001",
			ReceiptData:   map[string]any{"amount": 20000},
		})
		if err != nil {
			return err
		}

		return r.Complete(ctx, tx, paymentID, receiptID)
	})
	require.NoError(t, err)

	var fetched domain.Payment
	err = sharedPool.WithTenantReadTx(ctx, tenantA, func(tx pgx.Tx) error {
		var err error
		fetched, err = r.GetByID(ctx, tx, paymentID)
		return err
	})
	require.NoError(t, err)
	assert.Equal(t, domain.PaymentStatusCompleted, fetched.Status)
	require.NotNil(t, fetched.FiscalReceiptID)
	assert.Equal(t, receiptID, *fetched.FiscalReceiptID)
}

func TestPaymentRepo_TotalPaidForCheck(t *testing.T) {
	ctx := context.Background()
	r := repo.NewPaymentRepo()
	checkID := uuid.New()

	err := sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		// Create two payments for the same check and complete them.
		for i, amount := range []int64{10000, 5000} {
			p, err := r.Create(ctx, tx, domain.Payment{
				TenantID:       tenantA,
				BranchID:       branchA,
				CheckID:        &checkID,
				IdempotencyKey: fmt.Sprintf("total-check-key-%d", i),
				Method:         domain.PaymentMethodCash,
				AmountTotal:    amount,
				Currency:       "TRY",
			})
			if err != nil {
				return err
			}
			receiptID, err := r.InsertFiscalReceipt(ctx, tx, domain.FiscalReceipt{
				TenantID:      tenantA,
				PaymentID:     p.ID,
				DeviceType:    "mock",
				ReceiptNumber: fmt.Sprintf("MOCK-%d", i),
				ReceiptData:   map[string]any{},
			})
			if err != nil {
				return err
			}
			if err := r.Complete(ctx, tx, p.ID, receiptID); err != nil {
				return err
			}
		}
		return nil
	})
	require.NoError(t, err)

	var total int64
	err = sharedPool.WithTenantReadTx(ctx, tenantA, func(tx pgx.Tx) error {
		var err error
		total, err = r.TotalPaidForCheck(ctx, tx, tenantA, checkID)
		return err
	})
	require.NoError(t, err)
	assert.Equal(t, int64(15000), total)
}

func TestPaymentRepo_RLSIsolation(t *testing.T) {
	ctx := context.Background()
	r := repo.NewPaymentRepo()

	var created domain.Payment
	err := sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		var err error
		created, err = r.Create(ctx, tx, domain.Payment{
			TenantID:       tenantA,
			BranchID:       branchA,
			IdempotencyKey: "rls-isolation-key",
			Method:         domain.PaymentMethodCash,
			AmountTotal:    9999,
			Currency:       "TRY",
		})
		return err
	})
	require.NoError(t, err)

	// tenantB must NOT be able to read tenantA's payment.
	err = sharedPool.WithTenantReadTx(ctx, tenantB, func(tx pgx.Tx) error {
		_, err := r.GetByID(ctx, tx, created.ID)
		return err
	})
	assert.ErrorIs(t, err, repo.ErrNotFound, "RLS must prevent cross-tenant read")
}
