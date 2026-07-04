package http_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

	inventoryhttp "onlinemenu.tr/internal/modules/inventory/http"
	"onlinemenu.tr/internal/modules/inventory/repo"
	"onlinemenu.tr/internal/modules/inventory/service"
	"onlinemenu.tr/internal/platform/auth"
	"onlinemenu.tr/internal/platform/db"
)

// This file is the http-layer counterpart of
// service/branch_authz_test.go (ADR-AUTH-001 layer 3 / security sprint).
// Unlike the OPA-only 403 covered by authz_smoke_test.go (which never
// touches the database — the middleware denies before any handler runs),
// a branch-forbidden 403 is only reachable AFTER the target entity is
// loaded, so it requires a real persisted warehouse in a specific branch.
// Hence its own testcontainers-backed TestMain, mirroring
// service/integration_test.go.

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

	httpSharedPool = httpNewPool(ctx, superDSN, "app_runtime", "runtime_secret")

	rc := m.Run()

	httpSharedPool.Close()
	_ = ctr.Terminate(ctx)

	// NOTE: intentionally no goleak.Find here (unlike
	// service/integration_test.go's TestMain). This package's existing
	// newSmokeTestEngine helper (authz_smoke_test.go, predates this task)
	// constructs a redis.Client per test and never closes it, which leaks a
	// background dial-retry goroutine against the deliberately-unreachable
	// 127.0.0.1:1 address. That is a pre-existing test-hygiene gap outside
	// this task's scope (fixing it means touching every test that calls
	// newSmokeTestEngine); enforcing goleak here would fail on that
	// unrelated leak rather than on anything this task changed.

	os.Exit(rc)
}

// httpMigrationsBase returns the absolute path to backend/migrations.
// File: .../backend/internal/modules/inventory/http/branch_authz_integration_test.go
// Walk up 4: http→inventory→modules→internal→backend
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

	for _, mod := range []string{"tenant", "identity", "inventory"} {
		absPath := filepath.Join(httpMigrationsBase(), mod)
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

func httpNewPool(ctx context.Context, baseDSN, user, password string) *db.Pool {
	cfg, err := pgxpool.ParseConfig(baseDSN)
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse pool config: %v\n", err)
		os.Exit(1)
	}
	cfg.ConnConfig.User = user
	cfg.ConnConfig.Password = password
	cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	cfg.MaxConns = 10

	p, err := db.NewPoolFromConfig(ctx, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create pool (%s): %v\n", user, err)
		os.Exit(1)
	}
	return p
}

// newTestHandler wires a real Handler (real services on httpSharedPool, real
// OPA engine) exactly as fx would, minus DI — for driving an end-to-end HTTP
// request through RegisterRoutes.
func newTestHandler(t *testing.T) *inventoryhttp.Handler {
	t.Helper()
	whRepo := repo.NewWarehouseRepo()
	logger := zap.NewNop()

	warehouses := service.NewWarehouseService(service.WarehouseParams{DB: httpSharedPool, Repo: whRepo, Logger: logger})
	engine := newSmokeTestEngine(t)

	return inventoryhttp.NewHandler(inventoryhttp.Params{
		Svc:        service.NewInventoryService(service.Params{DB: httpSharedPool, LvlRepo: repo.NewStockLevelRepo(), MvRepo: repo.NewStockMovementRepo(), WhRepo: whRepo, Logger: logger}),
		StockItems: service.NewStockItemService(service.StockItemParams{DB: httpSharedPool, Repo: repo.NewStockItemRepo(), SupplyPolicyRepo: repo.NewSupplyPolicyRepo(), Logger: logger}),
		Warehouses: warehouses,
		Transfers:  service.NewTransferOrderService(service.TransferOrderParams{DB: httpSharedPool, Repo: repo.NewTransferOrderRepo(), ItemRepo: repo.NewTransferOrderItemRepo(), Logger: logger}),
		Shipments: service.NewShipmentService(service.ShipmentParams{
			DB: httpSharedPool, Repo: repo.NewShipmentRepo(), ItemRepo: repo.NewShipmentItemRepo(),
			LvlRepo: repo.NewStockLevelRepo(), MvRepo: repo.NewStockMovementRepo(),
			TransferRepo: repo.NewTransferOrderRepo(), TransferItem: repo.NewTransferOrderItemRepo(),
			WhRepo: whRepo, Logger: logger,
		}),
		SupplyPolicies: service.NewSupplyPolicyService(service.SupplyPolicyParams{
			DB: httpSharedPool, Repo: repo.NewSupplyPolicyRepo(), StockRepo: repo.NewStockItemRepo(), Logger: logger,
		}),
		Logger: logger,
		Engine: engine,
	})
}

// TestWarehouseUpdate_ForeignBranch_Returns403 proves the end-to-end path:
// a "warehouse" role principal (OPA allow=true, scope="branch" — passes
// layer 2) belonging to a DIFFERENT branch than the persisted warehouse gets
// 403 from the service's branch check (layer 3), not a silent 200 or a
// leaking 404/409. Uses the real chi router + real OPA engine + real DB, the
// same wiring production uses (module.go), only assembled by hand instead of
// fx.
func TestWarehouseUpdate_ForeignBranch_Returns403(t *testing.T) {
	tenantID := uuid.New()
	ownerBranch := uuid.New()
	foreignBranch := uuid.New()

	h := newTestHandler(t)
	mux := chi.NewMux()
	h.RegisterRoutes(mux)

	// Seed a warehouse that belongs to ownerBranch, acting as a chain-wide
	// (manager-equivalent) principal so creation itself isn't blocked by the
	// very check under test.
	wh, err := service.NewWarehouseService(service.WarehouseParams{DB: httpSharedPool, Repo: repo.NewWarehouseRepo(), Logger: zap.NewNop()}).
		Create(context.Background(), tenantID, service.CreateWarehouseRequest{BranchID: ownerBranch, Name: "Depo", WarehouseType: "depo"})
	require.NoError(t, err)

	foreignPrincipal := auth.Principal{
		PersonID: uuid.New(),
		Ctx:      auth.ContextStaff,
		TenantID: tenantID,
		BranchID: foreignBranch,
		RoleIDs:  []uuid.UUID{warehouseRoleID}, // allowed at OPA layer, scope=branch
	}

	body := fmt.Sprintf(`{"branch_id":%q,"name":"Depo Updated","warehouse_type":"depo"}`, ownerBranch)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/inventory/warehouses/"+wh.ID.String(), strings.NewReader(body))
	req = req.WithContext(auth.WithPrincipal(req.Context(), foreignPrincipal))
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code, "foreign-branch warehouse role principal must be forbidden, got body: %s", rec.Body.String())
}

// TestWarehouseUpdate_OwnBranch_Succeeds is the positive counterpart, proving
// the 403 above is actually branch-specific and not a wiring bug that denies
// everyone.
func TestWarehouseUpdate_OwnBranch_Succeeds(t *testing.T) {
	tenantID := uuid.New()
	ownerBranch := uuid.New()

	h := newTestHandler(t)
	mux := chi.NewMux()
	h.RegisterRoutes(mux)

	wh, err := service.NewWarehouseService(service.WarehouseParams{DB: httpSharedPool, Repo: repo.NewWarehouseRepo(), Logger: zap.NewNop()}).
		Create(context.Background(), tenantID, service.CreateWarehouseRequest{BranchID: ownerBranch, Name: "Depo", WarehouseType: "depo"})
	require.NoError(t, err)

	ownerPrincipal := auth.Principal{
		PersonID: uuid.New(),
		Ctx:      auth.ContextStaff,
		TenantID: tenantID,
		BranchID: ownerBranch,
		RoleIDs:  []uuid.UUID{warehouseRoleID},
	}

	body := fmt.Sprintf(`{"branch_id":%q,"name":"Depo Updated","warehouse_type":"depo"}`, ownerBranch)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/inventory/warehouses/"+wh.ID.String(), strings.NewReader(body))
	req = req.WithContext(auth.WithPrincipal(req.Context(), ownerPrincipal))
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "own-branch warehouse role principal must succeed, got body: %s", rec.Body.String())
}
