package events_test

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
	"github.com/jackc/pgx/v5/pgconn"
	pgxpool "github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"go.uber.org/goleak"
	"go.uber.org/zap"

	"onlinemenu.tr/internal/modules/identity/events"
	"onlinemenu.tr/internal/modules/identity/repo"
	"onlinemenu.tr/internal/platform/db"
)

// This harness lives in the events package because SeedTenantRoles does, and
// go-arch-lint forbids identity_repo → identity_events (that direction would be
// a cycle; identity_events → identity_repo is the sanctioned one).

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
	migratorDSN := fmt.Sprintf("pgx5://%s:%s@%s:%d/%s?sslmode=disable",
		"app_migrator", "migrator_secret",
		cfg.ConnConfig.Host, cfg.ConnConfig.Port, cfg.ConnConfig.Database,
	)

	for _, mod := range []string{"tenant", "identity"} {
		src := fmt.Sprintf("file://%s", filepath.Join(migrationsBase(), mod))
		dsn := fmt.Sprintf("%s&x-migrations-table=schema_migrations_%s", migratorDSN, mod)

		mg, err := migrate.New(src, dsn)
		if err != nil {
			return fmt.Errorf("migrate open %s: %w", mod, err)
		}
		if err := mg.Up(); err != nil && err != migrate.ErrNoChange {
			mg.Close()
			return fmt.Errorf("migrate up %s: %w", mod, err)
		}
		mg.Close()
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

	p, err := db.NewPoolFromConfig(ctx, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create pool: %v\n", err)
		os.Exit(1)
	}
	return p
}

// TestSeedTenantRoles_CopiesBranchScopedFlag is the regression test for the bug
// ADR-SEC-005 closes: role clones lose system_key, so branch_scoped on the row is
// the only surviving branch-scope signal. If the clone INSERT stops copying it,
// the cloned cashier role silently becomes grantable chain-wide again.
func TestSeedTenantRoles_CopiesBranchScopedFlag(t *testing.T) {
	ctx := context.Background()
	tenantID := uuid.New()

	// tenants RLS is tenant-scoped (tenant_write WITH CHECK id = app.tenant_id),
	// so the row must be created inside its own tenant transaction.
	require.NoError(t, sharedPool.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO tenants (id, name, slug, plan)
			VALUES ($1, 'Clone Test', $2, 'starter')`, tenantID, "clone-"+tenantID.String()[:8])
		return err
	}))

	roleRepo := repo.NewRoleRepo()
	sub := events.NewSubscriber(nil, sharedPool, roleRepo, zap.NewNop())

	copied, err := sub.SeedTenantRoles(ctx, tenantID)
	require.NoError(t, err)
	require.Positive(t, copied)

	type clonedRole struct {
		id           uuid.UUID
		branchScoped bool
		systemKey    string
	}
	clones := make(map[string]clonedRole)
	require.NoError(t, sharedPool.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, name, branch_scoped, COALESCE(system_key, '')
			FROM roles WHERE tenant_id = $1`, tenantID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var name string
			var c clonedRole
			if err := rows.Scan(&c.id, &name, &c.branchScoped, &c.systemKey); err != nil {
				return err
			}
			clones[name] = c
		}
		return rows.Err()
	}))

	require.NotEmpty(t, clones)
	for name, c := range clones {
		assert.Empty(t, c.systemKey, "clone %q must not retain system_key", name)
	}

	cashierClone, ok := clones["Kasiyer"]
	require.True(t, ok, "cashier template clone missing")
	assert.True(t, cashierClone.branchScoped, "clone must inherit branch_scoped")

	managerClone, ok := clones["Yönetici"]
	require.True(t, ok, "manager template clone missing")
	assert.False(t, managerClone.branchScoped, "manager is chain-wide")

	// End-to-end: the memberships guard must reject a chain-wide grant of the clone.
	var personID uuid.UUID
	require.NoError(t, sharedPool.WithAllTenantsTx(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			INSERT INTO persons (keycloak_sub, email, full_name)
			VALUES ($1, $2, 'Clone Guard Test')
			RETURNING id`,
			"kc-"+uuid.NewString(), "clone+"+uuid.NewString()+"@example.com",
		).Scan(&personID)
	}))

	err = sharedPool.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		_, execErr := tx.Exec(ctx, `
			INSERT INTO memberships (person_id, tenant_id, branch_id, role_id, status)
			VALUES ($1, $2, NULL, $3, 'active')`, personID, tenantID, cashierClone.id)
		return execErr
	})
	require.Error(t, err, "chain-wide grant of a cloned branch-scoped role must be rejected")

	var pgErr *pgconn.PgError
	require.ErrorAs(t, err, &pgErr)
	assert.Equal(t, "23514", pgErr.Code)
}
