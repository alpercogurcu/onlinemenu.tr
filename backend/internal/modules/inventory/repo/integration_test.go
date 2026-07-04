package repo_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

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
// Fixture helpers: stock_levels/stock_movements now carry a real FK to
// warehouses/stock_items (intra-module FK, added in migration 000003), so
// tests create those parent rows first.
// ============================================================

func createWarehouse(t *testing.T, tenantID uuid.UUID) uuid.UUID {
	t.Helper()
	whRepo := repo.NewWarehouseRepo()
	var id uuid.UUID
	withTx(t, tenantID, func(tx pgx.Tx) {
		wh, err := whRepo.Create(context.Background(), tx, domain.Warehouse{
			TenantID:      tenantID,
			BranchID:      branchA,
			Name:          "Test Warehouse " + uuid.NewString(),
			WarehouseType: domain.WarehouseTypeDepo,
			IsActive:      true,
		})
		require.NoError(t, err)
		id = wh.ID
	})
	return id
}

func createStockItem(t *testing.T, tenantID uuid.UUID) uuid.UUID {
	t.Helper()
	itemRepo := repo.NewStockItemRepo()
	newID, err := uuid.NewV7()
	require.NoError(t, err)
	withTx(t, tenantID, func(tx pgx.Tx) {
		item, err := itemRepo.Create(context.Background(), tx, domain.StockItem{
			ID:            newID,
			TenantID:      tenantID,
			SKU:           "SKU-" + uuid.NewString(),
			Name:          "Test Item",
			Kind:          domain.StockItemKindRaw,
			CanonicalUnit: "kg",
			IsActive:      true,
		})
		require.NoError(t, err)
		newID = item.ID
	})
	return newID
}

// ============================================================
// StockLevelRepo tests
// ============================================================

func TestStockLevelRepo_AdjustAndGet(t *testing.T) {
	lvlRepo := repo.NewStockLevelRepo()
	ctx := context.Background()
	warehouseID := createWarehouse(t, tenantA)
	itemID := createStockItem(t, tenantA)

	var lvl domain.StockLevel
	withTx(t, tenantA, func(tx pgx.Tx) {
		var err error
		lvl, err = lvlRepo.AdjustOnHand(ctx, tx, tenantA, warehouseID, itemID, 50.5, "kg")
		require.NoError(t, err)
	})

	assert.NotEqual(t, uuid.Nil, lvl.ID)
	assert.InDelta(t, 50.5, lvl.OnHand, 0.001)
	assert.InDelta(t, 50.5, lvl.Available, 0.001)
	assert.Equal(t, warehouseID, lvl.WarehouseID)

	withReadTx(t, tenantA, func(tx pgx.Tx) {
		got, err := lvlRepo.GetByStockItem(ctx, tx, warehouseID, itemID)
		require.NoError(t, err)
		assert.InDelta(t, 50.5, got.OnHand, 0.001)
	})
}

func TestStockLevelRepo_AdjustOnHandClampsAtZero(t *testing.T) {
	lvlRepo := repo.NewStockLevelRepo()
	ctx := context.Background()
	warehouseID := createWarehouse(t, tenantA)
	itemID := createStockItem(t, tenantA)

	var lvl domain.StockLevel
	withTx(t, tenantA, func(tx pgx.Tx) {
		var err error
		lvl, err = lvlRepo.AdjustOnHand(ctx, tx, tenantA, warehouseID, itemID, 100, "kg")
		require.NoError(t, err)
	})
	assert.InDelta(t, 100.0, lvl.OnHand, 0.001)

	withTx(t, tenantA, func(tx pgx.Tx) {
		var err error
		lvl, err = lvlRepo.AdjustOnHand(ctx, tx, tenantA, warehouseID, itemID, -30, "kg")
		require.NoError(t, err)
	})
	assert.InDelta(t, 70.0, lvl.OnHand, 0.001)

	withTx(t, tenantA, func(tx pgx.Tx) {
		var err error
		lvl, err = lvlRepo.AdjustOnHand(ctx, tx, tenantA, warehouseID, itemID, -999, "kg")
		require.NoError(t, err)
	})
	assert.InDelta(t, 0.0, lvl.OnHand, 0.001)
}

func TestStockLevelRepo_AdjustReserved_AvailableDerived(t *testing.T) {
	lvlRepo := repo.NewStockLevelRepo()
	ctx := context.Background()
	warehouseID := createWarehouse(t, tenantA)
	itemID := createStockItem(t, tenantA)

	withTx(t, tenantA, func(tx pgx.Tx) {
		_, err := lvlRepo.AdjustOnHand(ctx, tx, tenantA, warehouseID, itemID, 100, "kg")
		require.NoError(t, err)
	})

	var lvl domain.StockLevel
	withTx(t, tenantA, func(tx pgx.Tx) {
		var err error
		lvl, err = lvlRepo.AdjustReserved(ctx, tx, tenantA, warehouseID, itemID, 20, "kg")
		require.NoError(t, err)
	})
	assert.InDelta(t, 100.0, lvl.OnHand, 0.001)
	assert.InDelta(t, 20.0, lvl.Reserved, 0.001)
	// available is DB-generated (on_hand - reserved); never set by app code.
	assert.InDelta(t, 80.0, lvl.Available, 0.001)
}

func TestStockLevelRepo_TenantIsolation(t *testing.T) {
	lvlRepo := repo.NewStockLevelRepo()
	ctx := context.Background()
	warehouseID := createWarehouse(t, tenantA)
	itemID := createStockItem(t, tenantA)

	withTx(t, tenantA, func(tx pgx.Tx) {
		_, err := lvlRepo.AdjustOnHand(ctx, tx, tenantA, warehouseID, itemID, 42, "kg")
		require.NoError(t, err)
	})

	// tenantB should not see it (RLS hides the row entirely; a cross-tenant
	// warehouse_id/stock_item_id pair does not exist from tenantB's view).
	withReadTx(t, tenantB, func(tx pgx.Tx) {
		_, err := lvlRepo.GetByStockItem(ctx, tx, warehouseID, itemID)
		assert.ErrorIs(t, err, repo.ErrNotFound)
	})
}

func TestStockLevelRepo_ListByWarehouse(t *testing.T) {
	lvlRepo := repo.NewStockLevelRepo()
	ctx := context.Background()
	warehouseID := createWarehouse(t, tenantA)

	withTx(t, tenantA, func(tx pgx.Tx) {
		for i := range 3 {
			itemID := createStockItemInTx(t, tx, tenantA)
			_, err := lvlRepo.AdjustOnHand(ctx, tx, tenantA, warehouseID, itemID, float64(i+1), "kg")
			require.NoError(t, err)
		}
	})

	withReadTx(t, tenantA, func(tx pgx.Tx) {
		levels, err := lvlRepo.ListByWarehouse(ctx, tx, warehouseID)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(levels), 3)
	})
}

// createStockItemInTx creates a stock item using an already-open tx (for tests
// that build several fixtures within a single withTx block).
func createStockItemInTx(t *testing.T, tx pgx.Tx, tenantID uuid.UUID) uuid.UUID {
	t.Helper()
	itemRepo := repo.NewStockItemRepo()
	newID, err := uuid.NewV7()
	require.NoError(t, err)
	item, err := itemRepo.Create(context.Background(), tx, domain.StockItem{
		ID:            newID,
		TenantID:      tenantID,
		SKU:           "SKU-" + uuid.NewString(),
		Name:          "Test Item",
		Kind:          domain.StockItemKindRaw,
		CanonicalUnit: "kg",
		IsActive:      true,
	})
	require.NoError(t, err)
	return item.ID
}

// ============================================================
// StockMovementRepo tests
// ============================================================

func TestStockMovementRepo_CreateAndList(t *testing.T) {
	mvRepo := repo.NewStockMovementRepo()
	ctx := context.Background()
	warehouseID := createWarehouse(t, tenantA)
	itemID := createStockItem(t, tenantA)

	notes := "initial in"
	var created domain.StockMovement
	withTx(t, tenantA, func(tx pgx.Tx) {
		var err error
		created, err = mvRepo.Create(ctx, tx, domain.StockMovement{
			TenantID:    tenantA,
			WarehouseID: warehouseID,
			StockItemID: itemID,
			Type:        domain.MovementTypeIn,
			Quantity:    100,
			Notes:       &notes,
		})
		require.NoError(t, err)
	})

	assert.NotEqual(t, uuid.Nil, created.ID)
	assert.Equal(t, domain.MovementTypeIn, created.Type)
	assert.InDelta(t, 100.0, created.Quantity, 0.001)
	assert.Equal(t, &notes, created.Notes)

	withReadTx(t, tenantA, func(tx pgx.Tx) {
		mvs, err := mvRepo.ListByStockItem(ctx, tx, warehouseID, itemID, 10)
		require.NoError(t, err)
		require.Len(t, mvs, 1)
		assert.Equal(t, created.ID, mvs[0].ID)
	})
}

func TestStockMovementRepo_MultipleTypes(t *testing.T) {
	mvRepo := repo.NewStockMovementRepo()
	ctx := context.Background()
	warehouseID := createWarehouse(t, tenantA)
	itemID := createStockItem(t, tenantA)

	movements := []struct {
		typ domain.MovementType
		qty float64
	}{
		{domain.MovementTypeIn, 200},
		{domain.MovementTypeOut, 50},
		{domain.MovementTypeReserve, 10},
		{domain.MovementTypeAdjust, -5},
	}

	withTx(t, tenantA, func(tx pgx.Tx) {
		for _, m := range movements {
			_, err := mvRepo.Create(ctx, tx, domain.StockMovement{
				TenantID:    tenantA,
				WarehouseID: warehouseID,
				StockItemID: itemID,
				Type:        m.typ,
				Quantity:    m.qty,
			})
			require.NoError(t, err)
		}
	})

	withReadTx(t, tenantA, func(tx pgx.Tx) {
		mvs, err := mvRepo.ListByStockItem(ctx, tx, warehouseID, itemID, 100)
		require.NoError(t, err)
		assert.Len(t, mvs, len(movements))
		types := make(map[domain.MovementType]bool, len(mvs))
		for _, m := range mvs {
			types[m.Type] = true
		}
		for _, m := range movements {
			assert.True(t, types[m.typ], "expected type %q in results", m.typ)
		}
	})
}

func TestStockMovementRepo_TenantIsolation(t *testing.T) {
	mvRepo := repo.NewStockMovementRepo()
	ctx := context.Background()
	warehouseID := createWarehouse(t, tenantA)
	itemID := createStockItem(t, tenantA)

	withTx(t, tenantA, func(tx pgx.Tx) {
		_, err := mvRepo.Create(ctx, tx, domain.StockMovement{
			TenantID:    tenantA,
			WarehouseID: warehouseID,
			StockItemID: itemID,
			Type:        domain.MovementTypeIn,
			Quantity:    50,
		})
		require.NoError(t, err)
	})

	withReadTx(t, tenantB, func(tx pgx.Tx) {
		mvs, err := mvRepo.ListByStockItem(ctx, tx, warehouseID, itemID, 10)
		require.NoError(t, err)
		assert.Empty(t, mvs)
	})
}

func TestStockMovementRepo_ListByWarehouse(t *testing.T) {
	mvRepo := repo.NewStockMovementRepo()
	ctx := context.Background()
	warehouseID := createWarehouse(t, tenantA)

	withTx(t, tenantA, func(tx pgx.Tx) {
		for range 5 {
			itemID := createStockItemInTx(t, tx, tenantA)
			_, err := mvRepo.Create(ctx, tx, domain.StockMovement{
				TenantID:    tenantA,
				WarehouseID: warehouseID,
				StockItemID: itemID,
				Type:        domain.MovementTypeIn,
				Quantity:    10,
			})
			require.NoError(t, err)
		}
	})

	withReadTx(t, tenantA, func(tx pgx.Tx) {
		mvs, err := mvRepo.ListByWarehouse(ctx, tx, warehouseID, 100)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(mvs), 5)
	})
}

func TestStockMovementRepo_ReferenceID(t *testing.T) {
	mvRepo := repo.NewStockMovementRepo()
	ctx := context.Background()
	warehouseID := createWarehouse(t, tenantA)
	itemID := createStockItem(t, tenantA)
	shipmentID := uuid.New()
	refType := "shipment"

	var created domain.StockMovement
	withTx(t, tenantA, func(tx pgx.Tx) {
		var err error
		created, err = mvRepo.Create(ctx, tx, domain.StockMovement{
			TenantID:      tenantA,
			WarehouseID:   warehouseID,
			StockItemID:   itemID,
			Type:          domain.MovementTypeOut,
			Quantity:      3,
			ReferenceID:   &shipmentID,
			ReferenceType: &refType,
		})
		require.NoError(t, err)
	})

	assert.Equal(t, &shipmentID, created.ReferenceID)
	assert.Equal(t, &refType, created.ReferenceType)
}

// ============================================================
// StockItemRepo tests
// ============================================================

func TestStockItemRepo_CreateGetUpdateDelete(t *testing.T) {
	itemRepo := repo.NewStockItemRepo()
	ctx := context.Background()
	newID, err := uuid.NewV7()
	require.NoError(t, err)

	var created domain.StockItem
	withTx(t, tenantA, func(tx pgx.Tx) {
		var err error
		created, err = itemRepo.Create(ctx, tx, domain.StockItem{
			ID:            newID,
			TenantID:      tenantA,
			SKU:           "SKU-CREATE-" + uuid.NewString(),
			Name:          "Un",
			Kind:          domain.StockItemKindRaw,
			CanonicalUnit: "kg",
			Category:      "meat",
			IsActive:      true,
		})
		require.NoError(t, err)
	})
	assert.Equal(t, newID, created.ID)
	assert.Equal(t, domain.StockItemKindRaw, created.Kind)

	withReadTx(t, tenantA, func(tx pgx.Tx) {
		got, err := itemRepo.GetByID(ctx, tx, newID)
		require.NoError(t, err)
		assert.Equal(t, "Un", got.Name)
	})

	withTx(t, tenantA, func(tx pgx.Tx) {
		updated, err := itemRepo.Update(ctx, tx, domain.StockItem{
			ID:            newID,
			SKU:           created.SKU,
			Name:          "Un (güncellendi)",
			Kind:          domain.StockItemKindRaw,
			CanonicalUnit: "kg",
			Category:      "meat",
			IsActive:      true,
		})
		require.NoError(t, err)
		assert.Equal(t, "Un (güncellendi)", updated.Name)
	})

	withTx(t, tenantA, func(tx pgx.Tx) {
		require.NoError(t, itemRepo.Delete(ctx, tx, newID))
	})

	withReadTx(t, tenantA, func(tx pgx.Tx) {
		got, err := itemRepo.GetByID(ctx, tx, newID)
		require.NoError(t, err)
		assert.False(t, got.IsActive)
	})
}

func TestStockItemRepo_TenantIsolation(t *testing.T) {
	itemRepo := repo.NewStockItemRepo()
	ctx := context.Background()
	itemID := createStockItem(t, tenantA)

	withReadTx(t, tenantB, func(tx pgx.Tx) {
		_, err := itemRepo.GetByID(ctx, tx, itemID)
		assert.ErrorIs(t, err, repo.ErrNotFound)
	})
}

func TestStockItemRepo_ListFiltersByKind(t *testing.T) {
	itemRepo := repo.NewStockItemRepo()
	ctx := context.Background()

	withTx(t, tenantA, func(tx pgx.Tx) {
		for _, kind := range []domain.StockItemKind{domain.StockItemKindRaw, domain.StockItemKindFinished} {
			id, err := uuid.NewV7()
			require.NoError(t, err)
			_, err = itemRepo.Create(ctx, tx, domain.StockItem{
				ID:            id,
				TenantID:      tenantA,
				SKU:           "SKU-KIND-" + uuid.NewString(),
				Name:          "Item",
				Kind:          kind,
				CanonicalUnit: "adet",
				IsActive:      true,
			})
			require.NoError(t, err)
		}
	})

	withReadTx(t, tenantA, func(tx pgx.Tx) {
		items, err := itemRepo.List(ctx, tx, domain.StockItemKindFinished)
		require.NoError(t, err)
		for _, it := range items {
			assert.Equal(t, domain.StockItemKindFinished, it.Kind)
		}
	})
}

// ============================================================
// WarehouseRepo tests
// ============================================================

func TestWarehouseRepo_CreateGetUpdateDelete(t *testing.T) {
	whRepo := repo.NewWarehouseRepo()
	ctx := context.Background()

	var created domain.Warehouse
	withTx(t, tenantA, func(tx pgx.Tx) {
		var err error
		created, err = whRepo.Create(ctx, tx, domain.Warehouse{
			TenantID:      tenantA,
			BranchID:      branchA,
			Name:          "Merkez Depo",
			WarehouseType: domain.WarehouseTypeDepo,
			IsActive:      true,
		})
		require.NoError(t, err)
	})
	assert.NotEqual(t, uuid.Nil, created.ID)

	withReadTx(t, tenantA, func(tx pgx.Tx) {
		got, err := whRepo.GetByID(ctx, tx, created.ID)
		require.NoError(t, err)
		assert.Equal(t, "Merkez Depo", got.Name)
	})

	withTx(t, tenantA, func(tx pgx.Tx) {
		updated, err := whRepo.Update(ctx, tx, domain.Warehouse{
			ID:            created.ID,
			BranchID:      branchA,
			Name:          "Merkez Depo (Yeni)",
			WarehouseType: domain.WarehouseTypeImalat,
			IsActive:      true,
		})
		require.NoError(t, err)
		assert.Equal(t, domain.WarehouseTypeImalat, updated.WarehouseType)
	})

	withTx(t, tenantA, func(tx pgx.Tx) {
		require.NoError(t, whRepo.Delete(ctx, tx, created.ID))
	})

	withReadTx(t, tenantA, func(tx pgx.Tx) {
		got, err := whRepo.GetByID(ctx, tx, created.ID)
		require.NoError(t, err)
		assert.False(t, got.IsActive)
	})
}

func TestWarehouseRepo_TenantIsolation(t *testing.T) {
	whRepo := repo.NewWarehouseRepo()
	ctx := context.Background()
	id := createWarehouse(t, tenantA)

	withReadTx(t, tenantB, func(tx pgx.Tx) {
		_, err := whRepo.GetByID(ctx, tx, id)
		assert.ErrorIs(t, err, repo.ErrNotFound)
	})
}

func TestWarehouseRepo_ListByBranch(t *testing.T) {
	whRepo := repo.NewWarehouseRepo()
	ctx := context.Background()
	testBranch := uuid.New()

	withTx(t, tenantA, func(tx pgx.Tx) {
		for i := range 2 {
			_, err := whRepo.Create(ctx, tx, domain.Warehouse{
				TenantID:      tenantA,
				BranchID:      testBranch,
				Name:          fmt.Sprintf("Depo %d", i),
				WarehouseType: domain.WarehouseTypeDepo,
				IsActive:      true,
			})
			require.NoError(t, err)
		}
	})

	withReadTx(t, tenantA, func(tx pgx.Tx) {
		list, err := whRepo.List(ctx, tx, testBranch)
		require.NoError(t, err)
		assert.Len(t, list, 2)
	})
}

// ============================================================
// SupplyPolicyRepo tests (ADR-DATA-007)
// ============================================================

// NOTE: unlike most repo tests in this file, SupplyPolicyRepo's ListAll and
// ListCandidates queries are NOT scoped down to a single id the test itself
// picked (e.g. GetByID, or List filtered by a distinctive kind) — they
// return every row visible under a tenant. Reusing the shared tenantA/
// tenantB fixtures (used freely elsewhere in this file) would leak rows
// across these tests, since several of them run under the same tenant in
// the same test binary. Each test below therefore mints its own fresh
// tenant id.

func TestSupplyPolicyRepo_CreateAndListAll(t *testing.T) {
	spRepo := repo.NewSupplyPolicyRepo()
	ctx := context.Background()
	tenantID := uuid.New()
	itemID := createStockItem(t, tenantID)
	supplierA := uuid.New()
	supplierB := uuid.New()

	var created domain.SupplyPolicy
	var createErr error
	withTx(t, tenantID, func(tx pgx.Tx) {
		id, err := uuid.NewV7()
		require.NoError(t, err)
		created, createErr = spRepo.Create(ctx, tx, domain.SupplyPolicy{
			ID:                  id,
			TenantID:            tenantID,
			Scope:               domain.SupplyScopeStockItem,
			StockItemID:         &itemID,
			Mode:                domain.SupplyModeApprovedSuppliers,
			ApprovedSupplierIDs: []uuid.UUID{supplierA, supplierB},
			EffectiveFrom:       time.Now(),
		})
	})
	require.NoError(t, createErr)
	assert.Equal(t, domain.SupplyScopeStockItem, created.Scope)
	assert.Equal(t, domain.SupplyModeApprovedSuppliers, created.Mode)
	assert.Equal(t, []uuid.UUID{supplierA, supplierB}, created.ApprovedSupplierIDs)
	assert.Nil(t, created.BranchID)

	var all []domain.SupplyPolicy
	var listErr error
	withReadTx(t, tenantID, func(tx pgx.Tx) {
		all, listErr = spRepo.ListAll(ctx, tx)
	})
	require.NoError(t, listErr)
	require.Len(t, all, 1)
	assert.Equal(t, created.ID, all[0].ID)
}

func TestSupplyPolicyRepo_TenantIsolation(t *testing.T) {
	spRepo := repo.NewSupplyPolicyRepo()
	ctx := context.Background()
	tenantOwn := uuid.New()
	tenantOther := uuid.New()
	itemID := createStockItem(t, tenantOwn)

	withTx(t, tenantOwn, func(tx pgx.Tx) {
		id, err := uuid.NewV7()
		require.NoError(t, err)
		_, err = spRepo.Create(ctx, tx, domain.SupplyPolicy{
			ID: id, TenantID: tenantOwn, Scope: domain.SupplyScopeStockItem, StockItemID: &itemID,
			Mode: domain.SupplyModeFree, EffectiveFrom: time.Now(),
		})
		require.NoError(t, err)
	})

	var all []domain.SupplyPolicy
	var listErr error
	withReadTx(t, tenantOther, func(tx pgx.Tx) {
		all, listErr = spRepo.ListAll(ctx, tx)
	})
	require.NoError(t, listErr)
	assert.Empty(t, all, "a different tenant must not see this tenant's supply policy rows")
}

// TestSupplyPolicyRepo_ListCandidates_BranchScoping proves ListCandidates
// returns tenant-wide rows unconditionally plus only the rows scoped to the
// requested branch — never a different branch's override.
func TestSupplyPolicyRepo_ListCandidates_BranchScoping(t *testing.T) {
	spRepo := repo.NewSupplyPolicyRepo()
	ctx := context.Background()
	tenantID := uuid.New()
	itemID := createStockItem(t, tenantID)
	ownBranch := uuid.New()
	otherBranch := uuid.New()

	withTx(t, tenantID, func(tx pgx.Tx) {
		tenantWideID, err := uuid.NewV7()
		require.NoError(t, err)
		_, err = spRepo.Create(ctx, tx, domain.SupplyPolicy{
			ID: tenantWideID, TenantID: tenantID, Scope: domain.SupplyScopeTenantDefault,
			Mode: domain.SupplyModeExclusiveHQ, EffectiveFrom: time.Now(),
		})
		require.NoError(t, err)

		ownBranchID, err := uuid.NewV7()
		require.NoError(t, err)
		_, err = spRepo.Create(ctx, tx, domain.SupplyPolicy{
			ID: ownBranchID, TenantID: tenantID, BranchID: &ownBranch, Scope: domain.SupplyScopeStockItem,
			StockItemID: &itemID, Mode: domain.SupplyModeFree, EffectiveFrom: time.Now(),
		})
		require.NoError(t, err)

		otherBranchRowID, err := uuid.NewV7()
		require.NoError(t, err)
		_, err = spRepo.Create(ctx, tx, domain.SupplyPolicy{
			ID: otherBranchRowID, TenantID: tenantID, BranchID: &otherBranch, Scope: domain.SupplyScopeStockItem,
			StockItemID: &itemID, Mode: domain.SupplyModeApprovedSuppliers, EffectiveFrom: time.Now(),
		})
		require.NoError(t, err)
	})

	var candidates []domain.SupplyPolicy
	var listErr error
	withReadTx(t, tenantID, func(tx pgx.Tx) {
		candidates, listErr = spRepo.ListCandidates(ctx, tx, ownBranch)
	})
	require.NoError(t, listErr)
	require.Len(t, candidates, 2, "must include the tenant-wide row plus ownBranch's row, but not otherBranch's")
	for _, c := range candidates {
		if c.BranchID != nil {
			assert.Equal(t, ownBranch, *c.BranchID)
		}
	}
}
