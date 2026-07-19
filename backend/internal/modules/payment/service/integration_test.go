package service_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
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

	"onlinemenu.tr/internal/modules/payment/domain"
	"onlinemenu.tr/internal/modules/payment/repo"
	"onlinemenu.tr/internal/modules/payment/service"
	"onlinemenu.tr/internal/platform/db"
)

var (
	sharedPool *db.Pool
	tenantA    = uuid.MustParse("aaaaaaaa-0000-0000-0000-000000000001")
	branchA    = uuid.MustParse("cccccccc-0000-0000-0000-000000000001")
)

func TestMain(m *testing.M) {
	ctx := context.Background()

	ctr, err := startPostgres(ctx)
	if err != nil {
		// No container runtime (typical local dev without Docker): leave
		// sharedPool nil so requireDB skips the DB-backed tests, and still run
		// the pure unit tests that share this binary.
		fmt.Fprintf(os.Stderr, "postgres container unavailable, skipping DB-backed tests: %v\n", err)
		os.Exit(m.Run())
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

// startPostgres converts testcontainers' missing-runtime panic into an error.
// MustExtractDockerHost panics (rather than returning) when no Docker daemon is
// reachable, which would take the whole test binary down with it.
func startPostgres(ctx context.Context) (ctr *tcpostgres.PostgresContainer, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("container runtime unavailable: %v", r)
		}
	}()
	return tcpostgres.Run(ctx,
		"pgvector/pgvector:pg17",
		tcpostgres.WithDatabase("testdb"),
		tcpostgres.WithUsername("postgres"),
		tcpostgres.WithPassword("postgres"),
		tcpostgres.BasicWaitStrategies(),
	)
}

// requireDB skips a test when no Postgres container could be started.
func requireDB(t *testing.T) {
	t.Helper()
	if sharedPool == nil {
		t.Skip("postgres container unavailable (is Docker running?)")
	}
}

// migrationsBase returns the absolute path to backend/migrations.
// File: .../backend/internal/modules/payment/service/integration_test.go
// Walk up 4: service→payment→modules→internal→backend
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

	for _, mod := range []string{"tenant", "identity", "catalog", "pos", "payment"} {
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
	cfg.MaxConns = 10

	p, err := db.NewPoolFromConfig(ctx, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create pool (%s): %v\n", user, err)
		os.Exit(1)
	}
	return p
}

func newPaymentService() *service.PaymentService {
	return service.NewPaymentService(service.Params{
		DB:             sharedPool,
		PaymentRepo:    repo.NewPaymentRepo(),
		SubmissionRepo: repo.NewFiscalSubmissionRepo(),
		StatusRepo:     repo.NewFiscalStatusRepo(),
		Fiscal:         domain.MockFiscalAdapter{},
		Logger:         zap.NewNop(),
	})
}

func newSubmissionWorker(sink domain.FiscalResultSink) *service.SubmissionWorker {
	return service.NewSubmissionWorker(service.SubmissionWorkerParams{
		DB:             sharedPool,
		SubmissionRepo: repo.NewFiscalSubmissionRepo(),
		Adapter:        domain.MockFiscalAdapter{},
		Sink:           sink,
		Logger:         zap.NewNop(),
		Config:         service.SubmissionWorkerConfig{StaleClaimAfter: time.Minute},
	})
}

// drainFiscal runs the submission worker until it has nothing left to claim.
// RegisterSale now only enqueues the fiscal submission (ADR-FISCAL-002), so
// tests that need a settled payment must drive the worker explicitly.
func drainFiscal(t *testing.T, svc *service.PaymentService) {
	t.Helper()
	w := newSubmissionWorker(svc)
	for range 10 {
		n, err := w.RunOnce(context.Background())
		require.NoError(t, err)
		if n == 0 {
			// RunOnce returning 0 means "nothing claimable", which covers both
			// "all settled" and "rows stranded in a state the worker cannot
			// claim" (still pending but backing off, or claimed by a crashed
			// run). Those look identical here, so assert the drain actually
			// emptied the queue rather than trusting the zero.
			assertNoClaimableSubmissions(t)
			return
		}
	}
	t.Fatal("submission worker did not drain within 10 cycles")
}

// assertNoClaimableSubmissions fails with the surviving count so a test that
// silently stopped settling reports "3 stranded" instead of passing on a queue
// it never actually drained.
//
// Eligibility mirrors ClaimPending's predicate rather than asking "is anything
// pending": the package shares one database, and sibling tests deliberately
// park rows the worker cannot claim (backing off via next_retry_at, or held by
// a simulated crashed claim). Those are fixtures, not stranded work. A row the
// worker WOULD claim surviving a run that reported "no work" is the real
// contradiction, and the only one worth failing on.
func assertNoClaimableSubmissions(t *testing.T) {
	t.Helper()
	var stranded int
	err := sharedPool.WithTenantReadTx(context.Background(), tenantA, func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), `
			SELECT count(*)
			FROM fiscal_submissions
			WHERE status = 'pending'
			  AND (next_retry_at IS NULL OR next_retry_at <= NOW())
			  AND claimed_at IS NULL
		`).Scan(&stranded)
	})
	require.NoError(t, err)
	if stranded > 0 {
		t.Fatalf("submission worker reported no work but %d claimable submission(s) are still pending", stranded)
	}
}

func fetchPayment(t *testing.T, svc *service.PaymentService, id uuid.UUID) domain.Payment {
	t.Helper()
	p, err := svc.GetByID(context.Background(), tenantA, id)
	require.NoError(t, err)
	return p
}

func countOutboxEvents(t *testing.T, tenantID uuid.UUID, paymentID uuid.UUID, eventType string) int {
	t.Helper()
	var n int
	err := sharedPool.WithTenantReadTx(context.Background(), tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), `
			SELECT COUNT(*) FROM payment_outbox
			WHERE aggregate_id = $1 AND event_type = $2
		`, paymentID.String(), eventType).Scan(&n)
	})
	require.NoError(t, err)
	return n
}

func submissionStatus(t *testing.T, tenantID, paymentID uuid.UUID) string {
	t.Helper()
	var status string
	err := sharedPool.WithTenantReadTx(context.Background(), tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), `
			SELECT status FROM fiscal_submissions WHERE payment_id = $1
		`, paymentID).Scan(&status)
	})
	require.NoError(t, err)
	return status
}

func submissionID(t *testing.T, tenantID, paymentID uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := sharedPool.WithTenantReadTx(context.Background(), tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), `
			SELECT id FROM fiscal_submissions WHERE payment_id = $1
		`, paymentID).Scan(&id)
	})
	require.NoError(t, err)
	return id
}

// ---------------------------------------------------------------------------
// ListByCheck (double-payment guard)
// ---------------------------------------------------------------------------

// TestListByCheck_ReturnsOnlyCompletedForCheck exercises the service surface
// POS uses to show already-recorded payments when a cashier reopens a check.
// A pending payment (fiscal registration still in flight) must stay invisible;
// only once the worker settles it does it appear, and a completed payment on a
// different check must never bleed in.
func TestListByCheck_ReturnsOnlyCompletedForCheck(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	svc := newPaymentService()

	checkID := uuid.New()
	otherCheckID := uuid.New()

	paid, err := svc.RegisterSale(ctx, service.RegisterSaleRequest{
		TenantID:       tenantA,
		BranchID:       branchA,
		CheckID:        &checkID,
		IdempotencyKey: uuid.New().String(),
		Method:         domain.PaymentMethodCash,
		AmountTotal:    4200,
		Currency:       "TRY",
	})
	require.NoError(t, err)
	require.Equal(t, domain.PaymentStatusPending, paid.Status,
		"fiscal registration is asynchronous; RegisterSale must not complete the payment")

	pending, err := svc.ListByCheck(ctx, tenantA, checkID)
	require.NoError(t, err)
	assert.Empty(t, pending, "a payment awaiting fiscal registration must not show as recorded")

	_, err = svc.RegisterSale(ctx, service.RegisterSaleRequest{
		TenantID:       tenantA,
		BranchID:       branchA,
		CheckID:        &otherCheckID,
		IdempotencyKey: uuid.New().String(),
		Method:         domain.PaymentMethodCash,
		AmountTotal:    9900,
		Currency:       "TRY",
	})
	require.NoError(t, err)

	drainFiscal(t, svc)

	payments, err := svc.ListByCheck(ctx, tenantA, checkID)
	require.NoError(t, err)
	require.Len(t, payments, 1, "must only return payments for the requested check")
	assert.Equal(t, paid.ID, payments[0].ID)
	assert.Equal(t, int64(4200), payments[0].AmountTotal)
	assert.NotNil(t, payments[0].FiscalReceiptID, "a completed payment must link its fiscal receipt")
}

// ---------------------------------------------------------------------------
// Asynchronous fiscal registration (ADR-FISCAL-002)
// ---------------------------------------------------------------------------

// TestRegisterSale_EnqueuesSubmission_WorkerCompletesPayment walks the whole
// two-phase flow with the synchronous mock adapter: RegisterSale leaves the
// payment pending with a pending submission, and one worker cycle drives it to
// completed with a receipt and exactly one payment.completed outbox event.
func TestRegisterSale_EnqueuesSubmission_WorkerCompletesPayment(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	svc := newPaymentService()

	payment, err := svc.RegisterSale(ctx, service.RegisterSaleRequest{
		TenantID:       tenantA,
		BranchID:       branchA,
		IdempotencyKey: uuid.New().String(),
		Method:         domain.PaymentMethodMealCard,
		AmountTotal:    7500,
		Currency:       "TRY",
		Meta:           domain.FiscalMeta{TableLabel: "Masa 5", WaiterName: "Ayse", CheckNumber: 12},
	})
	require.NoError(t, err)
	require.Equal(t, domain.PaymentStatusPending, payment.Status)
	assert.Nil(t, payment.FiscalReceiptID)
	assert.Equal(t, string(domain.FiscalSubmissionPending), submissionStatus(t, tenantA, payment.ID))
	assert.Zero(t, countOutboxEvents(t, tenantA, payment.ID, "payment.completed"),
		"no completion event may be published before the device confirms")

	w := newSubmissionWorker(svc)
	n, err := w.RunOnce(ctx)
	require.NoError(t, err)
	require.GreaterOrEqual(t, n, 1,
		"the worker claimed nothing. The claim runs under WithAllTenantsTx "+
			"(app.tenant_scope='all_tenants'), so fiscal_submissions needs an "+
			"all-tenants RLS policy alongside its tenant-isolation one:\n"+
			"  CREATE POLICY fiscal_submissions_all_tenants ON fiscal_submissions\n"+
			"      USING (current_setting('app.tenant_scope', TRUE) = 'all_tenants');\n"+
			"It must be FOR ALL (no FOR clause), not SELECT-only: the claim is an UPDATE ... RETURNING.")

	settled := fetchPayment(t, svc, payment.ID)
	assert.Equal(t, domain.PaymentStatusCompleted, settled.Status)
	require.NotNil(t, settled.FiscalReceiptID)
	assert.Equal(t, string(domain.FiscalSubmissionCompleted), submissionStatus(t, tenantA, payment.ID))
	assert.Equal(t, 1, countOutboxEvents(t, tenantA, payment.ID, "payment.completed"))
}

// TestWorker_SecondCycleIsANoOp proves the claim query only picks up pending
// rows: re-running the worker must not re-submit a settled sale, which would
// print a duplicate fiscal receipt on a real device.
func TestWorker_SecondCycleIsANoOp(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	svc := newPaymentService()

	payment, err := svc.RegisterSale(ctx, service.RegisterSaleRequest{
		TenantID:       tenantA,
		BranchID:       branchA,
		IdempotencyKey: uuid.New().String(),
		Method:         domain.PaymentMethodCash,
		AmountTotal:    1000,
	})
	require.NoError(t, err)

	w := newSubmissionWorker(svc)
	_, err = w.RunOnce(ctx)
	require.NoError(t, err)

	n, err := w.RunOnce(ctx)
	require.NoError(t, err)
	assert.Zero(t, n, "a completed submission must never be claimed again")
	assert.Equal(t, 1, countOutboxEvents(t, tenantA, payment.ID, "payment.completed"))
}

// TestOnFiscalResult_DuplicateDelivery_HasNoSideEffects is the core idempotency
// guarantee: vendors retry webhooks and the reconciliation sweep replays open
// baskets, so the same result may arrive many times. Only the first delivery may
// insert a receipt, complete the payment, and publish the outbox event.
func TestOnFiscalResult_DuplicateDelivery_HasNoSideEffects(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	svc := newPaymentService()

	payment, err := svc.RegisterSale(ctx, service.RegisterSaleRequest{
		TenantID:       tenantA,
		BranchID:       branchA,
		IdempotencyKey: uuid.New().String(),
		Method:         domain.PaymentMethodTerminal,
		AmountTotal:    3300,
	})
	require.NoError(t, err)

	res := domain.FiscalResult{
		SubmissionID: submissionID(t, tenantA, payment.ID),
		TenantID:     tenantA,
		BranchID:     branchA,
		PaymentID:    payment.ID,
		Status:       domain.FiscalSubmissionCompleted,
		DeviceType:   "beko_x30tr_cloud",
		ReceiptNo:    "0001",
		ZNo:          "0042",
		VendorRef:    "b3f1e0c2-0000-0000-0000-000000000001",
		CompletedAt:  time.Now().UTC(),
	}

	require.NoError(t, svc.OnFiscalResult(ctx, res))
	for range 3 {
		require.NoError(t, svc.OnFiscalResult(ctx, res), "duplicate delivery must be a silent no-op")
	}

	settled := fetchPayment(t, svc, payment.ID)
	assert.Equal(t, domain.PaymentStatusCompleted, settled.Status)
	assert.Equal(t, 1, countOutboxEvents(t, tenantA, payment.ID, "payment.completed"),
		"exactly one completion event may be published")

	var receipts int
	err = sharedPool.WithTenantReadTx(ctx, tenantA, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT COUNT(*) FROM fiscal_receipts WHERE payment_id = $1`, payment.ID).Scan(&receipts)
	})
	require.NoError(t, err)
	assert.Equal(t, 1, receipts, "exactly one fiscal receipt may exist")
}

// TestOnFiscalResult_PersistsZNoAndVendorRefInReceiptData pins the storage
// contract: fiscal_receipts has no z_no/vendor_ref columns, so both live inside
// the receipt_data JSONB document.
func TestOnFiscalResult_PersistsZNoAndVendorRefInReceiptData(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	svc := newPaymentService()

	payment, err := svc.RegisterSale(ctx, service.RegisterSaleRequest{
		TenantID:       tenantA,
		BranchID:       branchA,
		IdempotencyKey: uuid.New().String(),
		Method:         domain.PaymentMethodCash,
		AmountTotal:    2500,
	})
	require.NoError(t, err)

	require.NoError(t, svc.OnFiscalResult(ctx, domain.FiscalResult{
		SubmissionID: submissionID(t, tenantA, payment.ID),
		TenantID:     tenantA,
		BranchID:     branchA,
		PaymentID:    payment.ID,
		Status:       domain.FiscalSubmissionCompleted,
		DeviceType:   "beko_x30tr_cloud",
		ReceiptNo:    "0007",
		ZNo:          "0099",
		VendorRef:    "vendor-tx-1",
		CompletedAt:  time.Now().UTC(),
	}))

	var zNo, vendorRef string
	err = sharedPool.WithTenantReadTx(ctx, tenantA, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT receipt_data->>'z_no', receipt_data->>'vendor_ref'
			FROM fiscal_receipts WHERE payment_id = $1
		`, payment.ID).Scan(&zNo, &vendorRef)
	})
	require.NoError(t, err)
	assert.Equal(t, "0099", zNo)
	assert.Equal(t, "vendor-tx-1", vendorRef)
}

// TestOnFiscalResult_Failed_FailsPaymentWithoutEvent asserts a rejected
// registration leaves no receipt and publishes no completion event.
func TestOnFiscalResult_Failed_FailsPaymentWithoutEvent(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	svc := newPaymentService()

	payment, err := svc.RegisterSale(ctx, service.RegisterSaleRequest{
		TenantID:       tenantA,
		BranchID:       branchA,
		IdempotencyKey: uuid.New().String(),
		Method:         domain.PaymentMethodCash,
		AmountTotal:    500,
	})
	require.NoError(t, err)

	require.NoError(t, svc.OnFiscalResult(ctx, domain.FiscalResult{
		SubmissionID:  submissionID(t, tenantA, payment.ID),
		TenantID:      tenantA,
		BranchID:      branchA,
		PaymentID:     payment.ID,
		Status:        domain.FiscalSubmissionFailed,
		FailureReason: "device rejected basket",
		CompletedAt:   time.Now().UTC(),
	}))

	settled := fetchPayment(t, svc, payment.ID)
	assert.Equal(t, domain.PaymentStatusFailed, settled.Status)
	assert.Nil(t, settled.FiscalReceiptID)
	assert.Zero(t, countOutboxEvents(t, tenantA, payment.ID, "payment.completed"))
	assert.Equal(t, string(domain.FiscalSubmissionFailed), submissionStatus(t, tenantA, payment.ID))
}

// TestVoidSale_VoidsCompletedPaymentAndPublishesEvent covers fiş iptali: a void
// may cancel an already-completed registration, and it is itself idempotent.
func TestVoidSale_VoidsCompletedPaymentAndPublishesEvent(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	svc := newPaymentService()

	payment, err := svc.RegisterSale(ctx, service.RegisterSaleRequest{
		TenantID:       tenantA,
		BranchID:       branchA,
		IdempotencyKey: uuid.New().String(),
		Method:         domain.PaymentMethodCash,
		AmountTotal:    6000,
	})
	require.NoError(t, err)
	drainFiscal(t, svc)
	require.Equal(t, domain.PaymentStatusCompleted, fetchPayment(t, svc, payment.ID).Status)

	require.NoError(t, svc.VoidSale(ctx, tenantA, payment.ID))

	voided := fetchPayment(t, svc, payment.ID)
	assert.Equal(t, domain.PaymentStatusVoided, voided.Status)
	assert.Equal(t, string(domain.FiscalSubmissionVoided), submissionStatus(t, tenantA, payment.ID))
	assert.Equal(t, 1, countOutboxEvents(t, tenantA, payment.ID, "payment.voided"))

	// A replayed void must not publish a second event.
	require.NoError(t, svc.VoidSale(ctx, tenantA, payment.ID))
	assert.Equal(t, 1, countOutboxEvents(t, tenantA, payment.ID, "payment.voided"))
}

// ---------------------------------------------------------------------------
// Idempotency race tests
// ---------------------------------------------------------------------------

func TestRegisterSale_SameKeySequential_ReturnsSamePayment(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	svc := newPaymentService()
	key := uuid.New().String()

	req := service.RegisterSaleRequest{
		TenantID:       tenantA,
		BranchID:       branchA,
		IdempotencyKey: key,
		Method:         domain.PaymentMethodCash,
		AmountTotal:    12345,
		Currency:       "TRY",
	}

	first, err := svc.RegisterSale(ctx, req)
	require.NoError(t, err)

	second, err := svc.RegisterSale(ctx, req)
	require.NoError(t, err)

	assert.Equal(t, first.ID, second.ID, "second call with the same key must return the original payment")
	assert.Equal(t, domain.PaymentStatusPending, second.Status)

	// The replay must not have enqueued a second fiscal submission — the partial
	// unique index on payment_id would reject it, but the idempotency fast path
	// short-circuits before we get there.
	var submissions int
	err = sharedPool.WithTenantReadTx(ctx, tenantA, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT COUNT(*) FROM fiscal_submissions WHERE payment_id = $1`, first.ID).Scan(&submissions)
	})
	require.NoError(t, err)
	assert.Equal(t, 1, submissions)

	drainFiscal(t, svc)
	assert.Equal(t, domain.PaymentStatusCompleted, fetchPayment(t, svc, first.ID).Status)
}

// TestRegisterSale_ConcurrentSameKey_NoDuplicateOrError exercises the race the
// HTTP idempotency middleware does not fully close at the service layer:
// two goroutines calling RegisterSale with the same key can both pass the
// in-transaction pre-check before either commits. The loser must recover via
// GetByIdempotencyKey in a fresh transaction instead of surfacing a 500.
func TestRegisterSale_ConcurrentSameKey_NoDuplicateOrError(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	svc := newPaymentService()
	key := uuid.New().String()

	const n = 8
	results := make([]domain.Payment, n)
	errs := make([]error, n)

	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = svc.RegisterSale(ctx, service.RegisterSaleRequest{
				TenantID:       tenantA,
				BranchID:       branchA,
				IdempotencyKey: key,
				Method:         domain.PaymentMethodTerminal,
				AmountTotal:    5000,
				Currency:       "TRY",
			})
		}(i)
	}
	wg.Wait()

	var firstID uuid.UUID
	for i, err := range errs {
		require.NoError(t, err, "call %d must not surface the unique-violation race as an error", i)
		if firstID == uuid.Nil {
			firstID = results[i].ID
		}
		assert.Equal(t, firstID, results[i].ID, "all concurrent calls with the same key must resolve to one payment")
	}

	total, err := svc.TotalPaidForCheck(ctx, tenantA, uuid.New())
	require.NoError(t, err)
	assert.Equal(t, int64(0), total, "sanity: unrelated check must remain unaffected")

	var count int
	err = sharedPool.WithTenantReadTx(ctx, tenantA, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT COUNT(*) FROM payments WHERE idempotency_key = $1`, key).Scan(&count)
	})
	require.NoError(t, err)
	assert.Equal(t, 1, count, "exactly one payment row must exist for the idempotency key")
}

// ---------------------------------------------------------------------------
// Reconciliation sweep (ADR-FISCAL-002 "Uzlaştırma")
// ---------------------------------------------------------------------------

func newReconciler(sink domain.FiscalResultSink, cfg service.ReconcilerConfig) *service.Reconciler {
	return service.NewReconciler(service.ReconcilerParams{
		DB:             sharedPool,
		SubmissionRepo: repo.NewFiscalSubmissionRepo(),
		Sink:           sink,
		Logger:         zap.NewNop(),
		Config:         cfg,
	})
}

// parkAsSubmitted forces a submission into the state it would reach after the
// adapter accepted the basket but the vendor's webhook never arrived.
func parkAsSubmitted(t *testing.T, tenantID, paymentID uuid.UUID, submittedAt time.Time) {
	t.Helper()
	err := sharedPool.WithTenantTx(context.Background(), tenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(context.Background(), `
			UPDATE fiscal_submissions
			SET status = 'submitted', submitted_at = $2
			WHERE payment_id = $1
		`, paymentID, submittedAt)
		return err
	})
	require.NoError(t, err)
}

func registerPending(t *testing.T, svc *service.PaymentService, amount int64) domain.Payment {
	t.Helper()
	p, err := svc.RegisterSale(context.Background(), service.RegisterSaleRequest{
		TenantID:       tenantA,
		BranchID:       branchA,
		IdempotencyKey: uuid.New().String(),
		Method:         domain.PaymentMethodCash,
		AmountTotal:    amount,
	})
	require.NoError(t, err)
	return p
}

// TestReconciler_OverdueSubmission_WarnsButDoesNotFailPayment is the money-safety
// test: a lost webhook must never fail a payment the device may have registered.
// Within the vendor's basket TTL the sweep only reports; it writes nothing.
func TestReconciler_OverdueSubmission_WarnsButDoesNotFailPayment(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	svc := newPaymentService()

	payment := registerPending(t, svc, 4500)
	parkAsSubmitted(t, tenantA, payment.ID, time.Now().UTC().Add(-2*time.Hour))

	r := newReconciler(svc, service.ReconcilerConfig{
		StaleAfter:  time.Second,
		ExpireAfter: 14 * 24 * time.Hour,
	})
	stats, err := r.RunOnce(ctx)
	require.NoError(t, err)
	require.GreaterOrEqual(t, stats.Scanned, 1,
		"the sweep scanned nothing; ListStaleSubmitted runs under WithAllTenantsReadTx "+
			"and needs the fiscal_submissions all-tenants RLS policy (see the submission worker test)")
	assert.GreaterOrEqual(t, stats.Warned, 1)
	assert.Zero(t, stats.Expired)

	unchanged := fetchPayment(t, svc, payment.ID)
	assert.Equal(t, domain.PaymentStatusPending, unchanged.Status,
		"a late fiscal result must not fail the payment: the receipt may well have printed")
	assert.Equal(t, string(domain.FiscalSubmissionSubmitted), submissionStatus(t, tenantA, payment.ID))
}

// TestReconciler_PastVendorTTL_ExpiresSubmissionAndFailsPayment covers the only
// branch allowed to write: once the vendor's basket lifetime has elapsed the
// basket cannot exist any more, so the sale can never complete.
func TestReconciler_PastVendorTTL_ExpiresSubmissionAndFailsPayment(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	svc := newPaymentService()

	payment := registerPending(t, svc, 6100)
	parkAsSubmitted(t, tenantA, payment.ID, time.Now().UTC().Add(-30*24*time.Hour))

	r := newReconciler(svc, service.ReconcilerConfig{
		StaleAfter:  time.Second,
		ExpireAfter: 14 * 24 * time.Hour,
		AutoExpire:  true,
	})
	stats, err := r.RunOnce(ctx)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, stats.Expired, 1)

	settled := fetchPayment(t, svc, payment.ID)
	assert.Equal(t, domain.PaymentStatusFailed, settled.Status)
	assert.Nil(t, settled.FiscalReceiptID)
	assert.Equal(t, string(domain.FiscalSubmissionExpired), submissionStatus(t, tenantA, payment.ID))
	assert.Zero(t, countOutboxEvents(t, tenantA, payment.ID, "payment.completed"))
}

// TestReconciler_SecondSweepIsANoOp: an expired submission leaves the 'submitted'
// set, so a replayed sweep neither rescans nor re-fails it.
func TestReconciler_SecondSweepIsANoOp(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	svc := newPaymentService()

	payment := registerPending(t, svc, 700)
	parkAsSubmitted(t, tenantA, payment.ID, time.Now().UTC().Add(-30*24*time.Hour))

	r := newReconciler(svc, service.ReconcilerConfig{
		StaleAfter:  time.Second,
		ExpireAfter: 14 * 24 * time.Hour,
		AutoExpire:  true,
	})
	_, err := r.RunOnce(ctx)
	require.NoError(t, err)

	before := fetchPayment(t, svc, payment.ID)
	_, err = r.RunOnce(ctx)
	require.NoError(t, err)

	after := fetchPayment(t, svc, payment.ID)
	assert.Equal(t, before.Status, after.Status)
	assert.Equal(t, string(domain.FiscalSubmissionExpired), submissionStatus(t, tenantA, payment.ID))
}

// TestReconciler_CompletedSubmissionIsNeverSwept guards the query filter: a sale
// that already finished must be invisible to the sweep no matter how old it is.
func TestReconciler_CompletedSubmissionIsNeverSwept(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	svc := newPaymentService()

	payment := registerPending(t, svc, 8800)
	drainFiscal(t, svc)
	require.Equal(t, domain.PaymentStatusCompleted, fetchPayment(t, svc, payment.ID).Status)

	// Backdate it far beyond any TTL; status='completed' must still exclude it.
	err := sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			UPDATE fiscal_submissions SET submitted_at = $2 WHERE payment_id = $1
		`, payment.ID, time.Now().UTC().Add(-365*24*time.Hour))
		return err
	})
	require.NoError(t, err)

	r := newReconciler(svc, service.ReconcilerConfig{
		StaleAfter:  time.Second,
		ExpireAfter: time.Hour,
		AutoExpire:  true,
	})
	_, err = r.RunOnce(ctx)
	require.NoError(t, err)

	assert.Equal(t, domain.PaymentStatusCompleted, fetchPayment(t, svc, payment.ID).Status)
	assert.Equal(t, string(domain.FiscalSubmissionCompleted), submissionStatus(t, tenantA, payment.ID))
}
