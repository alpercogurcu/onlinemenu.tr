// Package e2e contains cross-module integration tests that verify the full
// POS sale flow end-to-end: open check → place order → register payment →
// verify total paid → close check.
package e2e_test

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
	"go.uber.org/zap"

	paymentdomain "onlinemenu.tr/internal/modules/payment/domain"
	paymentpub "onlinemenu.tr/internal/modules/payment/public"
	paymentrepo "onlinemenu.tr/internal/modules/payment/repo"
	paymentsvc "onlinemenu.tr/internal/modules/payment/service"
	posdomain "onlinemenu.tr/internal/modules/pos/domain"
	posrepo "onlinemenu.tr/internal/modules/pos/repo"
	possvc "onlinemenu.tr/internal/modules/pos/service"
	"onlinemenu.tr/internal/platform/auth"
	"onlinemenu.tr/internal/platform/db"
)

var sharedPool *db.Pool

var (
	tenantID = uuid.MustParse("aaaaaaaa-0000-0000-0000-000000000001")
	branchID = uuid.MustParse("bbbbbbbb-0000-0000-0000-000000000001")
	staffID  = uuid.MustParse("cccccccc-0000-0000-0000-000000000001")
	prodID   = uuid.MustParse("dddddddd-0000-0000-0000-000000000001")
)

// staffPrincipal is a branch-scoped staff principal for branchID, used to
// satisfy the pos module's ADR-AUTH-001 layer 3 branch-scope checks
// (docs/lessons-from-b2b.md item 6) in this cross-module spine test.
func staffPrincipal() auth.Principal {
	return auth.Principal{
		PersonID: staffID,
		Ctx:      auth.ContextStaff,
		TenantID: tenantID,
		BranchID: branchID,
		RoleIDs:  []uuid.UUID{uuid.New()},
	}
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
	// file = .../backend/internal/e2e/spine_test.go
	// walk up 2 directories: e2e/ → internal/ → backend/
	base := filepath.Dir(file)
	for range 2 {
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

	for _, mod := range []string{"tenant", "identity", "pos", "payment"} {
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

// buildServices constructs POS and payment services without fx.
func buildServices() (*possvc.CheckService, *possvc.OrderService, *paymentsvc.PaymentService) {
	log := zap.NewNop()

	payRepo := paymentrepo.NewPaymentRepo()
	payService := paymentsvc.NewPaymentService(paymentsvc.Params{
		DB:          sharedPool,
		PaymentRepo: payRepo,
		Fiscal:      paymentdomain.MockFiscalAdapter{},
		Logger:      log,
	})

	reader := &saleReaderAdapter{svc: payService}

	checkRepo := posrepo.NewCheckRepo()
	orderRepo := posrepo.NewOrderRepo()

	checkService := possvc.NewCheckService(possvc.CheckParams{
		DB:         sharedPool,
		CheckRepo:  checkRepo,
		SaleReader: reader,
		Logger:     log,
	})
	orderService := possvc.NewOrderService(possvc.OrderParams{
		DB:        sharedPool,
		OrderRepo: orderRepo,
		Logger:    log,
	})

	return checkService, orderService, payService
}

// saleReaderAdapter bridges PaymentService → paymentpub.SaleReader.
type saleReaderAdapter struct{ svc *paymentsvc.PaymentService }

func (a *saleReaderAdapter) TotalPaidForCheck(ctx context.Context, tenantID, checkID uuid.UUID) (int64, error) {
	return a.svc.TotalPaidForCheck(ctx, tenantID, checkID)
}

func (a *saleReaderAdapter) PendingTotalForCheck(ctx context.Context, tenantID, checkID uuid.UUID) (int64, error) {
	return a.svc.PendingTotalForCheck(ctx, tenantID, checkID)
}

var _ paymentpub.SaleReader = (*saleReaderAdapter)(nil)

// drainFiscal runs the submission worker until it has nothing left to claim.
// RegisterSale only enqueues the fiscal submission (ADR-FISCAL-002), so the
// spine must drive the worker explicitly to reach a settled payment. RunOnce
// is used instead of Run because TestMain asserts goleak: the polling Run
// would strand a goroutine past the test.
func drainFiscal(t *testing.T, paySvc *paymentsvc.PaymentService) {
	t.Helper()
	w := paymentsvc.NewSubmissionWorker(paymentsvc.SubmissionWorkerParams{
		DB:             sharedPool,
		SubmissionRepo: paymentrepo.NewFiscalSubmissionRepo(),
		Adapter:        paymentdomain.MockFiscalAdapter{},
		Sink:           paySvc,
		Logger:         zap.NewNop(),
		Config:         paymentsvc.SubmissionWorkerConfig{StaleClaimAfter: time.Minute},
	})
	for range 10 {
		n, err := w.RunOnce(context.Background())
		require.NoError(t, err)
		if n == 0 {
			return
		}
	}
	t.Fatal("submission worker did not drain within 10 cycles")
}

// ---------------------------------------------------------------------------
// Spine test: open check → place order → register payment → close check
// ---------------------------------------------------------------------------

func TestPOSSpine_OpenOrderPayClose(t *testing.T) {
	ctx := context.Background()
	checkSvc, orderSvc, paySvc := buildServices()

	// 1. Open a check.
	check, err := checkSvc.Open(ctx, tenantID, staffPrincipal(), posdomain.Check{
		BranchID:   branchID,
		TableLabel: "T1",
		OpenedBy:   staffID,
	})
	require.NoError(t, err)
	assert.Equal(t, posdomain.CheckStatusOpen, check.Status)
	assert.Equal(t, "T1", check.TableLabel)

	// 2. Place an order linked to the check (2 items, 1500 + 500 kuruş = 2000 kuruş).
	order, err := orderSvc.Place(ctx, tenantID, staffPrincipal(), posdomain.Order{
		BranchID:     branchID,
		CheckID:      &check.ID,
		OrderChannel: posdomain.OrderChannelDineIn,
		Items: []posdomain.OrderItem{
			{
				ProductID:          prodID,
				ProductName:        "Adana Kebap",
				ProductPriceAmount: 1500,
				ProductCurrency:    "TRY",
				TaxRateBPS:         800,
				Quantity:           1,
				UnitPriceAmount:    1500,
			},
			{
				ProductID:          uuid.New(),
				ProductName:        "Ayran",
				ProductPriceAmount: 500,
				ProductCurrency:    "TRY",
				TaxRateBPS:         800,
				Quantity:           1,
				UnitPriceAmount:    500,
			},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, posdomain.OrderStatusPending, order.Status)
	assert.Len(t, order.Items, 2)

	// 3. Register a payment that covers the full check total (2000 kuruş).
	payment, err := paySvc.RegisterSale(ctx, paymentsvc.RegisterSaleRequest{
		TenantID:       tenantID,
		BranchID:       branchID,
		CheckID:        &check.ID,
		IdempotencyKey: "spine-test-pay-001",
		Method:         paymentdomain.PaymentMethodCash,
		AmountTotal:    2000,
		Currency:       "TRY",
	})
	require.NoError(t, err)
	assert.Equal(t, paymentdomain.PaymentStatusPending, payment.Status, "RegisterSale only enqueues the fiscal submission (ADR-FISCAL-002)")
	assert.Nil(t, payment.FiscalReceiptID)

	// 4. Drive the submission worker; only then does the payment settle.
	drainFiscal(t, paySvc)

	settled, err := paySvc.GetByID(ctx, tenantID, payment.ID)
	require.NoError(t, err)
	assert.Equal(t, paymentdomain.PaymentStatusCompleted, settled.Status)
	assert.NotNil(t, settled.FiscalReceiptID)

	// 5. Verify TotalPaidForCheck reflects the completed payment.
	total, err := paySvc.TotalPaidForCheck(ctx, tenantID, check.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(2000), total)

	// 6. Close the check — payment total covers the order total, should succeed.
	closed, err := checkSvc.Close(ctx, tenantID, staffPrincipal(), check.ID, staffID)
	require.NoError(t, err)
	assert.Equal(t, posdomain.CheckStatusClosed, closed.Status)
	assert.NotNil(t, closed.ClosedBy)
	assert.Equal(t, staffID, *closed.ClosedBy)
}

func TestPOSSpine_CloseWithInsufficientPayment(t *testing.T) {
	ctx := context.Background()
	checkSvc, orderSvc, paySvc := buildServices()

	// Open a check.
	check, err := checkSvc.Open(ctx, tenantID, staffPrincipal(), posdomain.Check{
		BranchID:   branchID,
		TableLabel: "T2",
		OpenedBy:   staffID,
	})
	require.NoError(t, err)

	// Place an order totalling 3000 kuruş.
	_, err = orderSvc.Place(ctx, tenantID, staffPrincipal(), posdomain.Order{
		BranchID:     branchID,
		CheckID:      &check.ID,
		OrderChannel: posdomain.OrderChannelDineIn,
		Items: []posdomain.OrderItem{
			{
				ProductID:       prodID,
				ProductName:     "Lahmacun",
				Quantity:        1,
				UnitPriceAmount: 3000,
				TaxRateBPS:      800,
			},
		},
	})
	require.NoError(t, err)

	// Register only a partial payment (1000 of 3000).
	_, err = paySvc.RegisterSale(ctx, paymentsvc.RegisterSaleRequest{
		TenantID:       tenantID,
		BranchID:       branchID,
		CheckID:        &check.ID,
		IdempotencyKey: "spine-test-partial-pay-001",
		Method:         paymentdomain.PaymentMethodCash,
		AmountTotal:    1000,
		Currency:       "TRY",
	})
	require.NoError(t, err)

	// Deliberately not drained: 1000 < 3000 is a genuine shortfall whether or
	// not the payment settles, so the shortfall must surface without the
	// worker. The submission left pending here is inert for later tests.
	//
	// Close must fail with ErrInsufficientPayment.
	_, err = checkSvc.Close(ctx, tenantID, staffPrincipal(), check.ID, staffID)
	require.ErrorIs(t, err, possvc.ErrInsufficientPayment)
}

// TestPOSSpine_ClosePaysOnlyForActiveOrders is the regression test for the
// money bug: a rejected order's items must not count toward the check total
// used to gate CloseCheck. Two orders are placed (1500 kuruş and 3000
// kuruş); the second is rejected. Paying only the first order's amount
// (1500) must be enough to close the check — with the pre-fix query (which
// summed ALL orders regardless of status), this same payment would leave
// the check underpaid (1500 < 4500) and Close would fail.
func TestPOSSpine_ClosePaysOnlyForActiveOrders(t *testing.T) {
	ctx := context.Background()
	checkSvc, orderSvc, paySvc := buildServices()

	check, err := checkSvc.Open(ctx, tenantID, staffPrincipal(), posdomain.Check{
		BranchID:   branchID,
		TableLabel: "T4",
		OpenedBy:   staffID,
	})
	require.NoError(t, err)

	// Order 1: stays active (pending), 1500 kuruş.
	_, err = orderSvc.Place(ctx, tenantID, staffPrincipal(), posdomain.Order{
		BranchID:     branchID,
		CheckID:      &check.ID,
		OrderChannel: posdomain.OrderChannelDineIn,
		Items: []posdomain.OrderItem{
			{
				ProductID:       prodID,
				ProductName:     "Ayran",
				Quantity:        1,
				UnitPriceAmount: 1500,
				TaxRateBPS:      800,
			},
		},
	})
	require.NoError(t, err)

	// Order 2: will be rejected, 3000 kuruş.
	rejectedOrder, err := orderSvc.Place(ctx, tenantID, staffPrincipal(), posdomain.Order{
		BranchID:     branchID,
		CheckID:      &check.ID,
		OrderChannel: posdomain.OrderChannelDineIn,
		Items: []posdomain.OrderItem{
			{
				ProductID:       uuid.New(),
				ProductName:     "Künefe",
				Quantity:        1,
				UnitPriceAmount: 3000,
				TaxRateBPS:      800,
			},
		},
	})
	require.NoError(t, err)

	_, err = orderSvc.Reject(ctx, tenantID, staffPrincipal(), rejectedOrder.ID, staffID, "mutfakta stok yok")
	require.NoError(t, err)

	// Pay only the active order's amount (1500), not the rejected order's.
	_, err = paySvc.RegisterSale(ctx, paymentsvc.RegisterSaleRequest{
		TenantID:       tenantID,
		BranchID:       branchID,
		CheckID:        &check.ID,
		IdempotencyKey: "spine-test-active-only-pay-001",
		Method:         paymentdomain.PaymentMethodCash,
		AmountTotal:    1500,
		Currency:       "TRY",
	})
	require.NoError(t, err)

	drainFiscal(t, paySvc)

	// Close must succeed: the rejected order's 3000 kuruş must not be part
	// of the check total gating this close.
	closed, err := checkSvc.Close(ctx, tenantID, staffPrincipal(), check.ID, staffID)
	require.NoError(t, err, "close must succeed once the only active order is paid in full, regardless of the rejected order")
	assert.Equal(t, posdomain.CheckStatusClosed, closed.Status)
}

// TestPOSSpine_CloseBlockedWhileFiscalPending pins the distinction introduced
// with ADR-FISCAL-002: a check fully covered by a payment still awaiting fiscal
// registration is transiently un-closeable (ErrFiscalPending), not underpaid
// (ErrInsufficientPayment). The same Close succeeds once the worker settles it.
func TestPOSSpine_CloseBlockedWhileFiscalPending(t *testing.T) {
	ctx := context.Background()
	checkSvc, orderSvc, paySvc := buildServices()

	check, err := checkSvc.Open(ctx, tenantID, staffPrincipal(), posdomain.Check{
		BranchID:   branchID,
		TableLabel: "T5",
		OpenedBy:   staffID,
	})
	require.NoError(t, err)

	_, err = orderSvc.Place(ctx, tenantID, staffPrincipal(), posdomain.Order{
		BranchID:     branchID,
		CheckID:      &check.ID,
		OrderChannel: posdomain.OrderChannelDineIn,
		Items: []posdomain.OrderItem{
			{
				ProductID:       prodID,
				ProductName:     "İskender",
				Quantity:        1,
				UnitPriceAmount: 2500,
				TaxRateBPS:      800,
			},
		},
	})
	require.NoError(t, err)

	_, err = paySvc.RegisterSale(ctx, paymentsvc.RegisterSaleRequest{
		TenantID:       tenantID,
		BranchID:       branchID,
		CheckID:        &check.ID,
		IdempotencyKey: "spine-fiscal-pending-pay-001",
		Method:         paymentdomain.PaymentMethodCash,
		AmountTotal:    2500,
		Currency:       "TRY",
	})
	require.NoError(t, err)

	_, err = checkSvc.Close(ctx, tenantID, staffPrincipal(), check.ID, staffID)
	require.ErrorIs(t, err, possvc.ErrFiscalPending, "fully covered but unsettled payment must read as pending, not underpaid")
	require.NotErrorIs(t, err, possvc.ErrInsufficientPayment)

	drainFiscal(t, paySvc)

	closed, err := checkSvc.Close(ctx, tenantID, staffPrincipal(), check.ID, staffID)
	require.NoError(t, err)
	assert.Equal(t, posdomain.CheckStatusClosed, closed.Status)
}

func TestPOSSpine_IdempotentPayment(t *testing.T) {
	ctx := context.Background()
	checkSvc, _, paySvc := buildServices()

	check, err := checkSvc.Open(ctx, tenantID, staffPrincipal(), posdomain.Check{
		BranchID:   branchID,
		TableLabel: "T3",
		OpenedBy:   staffID,
	})
	require.NoError(t, err)

	req := paymentsvc.RegisterSaleRequest{
		TenantID:       tenantID,
		BranchID:       branchID,
		CheckID:        &check.ID,
		IdempotencyKey: "spine-idempotent-pay-001",
		Method:         paymentdomain.PaymentMethodTerminal,
		AmountTotal:    5000,
		Currency:       "TRY",
	}

	// First call creates the payment.
	first, err := paySvc.RegisterSale(ctx, req)
	require.NoError(t, err)

	// Second call with the same key returns the same payment, no duplicate.
	second, err := paySvc.RegisterSale(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, first.ID, second.ID, "idempotent: same payment ID on retry")

	drainFiscal(t, paySvc)

	// Total should still be 5000 (not doubled): the retry must not have
	// enqueued a second fiscal submission.
	total, err := paySvc.TotalPaidForCheck(ctx, tenantID, check.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(5000), total)
}
