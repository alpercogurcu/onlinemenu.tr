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

	"onlinemenu.tr/internal/modules/pos/domain"
	"onlinemenu.tr/internal/modules/pos/repo"
	"onlinemenu.tr/internal/platform/db"
)

var (
	sharedPool *db.Pool
	tenantA    = uuid.MustParse("aaaaaaaa-0000-0000-0000-000000000001")
	tenantB    = uuid.MustParse("bbbbbbbb-0000-0000-0000-000000000001")
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
// File: .../backend/internal/modules/pos/repo/integration_test.go
// Walk up 4 directories: repo/ → pos/ → modules/ → internal/ → backend/
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

	for _, mod := range []string{"tenant", "identity", "catalog", "pos"} {
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
// Check repo tests
// ---------------------------------------------------------------------------

func TestCheckRepo_CRUD(t *testing.T) {
	ctx := context.Background()
	checkRepo := repo.NewCheckRepo()

	var created domain.Check
	err := sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		var err error
		created, err = checkRepo.Create(ctx, tx, domain.Check{
			TenantID:   tenantA,
			BranchID:   branchA,
			TableLabel: "Masa 5",
			Status:     domain.CheckStatusOpen,
			OpenedBy:   staffA,
		})
		return err
	})
	require.NoError(t, err)
	assert.NotEqual(t, uuid.Nil, created.ID)
	assert.Equal(t, domain.CheckStatusOpen, created.Status)
	assert.Equal(t, "Masa 5", created.TableLabel)

	var fetched domain.Check
	err = sharedPool.WithTenantReadTx(ctx, tenantA, func(tx pgx.Tx) error {
		var err error
		fetched, err = checkRepo.GetByID(ctx, tx, created.ID)
		return err
	})
	require.NoError(t, err)
	assert.Equal(t, created.ID, fetched.ID)
	assert.Equal(t, tenantA, fetched.TenantID)

	var closed domain.Check
	err = sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		var err error
		closed, err = checkRepo.UpdateStatus(ctx, tx, created.ID, domain.CheckStatusClosed, domain.CheckStatusOpen, &staffA)
		return err
	})
	require.NoError(t, err)
	assert.Equal(t, domain.CheckStatusClosed, closed.Status)
	assert.NotNil(t, closed.ClosedAt)
	assert.Equal(t, &staffA, closed.ClosedBy)
}

func TestCheckRepo_RLSIsolation(t *testing.T) {
	ctx := context.Background()
	checkRepo := repo.NewCheckRepo()

	// Create a check under tenantA
	var checkA domain.Check
	err := sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		var err error
		checkA, err = checkRepo.Create(ctx, tx, domain.Check{
			TenantID:   tenantA,
			BranchID:   branchA,
			TableLabel: "RLS Masa",
			Status:     domain.CheckStatusOpen,
			OpenedBy:   staffA,
		})
		return err
	})
	require.NoError(t, err)

	// tenantB cannot see tenantA's check
	err = sharedPool.WithTenantReadTx(ctx, tenantB, func(tx pgx.Tx) error {
		_, err := checkRepo.GetByID(ctx, tx, checkA.ID)
		return err
	})
	assert.ErrorIs(t, err, repo.ErrNotFound, "tenantB must not see tenantA's check")
}

// ---------------------------------------------------------------------------
// Order repo tests
// ---------------------------------------------------------------------------

func newTestCheck(t *testing.T, ctx context.Context, tenantID uuid.UUID) domain.Check {
	t.Helper()
	checkRepo := repo.NewCheckRepo()
	var c domain.Check
	err := sharedPool.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		c, err = checkRepo.Create(ctx, tx, domain.Check{
			TenantID:   tenantID,
			BranchID:   branchA,
			TableLabel: "Test Masa",
			Status:     domain.CheckStatusOpen,
			OpenedBy:   staffA,
		})
		return err
	})
	require.NoError(t, err)
	return c
}

func TestOrderRepo_Create_WithItems(t *testing.T) {
	ctx := context.Background()
	orderRepo := repo.NewOrderRepo()
	check := newTestCheck(t, ctx, tenantA)

	productID := uuid.New()
	var created domain.Order
	err := sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		var err error
		created, err = orderRepo.Create(ctx, tx, domain.Order{
			TenantID:     tenantA,
			BranchID:     branchA,
			CheckID:      &check.ID,
			OrderChannel: domain.OrderChannelDineIn,
			Status:       domain.OrderStatusPending,
			Items: []domain.OrderItem{
				{
					ProductID:          productID,
					ProductName:        "Adana Kebap",
					ProductPriceAmount: 25000,
					ProductCurrency:    "TRY",
					TaxRateBPS:         1000,
					Quantity:           2,
					UnitPriceAmount:    25000,
				},
			},
		})
		return err
	})
	require.NoError(t, err)
	assert.NotEqual(t, uuid.Nil, created.ID)
	assert.Equal(t, domain.OrderStatusPending, created.Status)
	assert.Equal(t, domain.OrderChannelDineIn, created.OrderChannel)
	require.Len(t, created.Items, 1)
	assert.Equal(t, "Adana Kebap", created.Items[0].ProductName)
	assert.Equal(t, 2, created.Items[0].Quantity)
}

func TestOrderRepo_GetByID_WithItems(t *testing.T) {
	ctx := context.Background()
	orderRepo := repo.NewOrderRepo()
	check := newTestCheck(t, ctx, tenantA)

	var orderID uuid.UUID
	err := sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		o, err := orderRepo.Create(ctx, tx, domain.Order{
			TenantID:     tenantA,
			BranchID:     branchA,
			CheckID:      &check.ID,
			OrderChannel: domain.OrderChannelDineIn,
			Status:       domain.OrderStatusPending,
			Items: []domain.OrderItem{
				{
					ProductID:          uuid.New(),
					ProductName:        "Mercimek Çorbası",
					ProductPriceAmount: 8000,
					ProductCurrency:    "TRY",
					TaxRateBPS:         1000,
					Quantity:           1,
					UnitPriceAmount:    8000,
				},
			},
		})
		if err == nil {
			orderID = o.ID
		}
		return err
	})
	require.NoError(t, err)

	var fetched domain.Order
	err = sharedPool.WithTenantReadTx(ctx, tenantA, func(tx pgx.Tx) error {
		var err error
		fetched, err = orderRepo.GetByID(ctx, tx, orderID)
		return err
	})
	require.NoError(t, err)
	assert.Equal(t, orderID, fetched.ID)
	require.Len(t, fetched.Items, 1)
	assert.Equal(t, "Mercimek Çorbası", fetched.Items[0].ProductName)
}

func TestOrderRepo_Accept_Reject(t *testing.T) {
	ctx := context.Background()
	orderRepo := repo.NewOrderRepo()

	// delivery order (no check)
	var deliveryOrderID uuid.UUID
	err := sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		o, err := orderRepo.Create(ctx, tx, domain.Order{
			TenantID:     tenantA,
			BranchID:     branchA,
			OrderChannel: domain.OrderChannelDelivery,
			Status:       domain.OrderStatusPending,
			Items: []domain.OrderItem{
				{
					ProductID:          uuid.New(),
					ProductName:        "Lahmacun",
					ProductPriceAmount: 12000,
					ProductCurrency:    "TRY",
					TaxRateBPS:         1000,
					Quantity:           3,
					UnitPriceAmount:    12000,
				},
			},
		})
		if err == nil {
			deliveryOrderID = o.ID
		}
		return err
	})
	require.NoError(t, err)

	var accepted domain.Order
	err = sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		var err error
		accepted, err = orderRepo.Accept(ctx, tx, deliveryOrderID, staffA, domain.OrderStatusPending)
		return err
	})
	require.NoError(t, err)
	assert.Equal(t, domain.OrderStatusAccepted, accepted.Status)
	assert.NotNil(t, accepted.AcceptedAt)
	assert.Equal(t, &staffA, accepted.AcceptedBy)

	// Create another order to test rejection
	var rejectOrderID uuid.UUID
	err = sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		o, err := orderRepo.Create(ctx, tx, domain.Order{
			TenantID:     tenantA,
			BranchID:     branchA,
			OrderChannel: domain.OrderChannelDelivery,
			Status:       domain.OrderStatusPending,
			Items: []domain.OrderItem{
				{
					ProductID:          uuid.New(),
					ProductName:        "Pide",
					ProductPriceAmount: 18000,
					ProductCurrency:    "TRY",
					TaxRateBPS:         1000,
					Quantity:           1,
					UnitPriceAmount:    18000,
				},
			},
		})
		if err == nil {
			rejectOrderID = o.ID
		}
		return err
	})
	require.NoError(t, err)

	var rejected domain.Order
	err = sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		var err error
		rejected, err = orderRepo.Reject(ctx, tx, rejectOrderID, staffA, "ürün stokta yok", domain.OrderStatusPending)
		return err
	})
	require.NoError(t, err)
	assert.Equal(t, domain.OrderStatusRejected, rejected.Status)
	assert.Equal(t, "ürün stokta yok", rejected.RejectionReason)
	assert.NotNil(t, rejected.RejectedAt)
}

func TestOrderRepo_RLSIsolation(t *testing.T) {
	ctx := context.Background()
	orderRepo := repo.NewOrderRepo()

	var orderA domain.Order
	err := sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		var err error
		orderA, err = orderRepo.Create(ctx, tx, domain.Order{
			TenantID:     tenantA,
			BranchID:     branchA,
			OrderChannel: domain.OrderChannelTakeaway,
			Status:       domain.OrderStatusPending,
			Items: []domain.OrderItem{
				{
					ProductID:          uuid.New(),
					ProductName:        "Döner",
					ProductPriceAmount: 15000,
					ProductCurrency:    "TRY",
					TaxRateBPS:         1000,
					Quantity:           1,
					UnitPriceAmount:    15000,
				},
			},
		})
		return err
	})
	require.NoError(t, err)

	// tenantB cannot see tenantA's order
	err = sharedPool.WithTenantReadTx(ctx, tenantB, func(tx pgx.Tx) error {
		_, err := orderRepo.GetByID(ctx, tx, orderA.ID)
		return err
	})
	assert.ErrorIs(t, err, repo.ErrNotFound, "tenantB must not see tenantA's order")
}
