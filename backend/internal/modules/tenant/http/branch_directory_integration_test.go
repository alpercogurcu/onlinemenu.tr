package http_test

// This file proves the C1 fix end to end (final-review-report.md): a
// branch-facing principal listing branches gets only the directory
// projection (no IBAN/tax/legal identity), while a tenant-scoped principal
// still gets the full record. It needs a real Postgres (RLS +
// WithTenantReadTx) and a real OPA engine to resolve the scope layer 2 sets,
// so it gets its own testcontainers-backed TestMain, mirroring inventory's
// branch_authz_integration_test.go. enabled_modules_test.go (M1) shares this
// TestMain and its handler/fixture helpers.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	pgxpool "github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"go.uber.org/zap"

	tenanthttp "onlinemenu.tr/internal/modules/tenant/http"
	pub "onlinemenu.tr/internal/modules/tenant/public"
	"onlinemenu.tr/internal/modules/tenant/repo"
	"onlinemenu.tr/internal/modules/tenant/service"
	"onlinemenu.tr/internal/platform/auth"
	"onlinemenu.tr/internal/platform/db"
)

// Well-known system role ids, matching configs/opa/bundles/authz.rego and
// identity/000006_seed_system_roles.up.sql (mirrors auth.systemRoleUUID,
// which is unexported in the platform/auth package).
var (
	cashierRoleID = uuid.MustParse("00000001-0000-0000-0000-000000000001")
	managerRoleID = uuid.MustParse("00000001-0000-0000-0000-000000000006")
)

var httpSharedPool *db.Pool

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

	if err := httpBootstrapRoles(ctx, superDSN); err != nil {
		fmt.Fprintf(os.Stderr, "bootstrap roles: %v\n", err)
		_ = ctr.Terminate(ctx)
		os.Exit(1)
	}

	if err := httpRunMigrations(superDSN); err != nil {
		fmt.Fprintf(os.Stderr, "run migrations: %v\n", err)
		_ = ctr.Terminate(ctx)
		os.Exit(1)
	}

	httpSharedPool = httpNewPool(ctx, superDSN)

	rc := m.Run()

	httpSharedPool.Close()
	_ = ctr.Terminate(ctx)

	// NOTE: intentionally no goleak.Find here — newSmokeTestEngine
	// (authz_smoke_test.go, predates this task) leaks a redis dial-retry
	// goroutine against the deliberately-unreachable 127.0.0.1:1 address,
	// same pre-existing gap noted in inventory's equivalent TestMain.
	os.Exit(rc)
}

// httpMigrationsBase returns the absolute path to backend/migrations.
// File: .../backend/internal/modules/tenant/http/branch_directory_integration_test.go
// Walk up 4: http→tenant→modules→internal→backend
func httpMigrationsBase() string {
	_, file, _, _ := runtime.Caller(0)
	base := filepath.Dir(file)
	for range 4 {
		base = filepath.Dir(base)
	}
	return filepath.Join(base, "migrations")
}

func httpRunMigrations(superDSN string) error {
	cfg, err := pgxpool.ParseConfig(superDSN)
	if err != nil {
		return fmt.Errorf("parse dsn: %w", err)
	}
	migratorDSN := fmt.Sprintf("pgx5://%s:%s@%s/%s?sslmode=disable",
		"app_migrator", "migrator_secret",
		cfg.ConnConfig.Host+fmt.Sprintf(":%d", cfg.ConnConfig.Port),
		cfg.ConnConfig.Database,
	)

	absPath := filepath.Join(httpMigrationsBase(), "tenant")
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

func httpBootstrapRoles(ctx context.Context, superDSN string) error {
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

func httpNewPool(ctx context.Context, superDSN string) *db.Pool {
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

// newTestHandler wires a real Handler (real service on httpSharedPool, real
// OPA engine) exactly as fx would, minus DI.
func newTestHandler(t *testing.T) *tenanthttp.Handler {
	t.Helper()
	svc := service.NewService(service.Params{
		DB:         httpSharedPool,
		TenantRepo: repo.NewTenantRepo(),
		BranchRepo: repo.NewBranchRepo(),
		DocRepo:    repo.NewDocumentRepo(),
		IntRepo:    repo.NewIntegratorRepo(),
		HoursRepo:  repo.NewHoursRepo(),
		Logger:     zap.NewNop(),
	})
	return tenanthttp.NewHandler(svc, zap.NewNop(), newSmokeTestEngine(t))
}

func createTestTenant(t *testing.T, ctx context.Context, enabledModules []string) pub.Tenant {
	t.Helper()
	id, err := uuid.NewV7()
	require.NoError(t, err)
	// A random (v4) suffix, not derived from id: id is a v7 UUID whose leading
	// bytes are a millisecond timestamp, so two tenants created in the same
	// millisecond would otherwise collide on slug/tax_no/mersis_no.
	suffix := uuid.NewString()[:8]

	f := pub.Tenant{
		ID:             id,
		Name:           "Test İşletme " + suffix,
		LegalName:      "Test İşletme A.Ş. " + suffix,
		Slug:           "tenant-" + suffix,
		Plan:           pub.PlanStarter,
		EnabledModules: enabledModules,
		IdentityType:   pub.IdentityKurumsal,
		TaxNo:          "TAXNO" + suffix,
		MersisNo:       "MERSIS" + suffix,
		IsActive:       true,
	}

	var created pub.Tenant
	r := repo.NewTenantRepo()
	err = httpSharedPool.WithTenantTx(ctx, id, func(tx pgx.Tx) error {
		var err error
		created, err = r.Create(ctx, tx, f)
		return err
	})
	require.NoError(t, err)
	return created
}

func createTestBranch(t *testing.T, ctx context.Context, tenantID uuid.UUID) pub.Branch {
	t.Helper()
	suffix := uuid.NewString()[:8]

	f := pub.Branch{
		TenantID:      tenantID,
		Name:          "Şube " + suffix,
		Slug:          "branch-" + suffix,
		OwnershipType: pub.OwnershipSube,
		OperationType: pub.OperationRestoran,
		IdentityType:  pub.IdentityKurumsal,
		TaxNo:         "BTAXNO" + suffix,
		IBAN:          "TR000006200000123456789" + suffix[:2],
		LegalName:     "Şube Ticari Unvan A.Ş. " + suffix,
		IsActive:      true,
	}

	var created pub.Branch
	r := repo.NewBranchRepo()
	err := httpSharedPool.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		created, err = r.CreateBranch(ctx, tx, f)
		return err
	})
	require.NoError(t, err)
	return created
}

// TestListBranches_CashierPrincipal_GetsDirectoryProjectionOnly is the C1
// regression test: a branch-scoped principal (scope="branch") must never see
// a branch's IBAN, tax number or legal name.
func TestListBranches_CashierPrincipal_GetsDirectoryProjectionOnly(t *testing.T) {
	ctx := context.Background()
	tenant := createTestTenant(t, ctx, []string{"pos"})
	branch := createTestBranch(t, ctx, tenant.ID)

	h := newTestHandler(t)
	mux := chi.NewMux()
	h.RegisterRoutes(mux)

	cashier := auth.Principal{
		PersonID: uuid.New(),
		Ctx:      auth.ContextStaff,
		TenantID: tenant.ID,
		BranchID: branch.ID,
		RoleIDs:  []uuid.UUID{cashierRoleID},
	}

	req := httptest.NewRequest(http.MethodGet, "/tenants/"+tenant.ID.String()+"/branches", nil)
	req = req.WithContext(auth.WithPrincipal(req.Context(), cashier))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	var rows []map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &rows))
	require.Len(t, rows, 1)

	row := rows[0]
	require.Equal(t, branch.ID.String(), row["id"])
	require.Equal(t, branch.Name, row["name"])
	require.Contains(t, row, "is_active")
	for _, sensitive := range []string{"iban", "tax_no", "tax_office", "legal_name", "identity_type", "address", "phone", "supply_rules", "slug", "ownership_type", "operation_type"} {
		require.NotContainsf(t, row, sensitive, "cashier's branch directory row must not carry %q", sensitive)
	}
}

// TestListBranches_ManagerPrincipal_GetsFullRecord proves the projection
// above is scope-specific, not a blanket regression that hides the fields
// from everyone.
func TestListBranches_ManagerPrincipal_GetsFullRecord(t *testing.T) {
	ctx := context.Background()
	tenant := createTestTenant(t, ctx, []string{"pos"})
	branch := createTestBranch(t, ctx, tenant.ID)

	h := newTestHandler(t)
	mux := chi.NewMux()
	h.RegisterRoutes(mux)

	manager := auth.Principal{
		PersonID: uuid.New(),
		Ctx:      auth.ContextStaff,
		TenantID: tenant.ID,
		RoleIDs:  []uuid.UUID{managerRoleID},
	}

	req := httptest.NewRequest(http.MethodGet, "/tenants/"+tenant.ID.String()+"/branches", nil)
	req = req.WithContext(auth.WithPrincipal(req.Context(), manager))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	var rows []map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &rows))
	require.Len(t, rows, 1)

	row := rows[0]
	require.Equal(t, branch.IBAN, row["iban"])
	require.Equal(t, branch.LegalName, row["legal_name"])
	require.Contains(t, row, "tax_no")
}

// TestGetBranch_CashierPrincipal_GetsDirectoryProjectionOnly closes the
// single-branch half of C1: a branch-scoped principal reading its own branch
// gets the same directory projection as the list, never the IBAN/tax record.
func TestGetBranch_CashierPrincipal_GetsDirectoryProjectionOnly(t *testing.T) {
	ctx := context.Background()
	tenant := createTestTenant(t, ctx, []string{"pos"})
	branch := createTestBranch(t, ctx, tenant.ID)

	h := newTestHandler(t)
	mux := chi.NewMux()
	h.RegisterRoutes(mux)

	cashier := auth.Principal{
		PersonID: uuid.New(),
		Ctx:      auth.ContextStaff,
		TenantID: tenant.ID,
		BranchID: branch.ID,
		RoleIDs:  []uuid.UUID{cashierRoleID},
	}

	req := httptest.NewRequest(http.MethodGet, "/tenants/"+tenant.ID.String()+"/branches/"+branch.ID.String(), nil)
	req = req.WithContext(auth.WithPrincipal(req.Context(), cashier))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	var row map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &row))
	require.Equal(t, branch.ID.String(), row["id"])
	require.Equal(t, branch.Name, row["name"])
	require.Contains(t, row, "is_active")
	for _, sensitive := range []string{"iban", "tax_no", "tax_office", "legal_name", "identity_type", "address", "phone", "supply_rules", "slug", "ownership_type", "operation_type"} {
		require.NotContainsf(t, row, sensitive, "cashier's own-branch read must not carry %q", sensitive)
	}
}
