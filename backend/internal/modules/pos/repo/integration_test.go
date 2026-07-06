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

	"onlinemenu.tr/internal/modules/pos/domain"
	"onlinemenu.tr/internal/modules/pos/repo"
	"onlinemenu.tr/internal/platform/db"
)

var (
	sharedPool *db.Pool
	tenantA    = uuid.MustParse("aaaaaaaa-0000-0000-0000-000000000001")
	tenantB    = uuid.MustParse("bbbbbbbb-0000-0000-0000-000000000001")
	branchA    = uuid.MustParse("cccccccc-0000-0000-0000-000000000001")
	staffA     = uuid.New()
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
// File: .../backend/internal/modules/pos/repo/integration_test.go
// Walk up 4 directories: repo/ → pos/ → modules/ → internal/ → backend/
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

	for _, mod := range []string{"tenant", "identity", "catalog", "pos"} {
		absPath := filepath.Join(migrationsBase(), mod)
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
			end := len(s)
			if end > 60 {
				end = 60
			}
			return fmt.Errorf("stmt failed %q: %w", s[:end], err)
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

// ---------------------------------------------------------------------------
// Check repo tests
// ---------------------------------------------------------------------------

func TestCheckRepo_CRUD(t *testing.T) {
	ctx := context.Background()
	checkRepo := repo.NewCheckRepo()

	var created domain.Check
	err := sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		var err error
		created, err = checkRepo.Create(ctx, tx, domain.Check{
			TenantID:   tenantA,
			BranchID:   branchA,
			TableLabel: "Masa 5",
			Status:     domain.CheckStatusOpen,
			OpenedBy:   staffA,
		})
		return err
	})
	require.NoError(t, err)
	assert.NotEqual(t, uuid.Nil, created.ID)
	assert.Equal(t, domain.CheckStatusOpen, created.Status)
	assert.Equal(t, "Masa 5", created.TableLabel)

	var fetched domain.Check
	err = sharedPool.WithTenantReadTx(ctx, tenantA, func(tx pgx.Tx) error {
		var err error
		fetched, err = checkRepo.GetByID(ctx, tx, created.ID)
		return err
	})
	require.NoError(t, err)
	assert.Equal(t, created.ID, fetched.ID)
	assert.Equal(t, tenantA, fetched.TenantID)

	var closed domain.Check
	err = sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		var err error
		closed, err = checkRepo.UpdateStatus(ctx, tx, created.ID, domain.CheckStatusClosed, domain.CheckStatusOpen, &staffA)
		return err
	})
	require.NoError(t, err)
	assert.Equal(t, domain.CheckStatusClosed, closed.Status)
	assert.NotNil(t, closed.ClosedAt)
	assert.Equal(t, &staffA, closed.ClosedBy)
}

// TestCheckRepo_List_FiltersByStatusAndBranch is the regression test for
// listChecks previously ignoring the status/branch_id query filters (task
// #25 item 1): it seeds two branches and a mix of open/closed checks, then
// asserts each optional ListFilter field narrows the result set
// independently, and that a nil filter still returns everything (unchanged
// prior behavior).
func TestCheckRepo_List_FiltersByStatusAndBranch(t *testing.T) {
	ctx := context.Background()
	checkRepo := repo.NewCheckRepo()
	branchOther := uuid.New()

	var openA, closedA, openOther domain.Check
	err := sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		var err error
		openA, err = checkRepo.Create(ctx, tx, domain.Check{
			TenantID: tenantA, BranchID: branchA, TableLabel: "Masa Open A",
			Status: domain.CheckStatusOpen, OpenedBy: staffA,
		})
		if err != nil {
			return err
		}
		closedA, err = checkRepo.Create(ctx, tx, domain.Check{
			TenantID: tenantA, BranchID: branchA, TableLabel: "Masa Closed A",
			Status: domain.CheckStatusOpen, OpenedBy: staffA,
		})
		if err != nil {
			return err
		}
		closedA, err = checkRepo.UpdateStatus(ctx, tx, closedA.ID, domain.CheckStatusClosed, domain.CheckStatusOpen, &staffA)
		if err != nil {
			return err
		}
		openOther, err = checkRepo.Create(ctx, tx, domain.Check{
			TenantID: tenantA, BranchID: branchOther, TableLabel: "Masa Other Branch",
			Status: domain.CheckStatusOpen, OpenedBy: staffA,
		})
		return err
	})
	require.NoError(t, err)

	// No filter: every check for the tenant comes back.
	err = sharedPool.WithTenantReadTx(ctx, tenantA, func(tx pgx.Tx) error {
		all, err := checkRepo.List(ctx, tx, repo.ListFilter{})
		if err != nil {
			return err
		}
		ids := checkIDs(all)
		assert.Contains(t, ids, openA.ID)
		assert.Contains(t, ids, closedA.ID)
		assert.Contains(t, ids, openOther.ID)
		return nil
	})
	require.NoError(t, err)

	// status=open must exclude closedA.
	openStatus := domain.CheckStatusOpen
	err = sharedPool.WithTenantReadTx(ctx, tenantA, func(tx pgx.Tx) error {
		open, err := checkRepo.List(ctx, tx, repo.ListFilter{Status: &openStatus})
		if err != nil {
			return err
		}
		ids := checkIDs(open)
		assert.Contains(t, ids, openA.ID)
		assert.Contains(t, ids, openOther.ID)
		assert.NotContains(t, ids, closedA.ID)
		return nil
	})
	require.NoError(t, err)

	// branch_id=branchA must exclude openOther (a different branch).
	err = sharedPool.WithTenantReadTx(ctx, tenantA, func(tx pgx.Tx) error {
		branchAOnly, err := checkRepo.List(ctx, tx, repo.ListFilter{BranchID: &branchA})
		if err != nil {
			return err
		}
		ids := checkIDs(branchAOnly)
		assert.Contains(t, ids, openA.ID)
		assert.Contains(t, ids, closedA.ID)
		assert.NotContains(t, ids, openOther.ID)
		return nil
	})
	require.NoError(t, err)

	// Combined: status=open AND branch_id=branchA must include openA but
	// exclude both closedA (wrong status) and openOther (wrong branch).
	// (Not asserting an exact result set: branchA/tenantA are shared fixture
	// IDs reused by sibling tests in this file, so other open checks in
	// branchA may legitimately coexist.)
	err = sharedPool.WithTenantReadTx(ctx, tenantA, func(tx pgx.Tx) error {
		combined, err := checkRepo.List(ctx, tx, repo.ListFilter{Status: &openStatus, BranchID: &branchA})
		if err != nil {
			return err
		}
		ids := checkIDs(combined)
		assert.Contains(t, ids, openA.ID)
		assert.NotContains(t, ids, closedA.ID)
		assert.NotContains(t, ids, openOther.ID)
		return nil
	})
	require.NoError(t, err)
}

func checkIDs(checks []domain.Check) []uuid.UUID {
	ids := make([]uuid.UUID, len(checks))
	for i, c := range checks {
		ids[i] = c.ID
	}
	return ids
}

func TestCheckRepo_RLSIsolation(t *testing.T) {
	ctx := context.Background()
	checkRepo := repo.NewCheckRepo()

	// Create a check under tenantA
	var checkA domain.Check
	err := sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		var err error
		checkA, err = checkRepo.Create(ctx, tx, domain.Check{
			TenantID:   tenantA,
			BranchID:   branchA,
			TableLabel: "RLS Masa",
			Status:     domain.CheckStatusOpen,
			OpenedBy:   staffA,
		})
		return err
	})
	require.NoError(t, err)

	// tenantB cannot see tenantA's check
	err = sharedPool.WithTenantReadTx(ctx, tenantB, func(tx pgx.Tx) error {
		_, err := checkRepo.GetByID(ctx, tx, checkA.ID)
		return err
	})
	assert.ErrorIs(t, err, repo.ErrNotFound, "tenantB must not see tenantA's check")
}

// TestCheckRepo_Create_SecondOpenCheckOnSameTable_MapsUniqueViolation is the
// regression test for checks_open_table_id_uidx's backstop role: it
// exercises the DB constraint directly (bypassing CheckService.Open's row
// lock, which is what a manual table-status reset + concurrent Open would
// effectively do) and asserts the resulting unique_violation is translated
// to repo.ErrTableOccupied rather than surfacing as a raw, unmapped pg error
// (which service.CheckService.Open would otherwise return as an
// unrecognized 500).
func TestCheckRepo_Create_SecondOpenCheckOnSameTable_MapsUniqueViolation(t *testing.T) {
	ctx := context.Background()
	checkRepo := repo.NewCheckRepo()
	z := newTestZone(t, ctx, tenantA, branchA)
	tbl := newTestTable(t, ctx, tenantA, branchA, z.ID)

	err := sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		_, err := checkRepo.Create(ctx, tx, domain.Check{
			TenantID: tenantA, BranchID: branchA, TableID: &tbl.ID, TableLabel: tbl.Name,
			Status: domain.CheckStatusOpen, OpenedBy: staffA,
		})
		return err
	})
	require.NoError(t, err)

	err = sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		_, err := checkRepo.Create(ctx, tx, domain.Check{
			TenantID: tenantA, BranchID: branchA, TableID: &tbl.ID, TableLabel: tbl.Name,
			Status: domain.CheckStatusOpen, OpenedBy: staffA,
		})
		return err
	})
	assert.ErrorIs(t, err, repo.ErrTableOccupied)
}

// TestCheckRepo_GetTotal is the regression test for the money bug: GetTotal
// must sum only orders whose status is not in domain.InactiveOrderStatuses
// (rejected/cancelled). Every order row is inserted directly via
// OrderRepo.Create with its final status set verbatim (Create writes o.Status
// as given — no transition machine involved), so this table exercises the
// repo query in isolation from the order status state machine.
func TestCheckRepo_GetTotal(t *testing.T) {
	ctx := context.Background()
	checkRepo := repo.NewCheckRepo()
	orderRepo := repo.NewOrderRepo()

	newOrderWithStatus := func(t *testing.T, checkID uuid.UUID, status domain.OrderStatus, unitPrice int64, qty int) {
		t.Helper()
		err := sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
			_, err := orderRepo.Create(ctx, tx, domain.Order{
				TenantID:     tenantA,
				BranchID:     branchA,
				CheckID:      &checkID,
				OrderChannel: domain.OrderChannelDineIn,
				Status:       status,
				Items: []domain.OrderItem{
					{
						ProductID:          uuid.New(),
						ProductName:        "Test Item",
						ProductPriceAmount: unitPrice,
						ProductCurrency:    "TRY",
						TaxRateBPS:         1000,
						Quantity:           qty,
						UnitPriceAmount:    unitPrice,
					},
				},
			})
			return err
		})
		require.NoError(t, err)
	}

	getTotal := func(t *testing.T, checkID uuid.UUID) int64 {
		t.Helper()
		var total int64
		err := sharedPool.WithTenantReadTx(ctx, tenantA, func(tx pgx.Tx) error {
			var err error
			total, err = checkRepo.GetTotal(ctx, tx, checkID)
			return err
		})
		require.NoError(t, err)
		return total
	}

	tests := []struct {
		name   string
		orders []struct {
			status    domain.OrderStatus
			unitPrice int64
			qty       int
		}
		wantTotal int64
	}{
		{
			name:      "no orders",
			orders:    nil,
			wantTotal: 0,
		},
		{
			name: "single active order",
			orders: []struct {
				status    domain.OrderStatus
				unitPrice int64
				qty       int
			}{
				{domain.OrderStatusPending, 1500, 2},
			},
			wantTotal: 3000,
		},
		{
			name: "rejected order excluded",
			orders: []struct {
				status    domain.OrderStatus
				unitPrice int64
				qty       int
			}{
				{domain.OrderStatusPending, 1500, 1},
				{domain.OrderStatusRejected, 5000, 1},
			},
			wantTotal: 1500,
		},
		{
			name: "cancelled order excluded",
			orders: []struct {
				status    domain.OrderStatus
				unitPrice int64
				qty       int
			}{
				{domain.OrderStatusAccepted, 2000, 1},
				{domain.OrderStatusCancelled, 9000, 3},
			},
			wantTotal: 2000,
		},
		{
			name: "mixed active statuses all counted, only inactive excluded",
			orders: []struct {
				status    domain.OrderStatus
				unitPrice int64
				qty       int
			}{
				{domain.OrderStatusPending, 1000, 1},
				{domain.OrderStatusAccepted, 1000, 1},
				{domain.OrderStatusPreparing, 1000, 1},
				{domain.OrderStatusReady, 1000, 1},
				{domain.OrderStatusDelivered, 1000, 1},
				{domain.OrderStatusRejected, 1000, 1},
				{domain.OrderStatusCancelled, 1000, 1},
			},
			wantTotal: 5000,
		},
		{
			name: "all orders rejected or cancelled",
			orders: []struct {
				status    domain.OrderStatus
				unitPrice int64
				qty       int
			}{
				{domain.OrderStatusRejected, 4000, 1},
				{domain.OrderStatusCancelled, 6000, 1},
			},
			wantTotal: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			check := newTestCheck(t, ctx, tenantA)
			for _, o := range tt.orders {
				newOrderWithStatus(t, check.ID, o.status, o.unitPrice, o.qty)
			}
			assert.Equal(t, tt.wantTotal, getTotal(t, check.ID))
		})
	}
}

// TestCheckRepo_GetTotal_DropsAfterRejection is the "toplam düştü" regression
// scenario called out explicitly by the bug report: an order that counted
// toward the total while pending must stop counting once rejected — the
// customer must not be charged for it.
func TestCheckRepo_GetTotal_DropsAfterRejection(t *testing.T) {
	ctx := context.Background()
	checkRepo := repo.NewCheckRepo()
	orderRepo := repo.NewOrderRepo()

	check := newTestCheck(t, ctx, tenantA)

	var orderID uuid.UUID
	err := sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		o, err := orderRepo.Create(ctx, tx, domain.Order{
			TenantID:     tenantA,
			BranchID:     branchA,
			CheckID:      &check.ID,
			OrderChannel: domain.OrderChannelDineIn,
			Status:       domain.OrderStatusPending,
			Items: []domain.OrderItem{
				{
					ProductID:          uuid.New(),
					ProductName:        "İskender",
					ProductPriceAmount: 18000,
					ProductCurrency:    "TRY",
					TaxRateBPS:         1000,
					Quantity:           1,
					UnitPriceAmount:    18000,
				},
			},
		})
		orderID = o.ID
		return err
	})
	require.NoError(t, err)

	var beforeReject int64
	err = sharedPool.WithTenantReadTx(ctx, tenantA, func(tx pgx.Tx) error {
		var err error
		beforeReject, err = checkRepo.GetTotal(ctx, tx, check.ID)
		return err
	})
	require.NoError(t, err)
	assert.Equal(t, int64(18000), beforeReject, "pending order must count toward the total")

	err = sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		_, err := orderRepo.Reject(ctx, tx, orderID, staffA, "kitchen out of stock", domain.OrderStatusPending)
		return err
	})
	require.NoError(t, err)

	var afterReject int64
	err = sharedPool.WithTenantReadTx(ctx, tenantA, func(tx pgx.Tx) error {
		var err error
		afterReject, err = checkRepo.GetTotal(ctx, tx, check.ID)
		return err
	})
	require.NoError(t, err)
	assert.Equal(t, int64(0), afterReject, "total must drop to 0 once the only order is rejected")
}

// TestCheckRepo_Pax_RoundTrips is the regression test for the pax column
// added in 000005_add_check_pax: a client-supplied guest count must survive
// Create's RETURNING, a subsequent GetByID, and List — the three read paths
// checkResponse (HTTP layer) is built from.
func TestCheckRepo_Pax_RoundTrips(t *testing.T) {
	ctx := context.Background()
	checkRepo := repo.NewCheckRepo()

	var created domain.Check
	err := sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		var err error
		created, err = checkRepo.Create(ctx, tx, domain.Check{
			TenantID: tenantA, BranchID: branchA, TableLabel: "Masa Pax",
			Pax: 6, Status: domain.CheckStatusOpen, OpenedBy: staffA,
		})
		return err
	})
	require.NoError(t, err)
	assert.Equal(t, 6, created.Pax, "Create's RETURNING must include pax")

	err = sharedPool.WithTenantReadTx(ctx, tenantA, func(tx pgx.Tx) error {
		fetched, err := checkRepo.GetByID(ctx, tx, created.ID)
		if err != nil {
			return err
		}
		assert.Equal(t, 6, fetched.Pax, "GetByID must return pax")

		listed, err := checkRepo.List(ctx, tx, repo.ListFilter{BranchID: &branchA})
		if err != nil {
			return err
		}
		found := false
		for _, c := range listed {
			if c.ID == created.ID {
				found = true
				assert.Equal(t, 6, c.Pax, "List must return pax for every row")
			}
		}
		assert.True(t, found, "created check must appear in List's branch-filtered result")
		return nil
	})
	require.NoError(t, err)
}

// TestCheckRepo_TotalsByCheckIDs_MatchesGetTotal_AndExcludesRejected is the
// batch-vs-single consistency test: TotalsByCheckIDs must agree with
// GetTotal for every check in the batch (both are backed by the same query
// shape, see TotalsByCheckIDs's doc comment), and — the money-bug regression
// — a rejected order's items must never appear in either.
func TestCheckRepo_TotalsByCheckIDs_MatchesGetTotal_AndExcludesRejected(t *testing.T) {
	ctx := context.Background()
	checkRepo := repo.NewCheckRepo()
	orderRepo := repo.NewOrderRepo()

	checkActive := newTestCheck(t, ctx, tenantA)
	checkMixed := newTestCheck(t, ctx, tenantA)
	checkEmpty := newTestCheck(t, ctx, tenantA)

	newOrder := func(checkID uuid.UUID, status domain.OrderStatus, unitPrice int64, qty int) {
		t.Helper()
		err := sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
			_, err := orderRepo.Create(ctx, tx, domain.Order{
				TenantID:     tenantA,
				BranchID:     branchA,
				CheckID:      &checkID,
				OrderChannel: domain.OrderChannelDineIn,
				Status:       status,
				Items: []domain.OrderItem{
					{
						ProductID:          uuid.New(),
						ProductName:        "Test Item",
						ProductPriceAmount: unitPrice,
						ProductCurrency:    "TRY",
						TaxRateBPS:         1000,
						Quantity:           qty,
						UnitPriceAmount:    unitPrice,
					},
				},
			})
			return err
		})
		require.NoError(t, err)
	}

	newOrder(checkActive.ID, domain.OrderStatusPending, 1000, 2) // 2000
	newOrder(checkMixed.ID, domain.OrderStatusAccepted, 2500, 1) // 2500, active
	newOrder(checkMixed.ID, domain.OrderStatusRejected, 9999, 5) // excluded
	// checkEmpty gets no orders at all.

	var totals map[uuid.UUID]int64
	err := sharedPool.WithTenantReadTx(ctx, tenantA, func(tx pgx.Tx) error {
		var err error
		totals, err = checkRepo.TotalsByCheckIDs(ctx, tx, []uuid.UUID{checkActive.ID, checkMixed.ID, checkEmpty.ID})
		return err
	})
	require.NoError(t, err)

	assert.Equal(t, int64(2000), totals[checkActive.ID])
	assert.Equal(t, int64(2500), totals[checkMixed.ID], "rejected order must not count toward the batch total")
	assert.Equal(t, int64(0), totals[checkEmpty.ID], "check with no orders is absent from the GROUP BY result, map returns 0")

	// Cross-check against the single-key GetTotal for every id in the batch.
	for _, id := range []uuid.UUID{checkActive.ID, checkMixed.ID, checkEmpty.ID} {
		var single int64
		err := sharedPool.WithTenantReadTx(ctx, tenantA, func(tx pgx.Tx) error {
			var err error
			single, err = checkRepo.GetTotal(ctx, tx, id)
			return err
		})
		require.NoError(t, err)
		assert.Equal(t, single, totals[id], "GetTotal and TotalsByCheckIDs must agree for check %s", id)
	}
}

// TestCheckRepo_TotalsByCheckIDs_EmptyInput_ReturnsEmptyMap guards the
// short-circuit: an empty id slice must not run a query at all, and must
// return an empty (non-nil) map rather than erroring.
func TestCheckRepo_TotalsByCheckIDs_EmptyInput_ReturnsEmptyMap(t *testing.T) {
	ctx := context.Background()
	checkRepo := repo.NewCheckRepo()

	var totals map[uuid.UUID]int64
	err := sharedPool.WithTenantReadTx(ctx, tenantA, func(tx pgx.Tx) error {
		var err error
		totals, err = checkRepo.TotalsByCheckIDs(ctx, tx, nil)
		return err
	})
	require.NoError(t, err)
	assert.NotNil(t, totals)
	assert.Empty(t, totals)
}

// ---------------------------------------------------------------------------
// Order repo tests
// ---------------------------------------------------------------------------

func newTestCheck(t *testing.T, ctx context.Context, tenantID uuid.UUID) domain.Check {
	t.Helper()
	checkRepo := repo.NewCheckRepo()
	var c domain.Check
	err := sharedPool.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		c, err = checkRepo.Create(ctx, tx, domain.Check{
			TenantID:   tenantID,
			BranchID:   branchA,
			TableLabel: "Test Masa",
			Status:     domain.CheckStatusOpen,
			OpenedBy:   staffA,
		})
		return err
	})
	require.NoError(t, err)
	return c
}

func TestOrderRepo_Create_WithItems(t *testing.T) {
	ctx := context.Background()
	orderRepo := repo.NewOrderRepo()
	check := newTestCheck(t, ctx, tenantA)

	productID := uuid.New()
	var created domain.Order
	err := sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		var err error
		created, err = orderRepo.Create(ctx, tx, domain.Order{
			TenantID:     tenantA,
			BranchID:     branchA,
			CheckID:      &check.ID,
			OrderChannel: domain.OrderChannelDineIn,
			Status:       domain.OrderStatusPending,
			Items: []domain.OrderItem{
				{
					ProductID:          productID,
					ProductName:        "Adana Kebap",
					ProductPriceAmount: 25000,
					ProductCurrency:    "TRY",
					TaxRateBPS:         1000,
					Quantity:           2,
					UnitPriceAmount:    25000,
				},
			},
		})
		return err
	})
	require.NoError(t, err)
	assert.NotEqual(t, uuid.Nil, created.ID)
	assert.Equal(t, domain.OrderStatusPending, created.Status)
	assert.Equal(t, domain.OrderChannelDineIn, created.OrderChannel)
	require.Len(t, created.Items, 1)
	assert.Equal(t, "Adana Kebap", created.Items[0].ProductName)
	assert.Equal(t, 2, created.Items[0].Quantity)
}

func TestOrderRepo_GetByID_WithItems(t *testing.T) {
	ctx := context.Background()
	orderRepo := repo.NewOrderRepo()
	check := newTestCheck(t, ctx, tenantA)

	var orderID uuid.UUID
	err := sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		o, err := orderRepo.Create(ctx, tx, domain.Order{
			TenantID:     tenantA,
			BranchID:     branchA,
			CheckID:      &check.ID,
			OrderChannel: domain.OrderChannelDineIn,
			Status:       domain.OrderStatusPending,
			Items: []domain.OrderItem{
				{
					ProductID:          uuid.New(),
					ProductName:        "Mercimek Çorbası",
					ProductPriceAmount: 8000,
					ProductCurrency:    "TRY",
					TaxRateBPS:         1000,
					Quantity:           1,
					UnitPriceAmount:    8000,
				},
			},
		})
		if err == nil {
			orderID = o.ID
		}
		return err
	})
	require.NoError(t, err)

	var fetched domain.Order
	err = sharedPool.WithTenantReadTx(ctx, tenantA, func(tx pgx.Tx) error {
		var err error
		fetched, err = orderRepo.GetByID(ctx, tx, orderID)
		return err
	})
	require.NoError(t, err)
	assert.Equal(t, orderID, fetched.ID)
	require.Len(t, fetched.Items, 1)
	assert.Equal(t, "Mercimek Çorbası", fetched.Items[0].ProductName)
}

func TestOrderRepo_Accept_Reject(t *testing.T) {
	ctx := context.Background()
	orderRepo := repo.NewOrderRepo()

	// delivery order (no check)
	var deliveryOrderID uuid.UUID
	err := sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		o, err := orderRepo.Create(ctx, tx, domain.Order{
			TenantID:     tenantA,
			BranchID:     branchA,
			OrderChannel: domain.OrderChannelDelivery,
			Status:       domain.OrderStatusPending,
			Items: []domain.OrderItem{
				{
					ProductID:          uuid.New(),
					ProductName:        "Lahmacun",
					ProductPriceAmount: 12000,
					ProductCurrency:    "TRY",
					TaxRateBPS:         1000,
					Quantity:           3,
					UnitPriceAmount:    12000,
				},
			},
		})
		if err == nil {
			deliveryOrderID = o.ID
		}
		return err
	})
	require.NoError(t, err)

	var accepted domain.Order
	err = sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		var err error
		accepted, err = orderRepo.Accept(ctx, tx, deliveryOrderID, staffA, domain.OrderStatusPending)
		return err
	})
	require.NoError(t, err)
	assert.Equal(t, domain.OrderStatusAccepted, accepted.Status)
	assert.NotNil(t, accepted.AcceptedAt)
	assert.Equal(t, &staffA, accepted.AcceptedBy)

	// Create another order to test rejection
	var rejectOrderID uuid.UUID
	err = sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		o, err := orderRepo.Create(ctx, tx, domain.Order{
			TenantID:     tenantA,
			BranchID:     branchA,
			OrderChannel: domain.OrderChannelDelivery,
			Status:       domain.OrderStatusPending,
			Items: []domain.OrderItem{
				{
					ProductID:          uuid.New(),
					ProductName:        "Pide",
					ProductPriceAmount: 18000,
					ProductCurrency:    "TRY",
					TaxRateBPS:         1000,
					Quantity:           1,
					UnitPriceAmount:    18000,
				},
			},
		})
		if err == nil {
			rejectOrderID = o.ID
		}
		return err
	})
	require.NoError(t, err)

	var rejected domain.Order
	err = sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		var err error
		rejected, err = orderRepo.Reject(ctx, tx, rejectOrderID, staffA, "ürün stokta yok", domain.OrderStatusPending)
		return err
	})
	require.NoError(t, err)
	assert.Equal(t, domain.OrderStatusRejected, rejected.Status)
	assert.Equal(t, "ürün stokta yok", rejected.RejectionReason)
	assert.NotNil(t, rejected.RejectedAt)
}

func TestOrderRepo_RLSIsolation(t *testing.T) {
	ctx := context.Background()
	orderRepo := repo.NewOrderRepo()

	var orderA domain.Order
	err := sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		var err error
		orderA, err = orderRepo.Create(ctx, tx, domain.Order{
			TenantID:     tenantA,
			BranchID:     branchA,
			OrderChannel: domain.OrderChannelTakeaway,
			Status:       domain.OrderStatusPending,
			Items: []domain.OrderItem{
				{
					ProductID:          uuid.New(),
					ProductName:        "Döner",
					ProductPriceAmount: 15000,
					ProductCurrency:    "TRY",
					TaxRateBPS:         1000,
					Quantity:           1,
					UnitPriceAmount:    15000,
				},
			},
		})
		return err
	})
	require.NoError(t, err)

	// tenantB cannot see tenantA's order
	err = sharedPool.WithTenantReadTx(ctx, tenantB, func(tx pgx.Tx) error {
		_, err := orderRepo.GetByID(ctx, tx, orderA.ID)
		return err
	})
	assert.ErrorIs(t, err, repo.ErrNotFound, "tenantB must not see tenantA's order")
}

// ---------------------------------------------------------------------------
// Table plan repo tests (Sprint-5 Wave 1)
// ---------------------------------------------------------------------------

func newTestZone(t *testing.T, ctx context.Context, tenantID, branchID uuid.UUID) domain.TableZone {
	t.Helper()
	tableRepo := repo.NewTableRepo()
	var z domain.TableZone
	err := sharedPool.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		z, err = tableRepo.CreateZone(ctx, tx, domain.TableZone{
			TenantID: tenantID,
			BranchID: branchID,
			Name:     "Zemin Kat",
			Floor:    0,
			IsActive: true,
		})
		return err
	})
	require.NoError(t, err)
	return z
}

func newTestTable(t *testing.T, ctx context.Context, tenantID, branchID, zoneID uuid.UUID) domain.Table {
	t.Helper()
	tableRepo := repo.NewTableRepo()
	var tbl domain.Table
	err := sharedPool.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		tbl, err = tableRepo.CreateTable(ctx, tx, domain.Table{
			TenantID: tenantID,
			BranchID: branchID,
			ZoneID:   zoneID,
			Name:     "Masa 1",
			Capacity: 4,
			IsActive: true,
		})
		return err
	})
	require.NoError(t, err)
	return tbl
}

func TestTableRepo_ZoneCRUD(t *testing.T) {
	ctx := context.Background()
	tableRepo := repo.NewTableRepo()

	z := newTestZone(t, ctx, tenantA, branchA)
	assert.NotEqual(t, uuid.Nil, z.ID)
	assert.Equal(t, "Zemin Kat", z.Name)

	var fetched domain.TableZone
	err := sharedPool.WithTenantReadTx(ctx, tenantA, func(tx pgx.Tx) error {
		var err error
		fetched, err = tableRepo.GetZoneByID(ctx, tx, z.ID)
		return err
	})
	require.NoError(t, err)
	assert.Equal(t, z.ID, fetched.ID)

	var updated domain.TableZone
	err = sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		var err error
		updated, err = tableRepo.UpdateZone(ctx, tx, domain.TableZone{ID: z.ID, Name: "Teras", Floor: 1, IsActive: false})
		return err
	})
	require.NoError(t, err)
	assert.Equal(t, "Teras", updated.Name)
	assert.Equal(t, 1, updated.Floor)
	assert.False(t, updated.IsActive)
}

func TestTableRepo_ZoneRLSIsolation(t *testing.T) {
	ctx := context.Background()
	tableRepo := repo.NewTableRepo()
	z := newTestZone(t, ctx, tenantA, branchA)

	err := sharedPool.WithTenantReadTx(ctx, tenantB, func(tx pgx.Tx) error {
		_, err := tableRepo.GetZoneByID(ctx, tx, z.ID)
		return err
	})
	assert.ErrorIs(t, err, repo.ErrNotFound, "tenantB must not see tenantA's zone")
}

func TestTableRepo_TableCRUD(t *testing.T) {
	ctx := context.Background()
	tableRepo := repo.NewTableRepo()
	z := newTestZone(t, ctx, tenantA, branchA)

	tbl := newTestTable(t, ctx, tenantA, branchA, z.ID)
	assert.NotEqual(t, uuid.Nil, tbl.ID)
	assert.Equal(t, domain.TableStatusEmpty, tbl.Status)
	assert.Equal(t, 4, tbl.Capacity)

	var fetched domain.Table
	err := sharedPool.WithTenantReadTx(ctx, tenantA, func(tx pgx.Tx) error {
		var err error
		fetched, err = tableRepo.GetTableByID(ctx, tx, tbl.ID)
		return err
	})
	require.NoError(t, err)
	assert.Equal(t, tbl.ID, fetched.ID)

	var updated domain.Table
	err = sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		var err error
		updated, err = tableRepo.UpdateTable(ctx, tx, domain.Table{
			ID: tbl.ID, ZoneID: z.ID, Name: "Masa 1-A", Capacity: 6, IsActive: true,
		})
		return err
	})
	require.NoError(t, err)
	assert.Equal(t, "Masa 1-A", updated.Name)
	assert.Equal(t, 6, updated.Capacity)
}

func TestTableRepo_UpdateStatus_GuardedTransition(t *testing.T) {
	ctx := context.Background()
	tableRepo := repo.NewTableRepo()
	z := newTestZone(t, ctx, tenantA, branchA)
	tbl := newTestTable(t, ctx, tenantA, branchA, z.ID)

	var occupied domain.Table
	err := sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		var err error
		occupied, err = tableRepo.UpdateStatus(ctx, tx, tbl.ID, domain.TableStatusOccupied, domain.TableStatusEmpty)
		return err
	})
	require.NoError(t, err)
	assert.Equal(t, domain.TableStatusOccupied, occupied.Status)

	// Guard: expected status no longer matches (already occupied) => ErrInvalidTransition.
	err = sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		_, err := tableRepo.UpdateStatus(ctx, tx, tbl.ID, domain.TableStatusOccupied, domain.TableStatusEmpty)
		return err
	})
	assert.ErrorIs(t, err, repo.ErrInvalidTransition)
}

func TestTableRepo_UpdateStatusIfCurrent_ToleratesMismatch(t *testing.T) {
	ctx := context.Background()
	tableRepo := repo.NewTableRepo()
	z := newTestZone(t, ctx, tenantA, branchA)
	tbl := newTestTable(t, ctx, tenantA, branchA, z.ID) // starts "empty"

	// fromStatus does not match current ("empty", not "occupied") => no rows
	// affected, but this must NOT be an error (best-effort semantics).
	var applied bool
	err := sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		var err error
		applied, err = tableRepo.UpdateStatusIfCurrent(ctx, tx, tbl.ID, domain.TableStatusCleaning, domain.TableStatusOccupied)
		return err
	})
	require.NoError(t, err)
	assert.False(t, applied)
}

func TestTableRepo_TableRLSIsolation(t *testing.T) {
	ctx := context.Background()
	tableRepo := repo.NewTableRepo()
	z := newTestZone(t, ctx, tenantA, branchA)
	tbl := newTestTable(t, ctx, tenantA, branchA, z.ID)

	err := sharedPool.WithTenantReadTx(ctx, tenantB, func(tx pgx.Tx) error {
		_, err := tableRepo.GetTableByID(ctx, tx, tbl.ID)
		return err
	})
	assert.ErrorIs(t, err, repo.ErrNotFound, "tenantB must not see tenantA's table")
}

func TestTableRepo_ListTablesByBranch_ReportsActiveCheckID(t *testing.T) {
	ctx := context.Background()
	tableRepo := repo.NewTableRepo()
	checkRepo := repo.NewCheckRepo()
	z := newTestZone(t, ctx, tenantA, branchA)
	tbl := newTestTable(t, ctx, tenantA, branchA, z.ID)

	entries, err := listTables(ctx, tableRepo, tenantA, branchA)
	require.NoError(t, err)
	found := findTableEntry(entries, tbl.ID)
	require.NotNil(t, found)
	assert.Nil(t, found.ActiveCheckID)

	var openCheck domain.Check
	err = sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		var err error
		openCheck, err = checkRepo.Create(ctx, tx, domain.Check{
			TenantID: tenantA, BranchID: branchA, TableID: &tbl.ID, TableLabel: tbl.Name,
			Status: domain.CheckStatusOpen, OpenedBy: staffA,
		})
		return err
	})
	require.NoError(t, err)

	entries, err = listTables(ctx, tableRepo, tenantA, branchA)
	require.NoError(t, err)
	found = findTableEntry(entries, tbl.ID)
	require.NotNil(t, found)
	require.NotNil(t, found.ActiveCheckID)
	assert.Equal(t, openCheck.ID, *found.ActiveCheckID)
}

func listTables(ctx context.Context, tableRepo *repo.TableRepo, tenantID, branchID uuid.UUID) ([]repo.TableWithCheck, error) {
	var entries []repo.TableWithCheck
	err := sharedPool.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		entries, err = tableRepo.ListTablesByBranch(ctx, tx, branchID)
		return err
	})
	return entries, err
}

func findTableEntry(entries []repo.TableWithCheck, tableID uuid.UUID) *repo.TableWithCheck {
	for i := range entries {
		if entries[i].Table.ID == tableID {
			return &entries[i]
		}
	}
	return nil
}
