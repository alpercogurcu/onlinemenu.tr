package repo_test

import (
	"context"
	"errors"
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

	pub "onlinemenu.tr/internal/modules/tenant/public"
	"onlinemenu.tr/internal/modules/tenant/repo"
	"onlinemenu.tr/internal/platform/db"
)

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

	sharedPool = newPool(ctx, superDSN)

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

	absPath := filepath.Join(migrationsBase(), "tenant")
	src := fmt.Sprintf("file://%s", absPath)
	dsn := migratorDSN + "&x-migrations-table=schema_migrations_tenant"

	mg, err := migrate.New(src, dsn)
	if err != nil {
		return fmt.Errorf("migrate open tenant: %w", err)
	}
	defer mg.Close()
	if err := mg.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("migrate up tenant: %w", err)
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
		`ALTER SCHEMA public OWNER TO app_migrator`,
		`GRANT ALL ON SCHEMA public TO app_migrator`,
		`ALTER DEFAULT PRIVILEGES FOR ROLE app_migrator IN SCHEMA public
		 GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO app_runtime`,
		`ALTER DEFAULT PRIVILEGES FOR ROLE app_migrator IN SCHEMA public
		 GRANT USAGE ON SEQUENCES TO app_runtime`,
	}

	for _, s := range stmts {
		if _, err := conn.Exec(ctx, s); err != nil {
			return fmt.Errorf("stmt failed: %w", err)
		}
	}
	return nil
}

func newPool(ctx context.Context, superDSN string) *db.Pool {
	cfg, err := pgxpool.ParseConfig(superDSN)
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse pool config: %v\n", err)
		os.Exit(1)
	}
	cfg.ConnConfig.User = "app_runtime"
	cfg.ConnConfig.Password = "runtime_secret"
	cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	cfg.MaxConns = 5

	pool, err := db.NewPoolFromConfig(ctx, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "new pool: %v\n", err)
		os.Exit(1)
	}
	return pool
}

func newTenantFixture(id uuid.UUID, slug string) pub.Tenant {
	return pub.Tenant{
		ID:             id,
		Name:           "Test İşletme",
		LegalName:      "Test İşletme A.Ş.",
		Slug:           slug,
		Plan:           pub.PlanStarter,
		EnabledModules: []string{"pos"},
		IdentityType:   pub.IdentityKurumsal,
		IsActive:       true,
	}
}

// TestTenantRepo_Create_UnderRLS proves the onboarding path works under
// FORCE RLS with the app_runtime role: the id is generated client-side and
// the INSERT runs inside WithTenantTx(newID), so WITH CHECK (id =
// app.tenant_id) passes with no sentinel/bypass involved.
func TestTenantRepo_Create_UnderRLS(t *testing.T) {
	ctx := context.Background()
	r := repo.NewTenantRepo()

	newID, err := uuid.NewV7()
	require.NoError(t, err)

	var created pub.Tenant
	err = sharedPool.WithTenantTx(ctx, newID, func(tx pgx.Tx) error {
		var err error
		created, err = r.Create(ctx, tx, newTenantFixture(newID, "create-rls"))
		return err
	})
	require.NoError(t, err)
	assert.Equal(t, newID, created.ID)

	// Read-back within the same tenant scope succeeds.
	err = sharedPool.WithTenantReadTx(ctx, newID, func(tx pgx.Tx) error {
		got, err := r.GetByID(ctx, tx, newID)
		if err != nil {
			return err
		}
		assert.Equal(t, "create-rls", got.Slug)
		return nil
	})
	require.NoError(t, err)

	// A different tenant scope must not see the row (RLS isolation).
	otherID, err := uuid.NewV7()
	require.NoError(t, err)
	err = sharedPool.WithTenantReadTx(ctx, otherID, func(tx pgx.Tx) error {
		_, err := r.GetByID(ctx, tx, newID)
		return err
	})
	require.Error(t, err)
}

// TestTenantRepo_Create_NilTenantRejected locks in the platform/db guard:
// the old uuid.Nil bootstrap sentinel must stay dead.
func TestTenantRepo_Create_NilTenantRejected(t *testing.T) {
	ctx := context.Background()
	err := sharedPool.WithTenantTx(ctx, uuid.Nil, func(tx pgx.Tx) error { return nil })
	require.Error(t, err)
	assert.True(t, errors.Is(err, db.ErrNilTenant), "expected ErrNilTenant, got: %v", err)
}
