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

	"onlinemenu.tr/internal/modules/identity/domain"
	pub "onlinemenu.tr/internal/modules/identity/public"
	"onlinemenu.tr/internal/modules/identity/repo"
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

	sharedPool = newPool(ctx, superDSN)

	if err := seedFixtures(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "seed fixtures: %v\n", err)
		sharedPool.Close()
		_ = ctr.Terminate(ctx)
		os.Exit(1)
	}

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
	// file = .../backend/internal/modules/identity/repo/integration_test.go
	// walk up 4 directories: repo/ → identity/ → modules/ → internal/ → backend/
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

	for _, mod := range []string{"tenant", "identity"} {
		absPath := filepath.Join(migrationsBase(), mod)
		src := fmt.Sprintf("file://%s", absPath)
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

// seedFixtures inserts tenant and branch rows required by identity FK constraints.
func seedFixtures(ctx context.Context) error {
	err := sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO tenants (id, name, slug, plan)
			VALUES ($1, 'Test Restaurant A', 'test-a', 'starter')
			ON CONFLICT (id) DO NOTHING`, tenantA)
		if err != nil {
			return fmt.Errorf("insert tenantA: %w", err)
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO branches (id, tenant_id, name)
			VALUES ($1, $2, 'Main Branch')
			ON CONFLICT (id) DO NOTHING`, branchA, tenantA)
		return err
	})
	if err != nil {
		return fmt.Errorf("seed tenantA: %w", err)
	}

	return sharedPool.WithTenantTx(ctx, tenantB, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO tenants (id, name, slug, plan)
			VALUES ($1, 'Test Restaurant B', 'test-b', 'starter')
			ON CONFLICT (id) DO NOTHING`, tenantB)
		return err
	})
}

// withTx executes f inside a write tenant transaction.
func withTx(ctx context.Context, t *testing.T, tenantID uuid.UUID, f func(pgx.Tx)) {
	t.Helper()
	err := sharedPool.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		f(tx)
		return nil
	})
	require.NoError(t, err)
}

// withReadTx executes f inside a read-only tenant transaction.
func withReadTx(ctx context.Context, t *testing.T, tenantID uuid.UUID, f func(pgx.Tx)) {
	t.Helper()
	err := sharedPool.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		f(tx)
		return nil
	})
	require.NoError(t, err)
}

// withPlatformTx executes f with uuid.Nil as tenant (platform-level bypass).
// Use for persons, which have no tenant_id and use the nil-UUID RLS bypass.
func withPlatformTx(ctx context.Context, t *testing.T, f func(pgx.Tx)) {
	t.Helper()
	withTx(ctx, t, uuid.Nil, f)
}

func withPlatformReadTx(ctx context.Context, t *testing.T, f func(pgx.Tx)) {
	t.Helper()
	withReadTx(ctx, t, uuid.Nil, f)
}

// ---------------------------------------------------------------------------
// PersonRepo tests
// ---------------------------------------------------------------------------

func TestPersonRepo_CreateAndGetByID(t *testing.T) {
	ctx := context.Background()
	r := repo.NewPersonRepo()

	want := domain.Person{
		KeycloakSub: "kc-sub-" + uuid.NewString(),
		Email:       "alice+" + uuid.NewString() + "@example.com",
		FullName:    "Alice Test",
		Phone:       "+905001234567",
	}

	var created domain.Person
	withPlatformTx(ctx, t, func(tx pgx.Tx) {
		var err error
		created, err = r.Create(ctx, tx, want)
		require.NoError(t, err)
		assert.NotEqual(t, uuid.Nil, created.ID)
		assert.Equal(t, want.KeycloakSub, created.KeycloakSub)
		assert.Equal(t, want.Email, created.Email)
		assert.Equal(t, want.FullName, created.FullName)
		assert.Equal(t, want.Phone, created.Phone)
	})

	withPlatformReadTx(ctx, t, func(tx pgx.Tx) {
		got, err := r.GetByID(ctx, tx, created.ID)
		require.NoError(t, err)
		assert.Equal(t, created.ID, got.ID)
		assert.Equal(t, want.Email, got.Email)
	})
}

func TestPersonRepo_GetByKeycloakSub(t *testing.T) {
	ctx := context.Background()
	r := repo.NewPersonRepo()

	sub := "kc-sub-" + uuid.NewString()
	var created domain.Person

	withPlatformTx(ctx, t, func(tx pgx.Tx) {
		var err error
		created, err = r.Create(ctx, tx, domain.Person{
			KeycloakSub: sub,
			Email:       "bob+" + uuid.NewString() + "@example.com",
			FullName:    "Bob Test",
		})
		require.NoError(t, err)
	})

	withPlatformReadTx(ctx, t, func(tx pgx.Tx) {
		got, err := r.GetByKeycloakSub(ctx, tx, sub)
		require.NoError(t, err)
		assert.Equal(t, created.ID, got.ID)
	})
}

func TestPersonRepo_Update(t *testing.T) {
	ctx := context.Background()
	r := repo.NewPersonRepo()

	var person domain.Person
	withPlatformTx(ctx, t, func(tx pgx.Tx) {
		var err error
		person, err = r.Create(ctx, tx, domain.Person{
			KeycloakSub: "kc-sub-" + uuid.NewString(),
			Email:       "charlie+" + uuid.NewString() + "@example.com",
			FullName:    "Charlie Test",
		})
		require.NoError(t, err)
	})

	withPlatformTx(ctx, t, func(tx pgx.Tx) {
		person.FullName = "Charlie Updated"
		person.Phone = "+905009999999"
		updated, err := r.Update(ctx, tx, person)
		require.NoError(t, err)
		assert.Equal(t, "Charlie Updated", updated.FullName)
		assert.Equal(t, "+905009999999", updated.Phone)
	})
}

func TestPersonRepo_GetByID_NotFound(t *testing.T) {
	ctx := context.Background()
	r := repo.NewPersonRepo()

	withPlatformReadTx(ctx, t, func(tx pgx.Tx) {
		_, err := r.GetByID(ctx, tx, uuid.New())
		assert.ErrorIs(t, err, pub.ErrNotFound)
	})
}

// ---------------------------------------------------------------------------
// RoleRepo tests
// ---------------------------------------------------------------------------

func TestRoleRepo_SystemRolesSeeded(t *testing.T) {
	ctx := context.Background()
	r := repo.NewRoleRepo()

	// System roles (tenant_id IS NULL) should be visible regardless of tenant context.
	withReadTx(ctx, t, tenantA, func(tx pgx.Tx) {
		roles, err := r.ListForTenant(ctx, tx, tenantA)
		require.NoError(t, err)

		keys := make(map[string]bool)
		for _, role := range roles {
			if role.IsSystem {
				keys[role.SystemKey] = true
			}
		}
		assert.True(t, keys["manager"], "manager system role must be seeded")
		assert.True(t, keys["cashier"], "cashier system role must be seeded")
		assert.True(t, keys["kitchen"], "kitchen system role must be seeded")
	})
}

func TestRoleRepo_CreateCustomAndDelete(t *testing.T) {
	ctx := context.Background()
	r := repo.NewRoleRepo()

	var customRole domain.Role
	withTx(ctx, t, tenantA, func(tx pgx.Tx) {
		var err error
		customRole, err = r.Create(ctx, tx, domain.Role{
			TenantID: &tenantA,
			Name:     "Custom Role " + uuid.NewString(),
		})
		require.NoError(t, err)
		assert.NotEqual(t, uuid.Nil, customRole.ID)
		assert.False(t, customRole.IsSystem)
	})

	// Verify it appears in the tenant's role list.
	withReadTx(ctx, t, tenantA, func(tx pgx.Tx) {
		roles, err := r.ListForTenant(ctx, tx, tenantA)
		require.NoError(t, err)
		found := false
		for _, ro := range roles {
			if ro.ID == customRole.ID {
				found = true
				break
			}
		}
		assert.True(t, found, "custom role must appear in tenant list")
	})

	// Delete it and verify it's gone.
	withTx(ctx, t, tenantA, func(tx pgx.Tx) {
		err := r.Delete(ctx, tx, tenantA, customRole.ID)
		require.NoError(t, err)
	})

	withReadTx(ctx, t, tenantA, func(tx pgx.Tx) {
		_, err := r.GetByID(ctx, tx, tenantA, customRole.ID)
		assert.ErrorIs(t, err, pub.ErrNotFound)
	})
}

func TestRoleRepo_TenantIsolation(t *testing.T) {
	ctx := context.Background()
	r := repo.NewRoleRepo()

	var roleInA domain.Role
	withTx(ctx, t, tenantA, func(tx pgx.Tx) {
		var err error
		roleInA, err = r.Create(ctx, tx, domain.Role{
			TenantID: &tenantA,
			Name:     "Isolated Role " + uuid.NewString(),
		})
		require.NoError(t, err)
	})

	// tenantB must not see tenantA's custom role.
	withReadTx(ctx, t, tenantB, func(tx pgx.Tx) {
		_, err := r.GetByID(ctx, tx, tenantB, roleInA.ID)
		assert.ErrorIs(t, err, pub.ErrNotFound)
	})
}

// ---------------------------------------------------------------------------
// MembershipRepo tests
// ---------------------------------------------------------------------------

// systemRoleID returns the UUID of a seeded system role by system_key.
func systemRoleID(t *testing.T, systemKey string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	r := repo.NewRoleRepo()
	var roleID uuid.UUID
	withReadTx(ctx, t, tenantA, func(tx pgx.Tx) {
		roles, err := r.ListForTenant(ctx, tx, tenantA)
		require.NoError(t, err)
		for _, ro := range roles {
			if ro.SystemKey == systemKey {
				roleID = ro.ID
				return
			}
		}
		t.Fatalf("system role %q not found in seed", systemKey)
	})
	return roleID
}

func TestMembershipRepo_CreateAndGetByID(t *testing.T) {
	ctx := context.Background()
	mr := repo.NewMembershipRepo()
	pr := repo.NewPersonRepo()
	cashierRoleID := systemRoleID(t, "cashier")

	var person domain.Person
	withPlatformTx(ctx, t, func(tx pgx.Tx) {
		var err error
		person, err = pr.Create(ctx, tx, domain.Person{
			KeycloakSub: "kc-sub-" + uuid.NewString(),
			Email:       "dave+" + uuid.NewString() + "@example.com",
			FullName:    "Dave Test",
		})
		require.NoError(t, err)
	})

	var created domain.Membership
	withTx(ctx, t, tenantA, func(tx pgx.Tx) {
		var err error
		created, err = mr.Create(ctx, tx, domain.Membership{
			PersonID: person.ID,
			TenantID: tenantA,
			BranchID: &branchA,
			RoleID:   cashierRoleID,
			Status:   domain.MembershipActive,
		})
		require.NoError(t, err)
		assert.NotEqual(t, uuid.Nil, created.ID)
		assert.Equal(t, domain.MembershipActive, created.Status)
	})

	withReadTx(ctx, t, tenantA, func(tx pgx.Tx) {
		got, err := mr.GetByID(ctx, tx, tenantA, created.ID)
		require.NoError(t, err)
		assert.Equal(t, created.ID, got.ID)
		assert.Equal(t, person.ID, got.PersonID)
		assert.Equal(t, &branchA, got.BranchID)
	})
}

func TestMembershipRepo_UpdateStatus(t *testing.T) {
	ctx := context.Background()
	mr := repo.NewMembershipRepo()
	pr := repo.NewPersonRepo()
	kitchenRoleID := systemRoleID(t, "kitchen")

	var person domain.Person
	withPlatformTx(ctx, t, func(tx pgx.Tx) {
		var err error
		person, err = pr.Create(ctx, tx, domain.Person{
			KeycloakSub: "kc-sub-" + uuid.NewString(),
			Email:       "eve+" + uuid.NewString() + "@example.com",
			FullName:    "Eve Test",
		})
		require.NoError(t, err)
	})

	var membership domain.Membership
	withTx(ctx, t, tenantA, func(tx pgx.Tx) {
		var err error
		membership, err = mr.Create(ctx, tx, domain.Membership{
			PersonID: person.ID,
			TenantID: tenantA,
			BranchID: &branchA,
			RoleID:   kitchenRoleID,
			Status:   domain.MembershipActive,
		})
		require.NoError(t, err)
	})

	withTx(ctx, t, tenantA, func(tx pgx.Tx) {
		err := mr.UpdateStatus(ctx, tx, tenantA, membership.ID, domain.MembershipSuspended)
		require.NoError(t, err)
	})

	withReadTx(ctx, t, tenantA, func(tx pgx.Tx) {
		got, err := mr.GetByID(ctx, tx, tenantA, membership.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.MembershipSuspended, got.Status)
	})
}

func TestMembershipRepo_ActiveRoleIDsAt(t *testing.T) {
	ctx := context.Background()
	mr := repo.NewMembershipRepo()
	pr := repo.NewPersonRepo()
	cashierRoleID := systemRoleID(t, "cashier")
	kitchenRoleID := systemRoleID(t, "kitchen")

	var person domain.Person
	withPlatformTx(ctx, t, func(tx pgx.Tx) {
		var err error
		person, err = pr.Create(ctx, tx, domain.Person{
			KeycloakSub: "kc-sub-" + uuid.NewString(),
			Email:       "frank+" + uuid.NewString() + "@example.com",
			FullName:    "Frank Test",
		})
		require.NoError(t, err)
	})

	// Create two active memberships at the same branch.
	withTx(ctx, t, tenantA, func(tx pgx.Tx) {
		_, err := mr.Create(ctx, tx, domain.Membership{
			PersonID: person.ID, TenantID: tenantA, BranchID: &branchA,
			RoleID: cashierRoleID, Status: domain.MembershipActive,
		})
		require.NoError(t, err)
		_, err = mr.Create(ctx, tx, domain.Membership{
			PersonID: person.ID, TenantID: tenantA, BranchID: &branchA,
			RoleID: kitchenRoleID, Status: domain.MembershipActive,
		})
		require.NoError(t, err)
	})

	withReadTx(ctx, t, tenantA, func(tx pgx.Tx) {
		ids, err := mr.ActiveRoleIDsAt(ctx, tx, tenantA, person.ID, branchA)
		require.NoError(t, err)
		assert.Len(t, ids, 2)
		idSet := make(map[uuid.UUID]bool, 2)
		for _, id := range ids {
			idSet[id] = true
		}
		assert.True(t, idSet[cashierRoleID])
		assert.True(t, idSet[kitchenRoleID])
	})
}

// ---------------------------------------------------------------------------
// PermissionRepo tests
// ---------------------------------------------------------------------------

func TestPermissionRepo_LoadForSystemRoles(t *testing.T) {
	ctx := context.Background()
	permRepo := repo.NewPermissionRepo()
	roleRepo := repo.NewRoleRepo()
	cashierRoleID := systemRoleID(t, "cashier")

	_ = roleRepo // used via systemRoleID

	withReadTx(ctx, t, tenantA, func(tx pgx.Tx) {
		perms, policies, err := permRepo.LoadForRoles(ctx, tx, []uuid.UUID{cashierRoleID})
		require.NoError(t, err)
		assert.NotEmpty(t, perms, "cashier role must have permissions seeded")
		// policies may be empty for some roles — just confirm no error
		_ = policies
	})
}

func TestPermissionRepo_UpsertAndDelete(t *testing.T) {
	ctx := context.Background()
	permRepo := repo.NewPermissionRepo()
	roleRepo := repo.NewRoleRepo()

	var customRole domain.Role
	withTx(ctx, t, tenantA, func(tx pgx.Tx) {
		var err error
		customRole, err = roleRepo.Create(ctx, tx, domain.Role{
			TenantID: &tenantA,
			Name:     "PermTest Role " + uuid.NewString(),
		})
		require.NoError(t, err)
	})

	withTx(ctx, t, tenantA, func(tx pgx.Tx) {
		err := permRepo.UpsertPermission(ctx, tx, domain.Permission{
			RoleID:   customRole.ID,
			Resource: "catalog",
			Action:   "product:read",
		})
		require.NoError(t, err)

		// Upsert again — must be idempotent (no error, no duplicate).
		err = permRepo.UpsertPermission(ctx, tx, domain.Permission{
			RoleID:   customRole.ID,
			Resource: "catalog",
			Action:   "product:read",
		})
		require.NoError(t, err)
	})

	withReadTx(ctx, t, tenantA, func(tx pgx.Tx) {
		perms, _, err := permRepo.LoadForRoles(ctx, tx, []uuid.UUID{customRole.ID})
		require.NoError(t, err)
		require.Len(t, perms, 1)
		assert.Equal(t, "catalog", perms[0].Resource)
		assert.Equal(t, "product:read", perms[0].Action)
	})

	withTx(ctx, t, tenantA, func(tx pgx.Tx) {
		err := permRepo.DeletePermission(ctx, tx, customRole.ID, "catalog", "product:read")
		require.NoError(t, err)
	})

	withReadTx(ctx, t, tenantA, func(tx pgx.Tx) {
		perms, _, err := permRepo.LoadForRoles(ctx, tx, []uuid.UUID{customRole.ID})
		require.NoError(t, err)
		assert.Empty(t, perms)
	})
}
