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

	"onlinemenu.tr/internal/modules/catalog/domain"
	"onlinemenu.tr/internal/modules/catalog/repo"
	"onlinemenu.tr/internal/platform/db"
)

var (
	sharedPool *db.Pool
	tenantA    = uuid.MustParse("aaaaaaaa-0000-0000-0000-000000000001")
	tenantB    = uuid.MustParse("bbbbbbbb-0000-0000-0000-000000000001")
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
	// file = .../backend/internal/modules/catalog/repo/integration_test.go
	// walk up 4 directories: repo/ → catalog/ → modules/ → internal/ → backend/
	base := filepath.Dir(file)
	for range 4 {
		base = filepath.Dir(base)
	}
	return filepath.Join(base, "migrations")
}

func runMigrations(superDSN string) error {
	// Migrations must run as app_migrator so that DEFAULT PRIVILEGES apply and
	// tables are owned by app_migrator (enabling app_runtime SELECT/INSERT/...).
	cfg, err := pgxpool.ParseConfig(superDSN)
	if err != nil {
		return fmt.Errorf("parse dsn: %w", err)
	}
	cfg.ConnConfig.User = "app_migrator"
	cfg.ConnConfig.Password = "migrator_secret"

	// Rebuild the DSN string with migrator credentials for golang-migrate.
	migratorDSN := fmt.Sprintf("pgx5://%s:%s@%s/%s?sslmode=disable",
		cfg.ConnConfig.User, cfg.ConnConfig.Password,
		cfg.ConnConfig.Host+fmt.Sprintf(":%d", cfg.ConnConfig.Port),
		cfg.ConnConfig.Database,
	)

	migrateModules := []string{"tenant", "identity", "catalog"}
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

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ---------------------------------------------------------------------------
// Category tests
// ---------------------------------------------------------------------------

func TestCategoryRepo_CreateAndGet(t *testing.T) {
	ctx := context.Background()
	r := repo.NewCategoryRepo()

	cat := domain.Category{
		TenantID:  tenantA,
		Name:      "Başlangıçlar",
		IsActive:  true,
		SortOrder: 1,
	}

	var created domain.Category
	require.NoError(t, sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		var err error
		created, err = r.Create(ctx, tx, cat)
		return err
	}))

	assert.NotEqual(t, uuid.Nil, created.ID)
	assert.Equal(t, "Başlangıçlar", created.Name)
	assert.Equal(t, tenantA, created.TenantID)

	// GetByID
	var fetched domain.Category
	require.NoError(t, sharedPool.WithTenantReadTx(ctx, tenantA, func(tx pgx.Tx) error {
		var err error
		fetched, err = r.GetByID(ctx, tx, created.ID)
		return err
	}))
	assert.Equal(t, created.ID, fetched.ID)
	assert.Equal(t, "Başlangıçlar", fetched.Name)
}

func TestCategoryRepo_TenantIsolation(t *testing.T) {
	ctx := context.Background()
	r := repo.NewCategoryRepo()

	// Create a category for tenantB
	var catB domain.Category
	require.NoError(t, sharedPool.WithTenantTx(ctx, tenantB, func(tx pgx.Tx) error {
		var err error
		catB, err = r.Create(ctx, tx, domain.Category{
			TenantID: tenantB,
			Name:     "tenantB-cat",
			IsActive: true,
		})
		return err
	}))

	// tenantA must not see tenantB's category
	require.NoError(t, sharedPool.WithTenantReadTx(ctx, tenantA, func(tx pgx.Tx) error {
		_, err := r.GetByID(ctx, tx, catB.ID)
		assert.ErrorIs(t, err, repo.ErrNotFound, "tenantA must not see tenantB category")
		return nil
	}))
}

func TestCategoryRepo_List(t *testing.T) {
	ctx := context.Background()
	r := repo.NewCategoryRepo()

	tid := uuid.New()
	names := []string{"Ana Yemekler", "Tatlılar", "İçecekler"}
	for _, n := range names {
		require.NoError(t, sharedPool.WithTenantTx(ctx, tid, func(tx pgx.Tx) error {
			_, err := r.Create(ctx, tx, domain.Category{TenantID: tid, Name: n, IsActive: true})
			return err
		}))
	}

	var list []domain.Category
	require.NoError(t, sharedPool.WithTenantReadTx(ctx, tid, func(tx pgx.Tx) error {
		var err error
		list, err = r.List(ctx, tx)
		return err
	}))
	assert.Len(t, list, len(names))
}

// ---------------------------------------------------------------------------
// Product tests
// ---------------------------------------------------------------------------

func TestProductRepo_CreateAndGet(t *testing.T) {
	ctx := context.Background()
	pr := repo.NewProductRepo()
	cr := repo.NewCategoryRepo()

	tid := uuid.New()

	// Create a category first
	var cat domain.Category
	require.NoError(t, sharedPool.WithTenantTx(ctx, tid, func(tx pgx.Tx) error {
		var err error
		cat, err = cr.Create(ctx, tx, domain.Category{TenantID: tid, Name: "Test Kat", IsActive: true})
		return err
	}))

	p := domain.Product{
		TenantID:             tid,
		CategoryID:           &cat.ID,
		Name:                 "Adana Kebap",
		PriceAmount:          18000, // 180 TL
		Currency:             "TRY",
		Unit:                 "porsiyon",
		TaxRateBPS:           1000, // %10
		IsActive:             true,
		AutoCloseOnZeroStock: true,
	}

	var created domain.Product
	require.NoError(t, sharedPool.WithTenantTx(ctx, tid, func(tx pgx.Tx) error {
		var err error
		created, err = pr.Create(ctx, tx, p)
		return err
	}))

	assert.NotEqual(t, uuid.Nil, created.ID)
	assert.Equal(t, int64(18000), created.PriceAmount)
	assert.True(t, created.AutoCloseOnZeroStock)

	var fetched domain.Product
	require.NoError(t, sharedPool.WithTenantReadTx(ctx, tid, func(tx pgx.Tx) error {
		var err error
		fetched, err = pr.GetByID(ctx, tx, created.ID)
		return err
	}))
	assert.Equal(t, "Adana Kebap", fetched.Name)
	require.NotNil(t, fetched.CategoryID)
	assert.Equal(t, cat.ID, *fetched.CategoryID)
}

// TestProductRepo_SourceStockItemID proves the ADR-DATA-005 catalog<->inventory
// link column round-trips through Create/Get/Update, and defaults to NULL for
// products with no stock backing (pure service/combo products).
func TestProductRepo_SourceStockItemID(t *testing.T) {
	ctx := context.Background()
	pr := repo.NewProductRepo()
	tid := uuid.New()
	stockItemID := uuid.New() // no FK: cross-module reference (migrations/catalog/000002)

	p := domain.Product{
		TenantID:    tid,
		Name:        "Şubede Üretilen Köfte Paketi",
		PriceAmount: 9000,
		Currency:    "TRY",
		Unit:        "adet",
		IsActive:    true,
	}

	var created domain.Product
	require.NoError(t, sharedPool.WithTenantTx(ctx, tid, func(tx pgx.Tx) error {
		var err error
		created, err = pr.Create(ctx, tx, p)
		return err
	}))
	assert.Nil(t, created.SourceStockItemID, "products default to no stock backing")

	created.SourceStockItemID = &stockItemID
	var updated domain.Product
	require.NoError(t, sharedPool.WithTenantTx(ctx, tid, func(tx pgx.Tx) error {
		var err error
		updated, err = pr.Update(ctx, tx, created)
		return err
	}))
	require.NotNil(t, updated.SourceStockItemID)
	assert.Equal(t, stockItemID, *updated.SourceStockItemID)

	var fetched domain.Product
	require.NoError(t, sharedPool.WithTenantReadTx(ctx, tid, func(tx pgx.Tx) error {
		var err error
		fetched, err = pr.GetByID(ctx, tx, created.ID)
		return err
	}))
	require.NotNil(t, fetched.SourceStockItemID)
	assert.Equal(t, stockItemID, *fetched.SourceStockItemID)
}

func TestProductRepo_TenantIsolation(t *testing.T) {
	ctx := context.Background()
	pr := repo.NewProductRepo()

	tidX := uuid.New()
	tidY := uuid.New()

	var prodX domain.Product
	require.NoError(t, sharedPool.WithTenantTx(ctx, tidX, func(tx pgx.Tx) error {
		var err error
		prodX, err = pr.Create(ctx, tx, domain.Product{
			TenantID:    tidX,
			Name:        "X Product",
			PriceAmount: 1000,
			Currency:    "TRY",
			Unit:        "adet",
			TaxRateBPS:  1800,
			IsActive:    true,
		})
		return err
	}))

	// tidY must not see tidX's product
	require.NoError(t, sharedPool.WithTenantReadTx(ctx, tidY, func(tx pgx.Tx) error {
		_, err := pr.GetByID(ctx, tx, prodX.ID)
		assert.ErrorIs(t, err, repo.ErrNotFound)
		return nil
	}))
}

func TestProductRepo_UpdateAndDelete(t *testing.T) {
	ctx := context.Background()
	pr := repo.NewProductRepo()
	tid := uuid.New()

	var p domain.Product
	require.NoError(t, sharedPool.WithTenantTx(ctx, tid, func(tx pgx.Tx) error {
		var err error
		p, err = pr.Create(ctx, tx, domain.Product{
			TenantID:    tid,
			Name:        "Lahmacun",
			PriceAmount: 5000,
			Currency:    "TRY",
			Unit:        "adet",
			TaxRateBPS:  1800,
			IsActive:    true,
		})
		return err
	}))

	// Update
	p.Name = "Lahmacun XL"
	p.PriceAmount = 7500
	require.NoError(t, sharedPool.WithTenantTx(ctx, tid, func(tx pgx.Tx) error {
		var err error
		p, err = pr.Update(ctx, tx, p)
		return err
	}))
	assert.Equal(t, "Lahmacun XL", p.Name)
	assert.Equal(t, int64(7500), p.PriceAmount)

	// Delete (soft delete via is_active=false)
	require.NoError(t, sharedPool.WithTenantTx(ctx, tid, func(tx pgx.Tx) error {
		return pr.Delete(ctx, tx, p.ID)
	}))

	var deleted domain.Product
	require.NoError(t, sharedPool.WithTenantReadTx(ctx, tid, func(tx pgx.Tx) error {
		var err error
		deleted, err = pr.GetByID(ctx, tx, p.ID)
		return err
	}))
	assert.False(t, deleted.IsActive)
}

// ---------------------------------------------------------------------------
// Modifier group tests
// ---------------------------------------------------------------------------

func TestModifierGroupRepo_CreateAndGet(t *testing.T) {
	ctx := context.Background()
	gr := repo.NewModifierGroupRepo()
	tid := uuid.New()

	maxSel := int16(3)
	var created domain.ModifierGroup
	require.NoError(t, sharedPool.WithTenantTx(ctx, tid, func(tx pgx.Tx) error {
		var err error
		created, err = gr.Create(ctx, tx, domain.ModifierGroup{
			TenantID:      tid,
			Name:          "Soslar",
			SelectionType: domain.SelectionMultiple,
			MinSelections: 0,
			MaxSelections: &maxSel,
			IsRequired:    false,
			SortOrder:     1,
		})
		return err
	}))

	assert.NotEqual(t, uuid.Nil, created.ID)
	assert.Equal(t, "Soslar", created.Name)
	assert.Equal(t, domain.SelectionMultiple, created.SelectionType)
	require.NotNil(t, created.MaxSelections)
	assert.Equal(t, int16(3), *created.MaxSelections)

	var fetched domain.ModifierGroup
	require.NoError(t, sharedPool.WithTenantReadTx(ctx, tid, func(tx pgx.Tx) error {
		var err error
		fetched, err = gr.GetByID(ctx, tx, created.ID)
		return err
	}))
	assert.Equal(t, created.ID, fetched.ID)
}

func TestModifierGroupRepo_TenantIsolation(t *testing.T) {
	ctx := context.Background()
	gr := repo.NewModifierGroupRepo()

	tidX := uuid.New()
	tidY := uuid.New()

	var gX domain.ModifierGroup
	require.NoError(t, sharedPool.WithTenantTx(ctx, tidX, func(tx pgx.Tx) error {
		var err error
		gX, err = gr.Create(ctx, tx, domain.ModifierGroup{
			TenantID:      tidX,
			Name:          "X-Soslar",
			SelectionType: domain.SelectionSingle,
		})
		return err
	}))

	require.NoError(t, sharedPool.WithTenantReadTx(ctx, tidY, func(tx pgx.Tx) error {
		_, err := gr.GetByID(ctx, tx, gX.ID)
		assert.ErrorIs(t, err, repo.ErrNotFound, "tenantY must not see tenantX group")
		return nil
	}))
}

func TestModifierGroupRepo_DeleteCascadesToModifiers(t *testing.T) {
	ctx := context.Background()
	gr := repo.NewModifierGroupRepo()
	mr := repo.NewModifierRepo()
	tid := uuid.New()

	var g domain.ModifierGroup
	require.NoError(t, sharedPool.WithTenantTx(ctx, tid, func(tx pgx.Tx) error {
		var err error
		g, err = gr.Create(ctx, tx, domain.ModifierGroup{
			TenantID: tid, Name: "Boyut", SelectionType: domain.SelectionSingle,
		})
		return err
	}))

	require.NoError(t, sharedPool.WithTenantTx(ctx, tid, func(tx pgx.Tx) error {
		_, err := mr.Create(ctx, tx, domain.Modifier{
			TenantID: tid, GroupID: g.ID, Name: "Küçük", IsActive: true,
		})
		return err
	}))

	require.NoError(t, sharedPool.WithTenantTx(ctx, tid, func(tx pgx.Tx) error {
		return gr.Delete(ctx, tx, g.ID)
	}))

	// After group delete, modifiers must be gone (CASCADE)
	var modifiers []domain.Modifier
	require.NoError(t, sharedPool.WithTenantReadTx(ctx, tid, func(tx pgx.Tx) error {
		var err error
		modifiers, err = mr.ListByGroup(ctx, tx, g.ID)
		return err
	}))
	assert.Empty(t, modifiers)
}

// ---------------------------------------------------------------------------
// Modifier tests
// ---------------------------------------------------------------------------

func TestModifierRepo_CreateListUpdate(t *testing.T) {
	ctx := context.Background()
	gr := repo.NewModifierGroupRepo()
	mr := repo.NewModifierRepo()
	tid := uuid.New()

	var g domain.ModifierGroup
	require.NoError(t, sharedPool.WithTenantTx(ctx, tid, func(tx pgx.Tx) error {
		var err error
		g, err = gr.Create(ctx, tx, domain.ModifierGroup{
			TenantID: tid, Name: "Pişirme", SelectionType: domain.SelectionSingle,
		})
		return err
	}))

	names := []string{"Az Pişmiş", "Orta", "İyi Pişmiş"}
	for _, n := range names {
		require.NoError(t, sharedPool.WithTenantTx(ctx, tid, func(tx pgx.Tx) error {
			_, err := mr.Create(ctx, tx, domain.Modifier{
				TenantID: tid, GroupID: g.ID, Name: n, IsActive: true,
			})
			return err
		}))
	}

	var list []domain.Modifier
	require.NoError(t, sharedPool.WithTenantReadTx(ctx, tid, func(tx pgx.Tx) error {
		var err error
		list, err = mr.ListByGroup(ctx, tx, g.ID)
		return err
	}))
	assert.Len(t, list, len(names))

	// Update first modifier
	m := list[0]
	m.Name = "Extra Az"
	m.PriceDelta = 500
	require.NoError(t, sharedPool.WithTenantTx(ctx, tid, func(tx pgx.Tx) error {
		var err error
		m, err = mr.Update(ctx, tx, m)
		return err
	}))
	assert.Equal(t, "Extra Az", m.Name)
	assert.Equal(t, int64(500), m.PriceDelta)
}

// ---------------------------------------------------------------------------
// ProductModifierGroup junction tests
// ---------------------------------------------------------------------------

func TestProductModifierGroupRepo_AssignAndList(t *testing.T) {
	ctx := context.Background()
	pr := repo.NewProductRepo()
	gr := repo.NewModifierGroupRepo()
	pmgr := repo.NewProductModifierGroupRepo()
	tid := uuid.New()

	var prod domain.Product
	require.NoError(t, sharedPool.WithTenantTx(ctx, tid, func(tx pgx.Tx) error {
		var err error
		prod, err = pr.Create(ctx, tx, domain.Product{
			TenantID: tid, Name: "Köfte", PriceAmount: 12000,
			Currency: "TRY", Unit: "adet", TaxRateBPS: 1800, IsActive: true,
		})
		return err
	}))

	var g1, g2 domain.ModifierGroup
	require.NoError(t, sharedPool.WithTenantTx(ctx, tid, func(tx pgx.Tx) error {
		var err error
		g1, err = gr.Create(ctx, tx, domain.ModifierGroup{
			TenantID: tid, Name: "Sos", SelectionType: domain.SelectionSingle,
		})
		if err != nil {
			return err
		}
		g2, err = gr.Create(ctx, tx, domain.ModifierGroup{
			TenantID: tid, Name: "Boyut", SelectionType: domain.SelectionSingle,
		})
		return err
	}))

	// Assign both groups
	require.NoError(t, sharedPool.WithTenantTx(ctx, tid, func(tx pgx.Tx) error {
		if err := pmgr.Assign(ctx, tx, prod.ID, g1.ID, tid, 0); err != nil {
			return err
		}
		return pmgr.Assign(ctx, tx, prod.ID, g2.ID, tid, 1)
	}))

	var ids []uuid.UUID
	require.NoError(t, sharedPool.WithTenantReadTx(ctx, tid, func(tx pgx.Tx) error {
		var err error
		ids, err = pmgr.ListByProduct(ctx, tx, prod.ID)
		return err
	}))
	assert.Len(t, ids, 2)
	assert.Contains(t, ids, g1.ID)
	assert.Contains(t, ids, g2.ID)

	// Remove one
	require.NoError(t, sharedPool.WithTenantTx(ctx, tid, func(tx pgx.Tx) error {
		return pmgr.Remove(ctx, tx, prod.ID, g1.ID)
	}))

	require.NoError(t, sharedPool.WithTenantReadTx(ctx, tid, func(tx pgx.Tx) error {
		var err error
		ids, err = pmgr.ListByProduct(ctx, tx, prod.ID)
		return err
	}))
	assert.Len(t, ids, 1)
	assert.Equal(t, g2.ID, ids[0])
}

// ---------------------------------------------------------------------------
// Menu tests
// ---------------------------------------------------------------------------

func TestMenuRepo_CreateAndGet(t *testing.T) {
	ctx := context.Background()
	mr := repo.NewMenuRepo()
	tid := uuid.New()

	var created domain.Menu
	require.NoError(t, sharedPool.WithTenantTx(ctx, tid, func(tx pgx.Tx) error {
		var err error
		created, err = mr.Create(ctx, tx, domain.Menu{
			TenantID:  tid,
			Name:      "Öğle Menüsü",
			IsActive:  true,
			SortOrder: 1,
		})
		return err
	}))

	assert.NotEqual(t, uuid.Nil, created.ID)
	assert.Equal(t, "Öğle Menüsü", created.Name)

	var fetched domain.Menu
	require.NoError(t, sharedPool.WithTenantReadTx(ctx, tid, func(tx pgx.Tx) error {
		var err error
		fetched, err = mr.GetByID(ctx, tx, created.ID)
		return err
	}))
	assert.Equal(t, created.ID, fetched.ID)
	assert.Equal(t, "Öğle Menüsü", fetched.Name)
}

func TestMenuRepo_TenantIsolation(t *testing.T) {
	ctx := context.Background()
	mr := repo.NewMenuRepo()

	tidA := uuid.New()
	tidB := uuid.New()

	var mA domain.Menu
	require.NoError(t, sharedPool.WithTenantTx(ctx, tidA, func(tx pgx.Tx) error {
		var err error
		mA, err = mr.Create(ctx, tx, domain.Menu{
			TenantID: tidA, Name: "A-Menü", IsActive: true,
		})
		return err
	}))

	require.NoError(t, sharedPool.WithTenantReadTx(ctx, tidB, func(tx pgx.Tx) error {
		_, err := mr.GetByID(ctx, tx, mA.ID)
		assert.ErrorIs(t, err, repo.ErrNotFound)
		return nil
	}))
}

// ---------------------------------------------------------------------------
// MenuItem junction tests
// ---------------------------------------------------------------------------

func TestMenuItemRepo_AddListRemove(t *testing.T) {
	ctx := context.Background()
	mnr := repo.NewMenuRepo()
	pr := repo.NewProductRepo()
	mir := repo.NewMenuItemRepo()
	tid := uuid.New()

	var menu domain.Menu
	require.NoError(t, sharedPool.WithTenantTx(ctx, tid, func(tx pgx.Tx) error {
		var err error
		menu, err = mnr.Create(ctx, tx, domain.Menu{TenantID: tid, Name: "Akşam", IsActive: true})
		return err
	}))

	var p1, p2 domain.Product
	require.NoError(t, sharedPool.WithTenantTx(ctx, tid, func(tx pgx.Tx) error {
		var err error
		p1, err = pr.Create(ctx, tx, domain.Product{
			TenantID: tid, Name: "Izgara Tavuk", PriceAmount: 9000,
			Currency: "TRY", Unit: "porsiyon", TaxRateBPS: 1800, IsActive: true,
		})
		if err != nil {
			return err
		}
		p2, err = pr.Create(ctx, tx, domain.Product{
			TenantID: tid, Name: "Mercimek Çorbası", PriceAmount: 4000,
			Currency: "TRY", Unit: "porsiyon", TaxRateBPS: 800, IsActive: true,
		})
		return err
	}))

	override := int64(8500)
	require.NoError(t, sharedPool.WithTenantTx(ctx, tid, func(tx pgx.Tx) error {
		if err := mir.AddItem(ctx, tx, domain.MenuItem{
			MenuID: menu.ID, ProductID: p1.ID, TenantID: tid,
			PriceOverride: &override, IsActive: true, SortOrder: 0,
		}); err != nil {
			return err
		}
		return mir.AddItem(ctx, tx, domain.MenuItem{
			MenuID: menu.ID, ProductID: p2.ID, TenantID: tid,
			IsActive: true, SortOrder: 1,
		})
	}))

	var items []domain.MenuItem
	require.NoError(t, sharedPool.WithTenantReadTx(ctx, tid, func(tx pgx.Tx) error {
		var err error
		items, err = mir.ListByMenu(ctx, tx, menu.ID)
		return err
	}))
	assert.Len(t, items, 2)

	// Verify price override
	for _, item := range items {
		if item.ProductID == p1.ID {
			require.NotNil(t, item.PriceOverride)
			assert.Equal(t, int64(8500), *item.PriceOverride)
		}
	}

	// Remove one item
	require.NoError(t, sharedPool.WithTenantTx(ctx, tid, func(tx pgx.Tx) error {
		return mir.RemoveItem(ctx, tx, menu.ID, p2.ID)
	}))

	require.NoError(t, sharedPool.WithTenantReadTx(ctx, tid, func(tx pgx.Tx) error {
		var err error
		items, err = mir.ListByMenu(ctx, tx, menu.ID)
		return err
	}))
	assert.Len(t, items, 1)
	assert.Equal(t, p1.ID, items[0].ProductID)
}
