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

	"onlinemenu.tr/internal/modules/inventory/domain"
	"onlinemenu.tr/internal/modules/inventory/repo"
	"onlinemenu.tr/internal/platform/db"
)

var (
	sharedPool *db.Pool
	tenantA    = uuid.MustParse("aaaaaaaa-0000-0000-0000-000000000001")
	tenantB    = uuid.MustParse("bbbbbbbb-0000-0000-0000-000000000001")
	branchA    = uuid.MustParse("aaaaaaaa-0000-0000-0000-000000000002")
	product1   = uuid.MustParse("11111111-0000-0000-0000-000000000001")
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
func migrationsBase() string {
	_, file, _, _ := runtime.Caller(0)
	// file = .../backend/internal/modules/inventory/repo/integration_test.go
	// walk up 4 directories: repo/ → inventory/ → modules/ → internal/ → backend/
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
	cfg.ConnConfig.User = "app_migrator"
	cfg.ConnConfig.Password = "migrator_secret"

	migratorDSN := fmt.Sprintf("pgx5://%s:%s@%s/%s?sslmode=disable",
		cfg.ConnConfig.User, cfg.ConnConfig.Password,
		cfg.ConnConfig.Host+fmt.Sprintf(":%d", cfg.ConnConfig.Port),
		cfg.ConnConfig.Database,
	)

	migrateModules := []string{"tenant", "identity", "inventory"}
	for _, mod := range migrateModules {
		absPath := filepath.Join(migrationsBase(), mod)
		src := fmt.Sprintf("file://%s", absPath)
		table := fmt.Sprintf("schema_migrations_%s", mod)
		dsn := fmt.Sprintf("%s&x-migrations-table=%s", migratorDSN, table)

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
			return fmt.Errorf("stmt failed %q: %w", s[:min(60, len(s))], err)
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

// withTx is a test helper that runs f inside a tenant-scoped transaction.
func withTx(t *testing.T, tenantID uuid.UUID, f func(tx pgx.Tx)) {
	t.Helper()
	err := sharedPool.WithTenantTx(context.Background(), tenantID, func(tx pgx.Tx) error {
		f(tx)
		return nil
	})
	require.NoError(t, err)
}

func withReadTx(t *testing.T, tenantID uuid.UUID, f func(tx pgx.Tx)) {
	t.Helper()
	err := sharedPool.WithTenantReadTx(context.Background(), tenantID, func(tx pgx.Tx) error {
		f(tx)
		return nil
	})
	require.NoError(t, err)
}

// ============================================================
// InventoryLevelRepo tests
// ============================================================

func TestInventoryLevelRepo_UpsertAndGet(t *testing.T) {
	lvlRepo := repo.NewInventoryLevelRepo()
	ctx := context.Background()

	var created domain.InventoryLevel
	withTx(t, tenantA, func(tx pgx.Tx) {
		var err error
		created, err = lvlRepo.Upsert(ctx, tx, domain.InventoryLevel{
			TenantID:  tenantA,
			BranchID:  branchA,
			ProductID: product1,
			Quantity:  50.5,
		})
		require.NoError(t, err)
	})

	assert.NotEqual(t, uuid.Nil, created.ID)
	assert.Equal(t, 50.5, created.Quantity)
	assert.Equal(t, branchA, created.BranchID)

	// Verify via GetByProduct.
	withReadTx(t, tenantA, func(tx pgx.Tx) {
		lvl, err := lvlRepo.GetByProduct(ctx, tx, branchA, product1)
		require.NoError(t, err)
		assert.InDelta(t, 50.5, lvl.Quantity, 0.001)
	})
}

func TestInventoryLevelRepo_UpsertIdempotent(t *testing.T) {
	lvlRepo := repo.NewInventoryLevelRepo()
	ctx := context.Background()
	prodID := uuid.New()

	withTx(t, tenantA, func(tx pgx.Tx) {
		_, err := lvlRepo.Upsert(ctx, tx, domain.InventoryLevel{
			TenantID:  tenantA,
			BranchID:  branchA,
			ProductID: prodID,
			Quantity:  10,
		})
		require.NoError(t, err)
	})

	// Upsert with different quantity should update.
	var updated domain.InventoryLevel
	withTx(t, tenantA, func(tx pgx.Tx) {
		var err error
		updated, err = lvlRepo.Upsert(ctx, tx, domain.InventoryLevel{
			TenantID:  tenantA,
			BranchID:  branchA,
			ProductID: prodID,
			Quantity:  25,
		})
		require.NoError(t, err)
	})
	assert.InDelta(t, 25.0, updated.Quantity, 0.001)
}

func TestInventoryLevelRepo_AdjustQuantity(t *testing.T) {
	lvlRepo := repo.NewInventoryLevelRepo()
	ctx := context.Background()
	prodID := uuid.New()

	// Start at zero (no prior level record).
	var lvl domain.InventoryLevel
	withTx(t, tenantA, func(tx pgx.Tx) {
		var err error
		lvl, err = lvlRepo.AdjustQuantity(ctx, tx, tenantA, branchA, prodID, 100)
		require.NoError(t, err)
	})
	assert.InDelta(t, 100.0, lvl.Quantity, 0.001)

	// Apply a negative delta.
	withTx(t, tenantA, func(tx pgx.Tx) {
		var err error
		lvl, err = lvlRepo.AdjustQuantity(ctx, tx, tenantA, branchA, prodID, -30)
		require.NoError(t, err)
	})
	assert.InDelta(t, 70.0, lvl.Quantity, 0.001)

	// Delta that would go negative is clamped to zero.
	withTx(t, tenantA, func(tx pgx.Tx) {
		var err error
		lvl, err = lvlRepo.AdjustQuantity(ctx, tx, tenantA, branchA, prodID, -999)
		require.NoError(t, err)
	})
	assert.InDelta(t, 0.0, lvl.Quantity, 0.001)
}

func TestInventoryLevelRepo_TenantIsolation(t *testing.T) {
	lvlRepo := repo.NewInventoryLevelRepo()
	ctx := context.Background()
	prodID := uuid.New()

	// Insert level for tenantA.
	withTx(t, tenantA, func(tx pgx.Tx) {
		_, err := lvlRepo.Upsert(ctx, tx, domain.InventoryLevel{
			TenantID:  tenantA,
			BranchID:  branchA,
			ProductID: prodID,
			Quantity:  42,
		})
		require.NoError(t, err)
	})

	// tenantB should not see it.
	withReadTx(t, tenantB, func(tx pgx.Tx) {
		_, err := lvlRepo.GetByProduct(ctx, tx, branchA, prodID)
		assert.ErrorIs(t, err, repo.ErrNotFound)
	})
}

func TestInventoryLevelRepo_ListByBranch(t *testing.T) {
	lvlRepo := repo.NewInventoryLevelRepo()
	ctx := context.Background()
	branchID := uuid.New()

	withTx(t, tenantA, func(tx pgx.Tx) {
		for i := range 3 {
			_, err := lvlRepo.Upsert(ctx, tx, domain.InventoryLevel{
				TenantID:  tenantA,
				BranchID:  branchID,
				ProductID: uuid.New(),
				Quantity:  float64(i + 1),
			})
			require.NoError(t, err)
		}
	})

	withReadTx(t, tenantA, func(tx pgx.Tx) {
		levels, err := lvlRepo.ListByBranch(ctx, tx, branchID)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(levels), 3)
	})
}

// ============================================================
// InventoryTransactionRepo tests
// ============================================================

func TestInventoryTransactionRepo_CreateAndList(t *testing.T) {
	txRepo := repo.NewInventoryTransactionRepo()
	ctx := context.Background()
	prodID := uuid.New()
	branchID := uuid.New()

	notes := "initial restock"
	var created domain.InventoryTransaction
	withTx(t, tenantA, func(tx pgx.Tx) {
		var err error
		created, err = txRepo.Create(ctx, tx, domain.InventoryTransaction{
			TenantID:      tenantA,
			BranchID:      branchID,
			ProductID:     prodID,
			Type:          domain.TransactionTypeRestock,
			QuantityDelta: 100,
			Notes:         &notes,
		})
		require.NoError(t, err)
	})

	assert.NotEqual(t, uuid.Nil, created.ID)
	assert.Equal(t, domain.TransactionTypeRestock, created.Type)
	assert.InDelta(t, 100.0, created.QuantityDelta, 0.001)
	assert.Equal(t, &notes, created.Notes)

	// List returns the created transaction.
	withReadTx(t, tenantA, func(tx pgx.Tx) {
		txs, err := txRepo.ListByProduct(ctx, tx, branchID, prodID, 10)
		require.NoError(t, err)
		require.Len(t, txs, 1)
		assert.Equal(t, created.ID, txs[0].ID)
	})
}

func TestInventoryTransactionRepo_MultipleTypes(t *testing.T) {
	txRepo := repo.NewInventoryTransactionRepo()
	ctx := context.Background()
	prodID := uuid.New()
	branchID := uuid.New()

	movements := []struct {
		typ   domain.TransactionType
		delta float64
	}{
		{domain.TransactionTypeRestock, 200},
		{domain.TransactionTypeConsumption, -50},
		{domain.TransactionTypeWaste, -10},
		{domain.TransactionTypeAdjustment, 5},
	}

	withTx(t, tenantA, func(tx pgx.Tx) {
		for _, m := range movements {
			_, err := txRepo.Create(ctx, tx, domain.InventoryTransaction{
				TenantID:      tenantA,
				BranchID:      branchID,
				ProductID:     prodID,
				Type:          m.typ,
				QuantityDelta: m.delta,
			})
			require.NoError(t, err)
		}
	})

	withReadTx(t, tenantA, func(tx pgx.Tx) {
		txs, err := txRepo.ListByProduct(ctx, tx, branchID, prodID, 100)
		require.NoError(t, err)
		assert.Len(t, txs, len(movements))
		// All expected types are present (order by created_at; within-tx timestamps identical).
		types := make(map[domain.TransactionType]bool, len(txs))
		for _, tx := range txs {
			types[tx.Type] = true
		}
		for _, m := range movements {
			assert.True(t, types[m.typ], "expected type %q in results", m.typ)
		}
	})
}

func TestInventoryTransactionRepo_TenantIsolation(t *testing.T) {
	txRepo := repo.NewInventoryTransactionRepo()
	ctx := context.Background()
	prodID := uuid.New()
	branchID := uuid.New()

	withTx(t, tenantA, func(tx pgx.Tx) {
		_, err := txRepo.Create(ctx, tx, domain.InventoryTransaction{
			TenantID:      tenantA,
			BranchID:      branchID,
			ProductID:     prodID,
			Type:          domain.TransactionTypeRestock,
			QuantityDelta: 50,
		})
		require.NoError(t, err)
	})

	// tenantB sees no transactions for tenantA's branch+product.
	withReadTx(t, tenantB, func(tx pgx.Tx) {
		txs, err := txRepo.ListByProduct(ctx, tx, branchID, prodID, 10)
		require.NoError(t, err)
		assert.Empty(t, txs)
	})
}

func TestInventoryTransactionRepo_ListByBranch(t *testing.T) {
	txRepo := repo.NewInventoryTransactionRepo()
	ctx := context.Background()
	branchID := uuid.New()

	withTx(t, tenantA, func(tx pgx.Tx) {
		for range 5 {
			_, err := txRepo.Create(ctx, tx, domain.InventoryTransaction{
				TenantID:      tenantA,
				BranchID:      branchID,
				ProductID:     uuid.New(),
				Type:          domain.TransactionTypeRestock,
				QuantityDelta: 10,
			})
			require.NoError(t, err)
		}
	})

	withReadTx(t, tenantA, func(tx pgx.Tx) {
		txs, err := txRepo.ListByBranch(ctx, tx, branchID, 100)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(txs), 5)
	})
}

func TestInventoryTransactionRepo_ReferenceID(t *testing.T) {
	txRepo := repo.NewInventoryTransactionRepo()
	ctx := context.Background()
	prodID := uuid.New()
	branchID := uuid.New()
	orderID := uuid.New()
	refType := "order"

	var created domain.InventoryTransaction
	withTx(t, tenantA, func(tx pgx.Tx) {
		var err error
		created, err = txRepo.Create(ctx, tx, domain.InventoryTransaction{
			TenantID:      tenantA,
			BranchID:      branchID,
			ProductID:     prodID,
			Type:          domain.TransactionTypeConsumption,
			QuantityDelta: -3,
			ReferenceID:   &orderID,
			ReferenceType: &refType,
		})
		require.NoError(t, err)
	})

	assert.Equal(t, &orderID, created.ReferenceID)
	assert.Equal(t, &refType, created.ReferenceType)
}
