package service_test

// This harness is a per-package copy of the same testcontainers/TestMain
// pattern used in identity/repo and identity/events (see
// events/seed_roles_integration_test.go's comment on why each package that
// needs a real Postgres carries its own copy rather than sharing one).
//
// These tests cover the /me/contexts behavioural fix: an authenticated
// Keycloak subject with no persons row must get an empty context list, not
// a caller-facing error, because a persons row grants no access on its own
// (memberships do). Full auto-provisioning (creating the persons row here)
// is separate work, blocked on a decision recorded in docs/backlog-pilot.md
// item 2: the Keycloak token carries only `sub` (ADR-AUTH-001), while
// persons.email is NOT NULL UNIQUE, so there is no legitimate value to
// insert for a never-seen subject.

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
	"go.uber.org/zap"

	"onlinemenu.tr/internal/modules/identity/domain"
	pub "onlinemenu.tr/internal/modules/identity/public"
	"onlinemenu.tr/internal/modules/identity/repo"
	"onlinemenu.tr/internal/modules/identity/service"
	tenantpub "onlinemenu.tr/internal/modules/tenant/public"
	"onlinemenu.tr/internal/platform/db"
)

var (
	sharedPool *db.Pool
	tenantA    = uuid.MustParse("aaaaaaaa-0000-0000-0000-000000000002")
	branchA    = uuid.MustParse("cccccccc-0000-0000-0000-000000000002")

	personSvc     *service.PersonService
	membershipSvc *service.MembershipService
)

// fakeTenantReader is an in-memory stand-in for tenant's pub.TenantReader —
// only GetBranch is exercised by identity's R2 branch-existence validation
// (StaffInviteService.Invite, MembershipService.Create).
type fakeTenantReader struct {
	branches map[uuid.UUID]tenantpub.Branch
}

func newFakeTenantReader() *fakeTenantReader {
	return &fakeTenantReader{
		branches: map[uuid.UUID]tenantpub.Branch{
			branchA: {ID: branchA, TenantID: tenantA, Name: "Main Branch"},
		},
	}
}

func (f *fakeTenantReader) GetByID(context.Context, uuid.UUID) (tenantpub.Tenant, error) {
	return tenantpub.Tenant{}, tenantpub.ErrNotFound
}

func (f *fakeTenantReader) GetBranch(_ context.Context, tenantID, branchID uuid.UUID) (tenantpub.Branch, error) {
	b, ok := f.branches[branchID]
	if !ok || b.TenantID != tenantID {
		return tenantpub.Branch{}, tenantpub.ErrNotFound
	}
	return b, nil
}

func (f *fakeTenantReader) IsModuleEnabled(context.Context, uuid.UUID, string) (bool, error) {
	return true, nil
}

func (f *fakeTenantReader) GetEffectiveIntegrator(context.Context, uuid.UUID, uuid.UUID, tenantpub.BillingProvider) (tenantpub.BillingIntegrator, error) {
	return tenantpub.BillingIntegrator{}, tenantpub.ErrNotFound
}

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

	buildServices()

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

func buildServices() {
	log := zap.NewNop()
	personSvc = service.NewPersonService(service.PersonParams{
		DB: sharedPool, PersonRepo: repo.NewPersonRepo(), Logger: log,
	})
	membershipSvc = service.NewMembershipService(service.MembershipParams{
		DB: sharedPool, MembershipRepo: repo.NewMembershipRepo(), RoleRepo: repo.NewRoleRepo(),
		TenantReader: newFakeTenantReader(), Logger: log,
	})
}

func migrationsBase() string {
	_, file, _, _ := runtime.Caller(0)
	// file = .../backend/internal/modules/identity/service/membership_service_test.go
	// walk up 4: service -> identity -> modules -> internal -> backend
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

// seedFixtures inserts the tenant and branch rows required by identity FK constraints.
func seedFixtures(ctx context.Context) error {
	return sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO tenants (id, name, slug, plan)
			VALUES ($1, 'Test Restaurant Svc A', 'test-svc-a', 'starter')
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
}

func systemRoleID(t *testing.T, systemKey string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	r := repo.NewRoleRepo()
	var roleID uuid.UUID
	err := sharedPool.WithTenantReadTx(ctx, tenantA, func(tx pgx.Tx) error {
		roles, err := r.ListForTenant(ctx, tx, tenantA)
		if err != nil {
			return err
		}
		for _, ro := range roles {
			if ro.SystemKey == systemKey {
				roleID = ro.ID
				return nil
			}
		}
		t.Fatalf("system role %q not found in seed", systemKey)
		return nil
	})
	require.NoError(t, err)
	return roleID
}

// ---------------------------------------------------------------------------
// MembershipService.ListContexts — pre-context provisioning behaviour
// ---------------------------------------------------------------------------

// TestListContexts_UnknownKeycloakSub_ReturnsEmptyNotError proves the
// user-visible bug is fixed: a Keycloak subject that has authenticated but
// has no persons row yet must get an empty context list (200), not
// pub.ErrNotFound surfaced as an error (which the HTTP layer maps to 404).
func TestListContexts_UnknownKeycloakSub_ReturnsEmptyNotError(t *testing.T) {
	ctx := context.Background()
	unknownSub := "kc-sub-never-seen-" + uuid.NewString()

	items, err := membershipSvc.ListContexts(ctx, unknownSub, personSvc)
	require.NoError(t, err, "unknown keycloak subject must not surface an error")
	assert.NotNil(t, items, "must return an empty slice, not nil, so the DTO layer renders []")
	assert.Empty(t, items)
}

// TestListContexts_ExistingPersonWithMemberships_Unchanged is the regression
// guard: a person that already exists and holds an active membership must
// keep returning that membership's context — the fix above must not swallow
// real results.
func TestListContexts_ExistingPersonWithMemberships_Unchanged(t *testing.T) {
	ctx := context.Background()
	sub := "kc-sub-" + uuid.NewString()
	cashierRoleID := systemRoleID(t, "cashier")

	person, err := personSvc.Create(ctx, domain.Person{
		KeycloakSub: sub,
		Email:       "existing+" + uuid.NewString() + "@example.com",
		FullName:    "Existing Person",
	})
	require.NoError(t, err)

	err = sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		_, err := repo.NewMembershipRepo().Create(ctx, tx, domain.Membership{
			PersonID: person.ID, TenantID: tenantA, BranchID: &branchA,
			RoleID: cashierRoleID, Status: domain.MembershipActive,
		})
		return err
	})
	require.NoError(t, err)

	items, err := membershipSvc.ListContexts(ctx, sub, personSvc)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, tenantA, items[0].TenantID)
	assert.Equal(t, cashierRoleID, items[0].RoleID)
}

// ---------------------------------------------------------------------------
// MembershipService.Create — R2 branch existence validation
// ---------------------------------------------------------------------------

// TestMembershipService_Create_KnownBranch_Succeeds is the happy path: a
// branch_id the fakeTenantReader actually knows about must not be rejected.
func TestMembershipService_Create_KnownBranch_Succeeds(t *testing.T) {
	ctx := context.Background()
	cashierRoleID := systemRoleID(t, "cashier")

	person, err := personSvc.Create(ctx, domain.Person{
		KeycloakSub: "kc-sub-" + uuid.NewString(),
		Email:       "known-branch+" + uuid.NewString() + "@example.com",
		FullName:    "Known Branch",
	})
	require.NoError(t, err)

	membership, err := membershipSvc.Create(ctx, tenantA, person.ID, &branchA, cashierRoleID)
	require.NoError(t, err)
	assert.Equal(t, branchA, *membership.BranchID)
}

// TestMembershipService_Create_UnknownBranch_ReturnsErrInvalidInput pins R2:
// a branch_id that does not exist in the tenant must be rejected as a 422,
// not left to fail later as an opaque error — identity carries no FK to
// tenant's branches table (module isolation), so this check is the only
// thing that catches it.
func TestMembershipService_Create_UnknownBranch_ReturnsErrInvalidInput(t *testing.T) {
	ctx := context.Background()
	cashierRoleID := systemRoleID(t, "cashier")
	unknownBranch := uuid.New()

	person, err := personSvc.Create(ctx, domain.Person{
		KeycloakSub: "kc-sub-" + uuid.NewString(),
		Email:       "unknown-branch+" + uuid.NewString() + "@example.com",
		FullName:    "Unknown Branch",
	})
	require.NoError(t, err)

	_, err = membershipSvc.Create(ctx, tenantA, person.ID, &unknownBranch, cashierRoleID)
	require.Error(t, err)
	assert.True(t, errors.Is(err, pub.ErrInvalidInput), "got %v", err)
}

// TestMembershipService_Create_NilTenantReader_FailsClosed pins M-1: a
// service misconstructed without a TenantReader (fx wires it as a required
// param in production, so this can only happen via a struct literal that
// forgot the field, e.g. in a test) must refuse a branch-scoped Create with
// an explicit error, not silently skip R2's validation and let an
// unvalidated branch_id straight into a membership write.
func TestMembershipService_Create_NilTenantReader_FailsClosed(t *testing.T) {
	ctx := context.Background()
	cashierRoleID := systemRoleID(t, "cashier")

	person, err := personSvc.Create(ctx, domain.Person{
		KeycloakSub: "kc-sub-" + uuid.NewString(),
		Email:       "nil-reader+" + uuid.NewString() + "@example.com",
		FullName:    "Nil Reader",
	})
	require.NoError(t, err)

	svc := service.NewMembershipService(service.MembershipParams{
		DB: sharedPool, MembershipRepo: repo.NewMembershipRepo(), RoleRepo: repo.NewRoleRepo(),
		TenantReader: nil, Logger: zap.NewNop(),
	})

	_, err = svc.Create(ctx, tenantA, person.ID, &branchA, cashierRoleID)
	require.Error(t, err, "a nil tenant reader must fail closed, not be treated as validation-skipped")

	memberships, err := membershipSvc.ListDetails(ctx, tenantA, &person.ID, nil)
	require.NoError(t, err)
	assert.Empty(t, memberships, "the rejected call must not have written a membership")
}
