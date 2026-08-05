package service_test

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
	"go.uber.org/zap"

	"onlinemenu.tr/internal/modules/catalog/domain"
	pub "onlinemenu.tr/internal/modules/catalog/public"
	"onlinemenu.tr/internal/modules/catalog/repo"
	"onlinemenu.tr/internal/modules/catalog/service"
	"onlinemenu.tr/internal/platform/db"
)

// PriceCart is the storefront's whole defence against price tampering, and it
// only exists as SQL plus a fold over its rows — so it is exercised against a
// real database here rather than against a stubbed repo.

var sharedPool *db.Pool

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
		fmt.Fprintf(os.Stderr, "connection string: %v\n", err)
		_ = ctr.Terminate(ctx)
		os.Exit(1)
	}
	if err := bootstrapRoles(ctx, superDSN); err != nil {
		fmt.Fprintf(os.Stderr, "bootstrap roles: %v\n", err)
		_ = ctr.Terminate(ctx)
		os.Exit(1)
	}
	if err := runMigrations(superDSN); err != nil {
		fmt.Fprintf(os.Stderr, "migrations: %v\n", err)
		_ = ctr.Terminate(ctx)
		os.Exit(1)
	}

	sharedPool = newPool(ctx, superDSN, "app_runtime", "runtime_secret")
	rc := m.Run()
	sharedPool.Close()
	_ = ctr.Terminate(ctx)
	os.Exit(rc)
}

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
	migratorDSN := fmt.Sprintf("pgx5://app_migrator:migrator_secret@%s:%d/%s?sslmode=disable",
		cfg.ConnConfig.Host, cfg.ConnConfig.Port, cfg.ConnConfig.Database)

	for _, mod := range []string{"tenant", "identity", "catalog"} {
		src := "file://" + filepath.Join(migrationsBase(), mod)
		dsn := fmt.Sprintf("%s&x-migrations-table=schema_migrations_%s", migratorDSN, mod)
		mig, err := migrate.New(src, dsn)
		if err != nil {
			return fmt.Errorf("migrate open %s: %w", mod, err)
		}
		if err := mig.Up(); err != nil && err != migrate.ErrNoChange {
			mig.Close()
			return fmt.Errorf("migrate up %s: %w", mod, err)
		}
		mig.Close()
	}
	return nil
}

func bootstrapRoles(ctx context.Context, superDSN string) error {
	conn, err := pgx.Connect(ctx, superDSN)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer conn.Close(ctx)

	for _, s := range []string{
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
	} {
		if _, err := conn.Exec(ctx, s); err != nil {
			return fmt.Errorf("stmt: %w", err)
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
		fmt.Fprintf(os.Stderr, "create pool: %v\n", err)
		os.Exit(1)
	}
	return p
}

// ---------------------------------------------------------------------------

type pricingFixture struct {
	tenantID   uuid.UUID
	branchID   uuid.UUID
	productID  uuid.UUID
	discountID uuid.UUID // a NEGATIVE price delta — schema allows it explicitly
	extraID    uuid.UUID
	sizeSmall  uuid.UUID
	sizeLarge  uuid.UUID
}

// seedPricingFixture builds a product on an active branch menu with two
// modifier groups: a "multiple" group (one positive, one negative delta) and
// a "single" group with two options.
func seedPricingFixture(t *testing.T) pricingFixture {
	t.Helper()
	ctx := context.Background()
	f := pricingFixture{tenantID: uuid.New(), branchID: uuid.New()}

	require.NoError(t, sharedPool.WithTenantTx(ctx, f.tenantID, func(tx pgx.Tx) error {
		product, err := repo.NewProductRepo().Create(ctx, tx, domain.Product{
			TenantID: f.tenantID, Name: "Hamburger", PriceAmount: 15000,
			Currency: "TRY", Unit: "adet", TaxRateBPS: 1000, IsActive: true,
		})
		if err != nil {
			return err
		}
		f.productID = product.ID

		menu, err := repo.NewMenuRepo().Create(ctx, tx, domain.Menu{
			TenantID: f.tenantID, BranchID: &f.branchID, Name: "Menü", IsActive: true,
		})
		if err != nil {
			return err
		}
		if err := repo.NewMenuItemRepo().AddItem(ctx, tx, domain.MenuItem{
			MenuID: menu.ID, ProductID: product.ID, TenantID: f.tenantID, IsActive: true,
		}); err != nil {
			return err
		}

		extras, err := repo.NewModifierGroupRepo().Create(ctx, tx, domain.ModifierGroup{
			TenantID: f.tenantID, Name: "Ekstralar", SelectionType: "multiple",
		})
		if err != nil {
			return err
		}
		extra, err := repo.NewModifierRepo().Create(ctx, tx, domain.Modifier{
			TenantID: f.tenantID, GroupID: extras.ID, Name: "Ekstra peynir", PriceDelta: 2000, IsActive: true,
		})
		if err != nil {
			return err
		}
		f.extraID = extra.ID
		discount, err := repo.NewModifierRepo().Create(ctx, tx, domain.Modifier{
			TenantID: f.tenantID, GroupID: extras.ID, Name: "Sossuz", PriceDelta: -1000, IsActive: true,
		})
		if err != nil {
			return err
		}
		f.discountID = discount.ID

		sizes, err := repo.NewModifierGroupRepo().Create(ctx, tx, domain.ModifierGroup{
			TenantID: f.tenantID, Name: "Boy", SelectionType: "single",
		})
		if err != nil {
			return err
		}
		small, err := repo.NewModifierRepo().Create(ctx, tx, domain.Modifier{
			TenantID: f.tenantID, GroupID: sizes.ID, Name: "Küçük", PriceDelta: 0, IsActive: true,
		})
		if err != nil {
			return err
		}
		f.sizeSmall = small.ID
		large, err := repo.NewModifierRepo().Create(ctx, tx, domain.Modifier{
			TenantID: f.tenantID, GroupID: sizes.ID, Name: "Büyük", PriceDelta: 3000, IsActive: true,
		})
		if err != nil {
			return err
		}
		f.sizeLarge = large.ID

		pmg := repo.NewProductModifierGroupRepo()
		if err := pmg.Assign(ctx, tx, product.ID, extras.ID, f.tenantID, 0); err != nil {
			return err
		}
		return pmg.Assign(ctx, tx, product.ID, sizes.ID, f.tenantID, 1)
	}))
	return f
}

func newPricingService() *service.StorefrontMenuService {
	return service.NewStorefrontMenuService(service.StorefrontMenuParams{
		DB:     sharedPool,
		Repo:   repo.NewStorefrontMenuRepo(),
		Logger: zap.NewNop(),
	})
}

func TestPriceCart_AppliesModifierDeltasOnce(t *testing.T) {
	f := seedPricingFixture(t)

	priced, err := newPricingService().PriceCart(context.Background(), f.tenantID, f.branchID,
		[]pub.CartLine{{ProductID: f.productID, Quantity: 2, ModifierIDs: []uuid.UUID{f.extraID, f.sizeLarge}}})
	require.NoError(t, err)

	require.Len(t, priced, 1)
	assert.Equal(t, int64(15000), priced[0].BasePriceAmount)
	assert.Equal(t, int64(20000), priced[0].UnitPriceAmount, "15000 + 2000 + 3000")
	assert.Equal(t, 1000, priced[0].TaxRateBPS)
	assert.Len(t, priced[0].Modifiers, 2)
}

// TestPriceCart_RepeatedNegativeModifier_IsRejected is the price-manipulation
// case duplicates open up: price_delta is signed by design, so stacking one
// "Sossuz" (-1000) fifteen times would take a 15000 item to zero. The server
// must refuse what was sent rather than quietly deduping it.
func TestPriceCart_RepeatedNegativeModifier_IsRejected(t *testing.T) {
	f := seedPricingFixture(t)

	repeated := make([]uuid.UUID, 15)
	for i := range repeated {
		repeated[i] = f.discountID
	}

	_, err := newPricingService().PriceCart(context.Background(), f.tenantID, f.branchID,
		[]pub.CartLine{{ProductID: f.productID, Quantity: 1, ModifierIDs: repeated}})

	var validation *pub.ValidationError
	require.ErrorAs(t, err, &validation)
}

// TestPriceCart_TwoOptionsFromASingleGroup_IsRejected: a "single" group means
// one choice, whatever the client's UI allowed.
func TestPriceCart_TwoOptionsFromASingleGroup_IsRejected(t *testing.T) {
	f := seedPricingFixture(t)

	_, err := newPricingService().PriceCart(context.Background(), f.tenantID, f.branchID,
		[]pub.CartLine{{ProductID: f.productID, Quantity: 1, ModifierIDs: []uuid.UUID{f.sizeSmall, f.sizeLarge}}})

	var validation *pub.ValidationError
	require.ErrorAs(t, err, &validation)
}

func TestPriceCart_ForeignModifier_IsRejected(t *testing.T) {
	f := seedPricingFixture(t)
	other := seedPricingFixture(t)

	_, err := newPricingService().PriceCart(context.Background(), f.tenantID, f.branchID,
		[]pub.CartLine{{ProductID: f.productID, Quantity: 1, ModifierIDs: []uuid.UUID{other.extraID}}})

	var validation *pub.ValidationError
	require.ErrorAs(t, err, &validation,
		"another tenant's modifier must not price this line, even though it exists")
}

func TestPriceCart_AnotherTenantsProduct_IsRejected(t *testing.T) {
	f := seedPricingFixture(t)
	other := seedPricingFixture(t)

	_, err := newPricingService().PriceCart(context.Background(), f.tenantID, f.branchID,
		[]pub.CartLine{{ProductID: other.productID, Quantity: 1}})

	var validation *pub.ValidationError
	require.ErrorAs(t, err, &validation)
}

// TestPriceCart_ReturnsOneLinePerInputLineInOrder pins the positional contract
// the storefront relies on to attach each diner's note to the right dish.
func TestPriceCart_ReturnsOneLinePerInputLineInOrder(t *testing.T) {
	f := seedPricingFixture(t)

	priced, err := newPricingService().PriceCart(context.Background(), f.tenantID, f.branchID, []pub.CartLine{
		{ProductID: f.productID, Quantity: 1},
		{ProductID: f.productID, Quantity: 1, ModifierIDs: []uuid.UUID{f.sizeLarge}},
	})
	require.NoError(t, err)

	require.Len(t, priced, 2, "the same product twice stays two lines")
	assert.Equal(t, int64(15000), priced[0].UnitPriceAmount)
	assert.Equal(t, int64(18000), priced[1].UnitPriceAmount)
}
