package service_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
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

	"onlinemenu.tr/internal/modules/inventory/domain"
	pub "onlinemenu.tr/internal/modules/inventory/public"
	"onlinemenu.tr/internal/modules/inventory/repo"
	"onlinemenu.tr/internal/modules/inventory/service"
	"onlinemenu.tr/internal/platform/auth"
	"onlinemenu.tr/internal/platform/db"
)

var (
	sharedPool *db.Pool
	tenantA    = uuid.MustParse("aaaaaaaa-0000-0000-0000-000000000001")
	tenantB    = uuid.MustParse("bbbbbbbb-0000-0000-0000-000000000001")
	branchReq  = uuid.MustParse("cccccccc-0000-0000-0000-000000000001") // requesting branch
	branchSrc  = uuid.MustParse("dddddddd-0000-0000-0000-000000000001") // source (depo) branch
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
// File: .../backend/internal/modules/inventory/service/integration_test.go
// Walk up 4: service→inventory→modules→internal→backend
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

	for _, mod := range []string{"tenant", "identity", "inventory"} {
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

// ---------------------------------------------------------------------------
// Test fixtures
// ---------------------------------------------------------------------------

func newWarehouseService() *service.WarehouseService {
	return service.NewWarehouseService(service.WarehouseParams{
		DB: sharedPool, Repo: repo.NewWarehouseRepo(), Logger: zap.NewNop(),
	})
}

func newStockItemService() *service.StockItemService {
	return service.NewStockItemService(service.StockItemParams{
		DB: sharedPool, Repo: repo.NewStockItemRepo(), SupplyPolicyRepo: repo.NewSupplyPolicyRepo(), Logger: zap.NewNop(),
	})
}

func newInventoryService() *service.InventoryService {
	return service.NewInventoryService(service.Params{
		DB: sharedPool, LvlRepo: repo.NewStockLevelRepo(), MvRepo: repo.NewStockMovementRepo(),
		WhRepo: repo.NewWarehouseRepo(), Logger: zap.NewNop(),
	})
}

func newTransferOrderService() *service.TransferOrderService {
	return service.NewTransferOrderService(service.TransferOrderParams{
		DB: sharedPool, Repo: repo.NewTransferOrderRepo(), ItemRepo: repo.NewTransferOrderItemRepo(), Logger: zap.NewNop(),
	})
}

func newShipmentService() *service.ShipmentService {
	return service.NewShipmentService(service.ShipmentParams{
		DB:           sharedPool,
		Repo:         repo.NewShipmentRepo(),
		ItemRepo:     repo.NewShipmentItemRepo(),
		LvlRepo:      repo.NewStockLevelRepo(),
		MvRepo:       repo.NewStockMovementRepo(),
		TransferRepo: repo.NewTransferOrderRepo(),
		TransferItem: repo.NewTransferOrderItemRepo(),
		WhRepo:       repo.NewWarehouseRepo(),
		Logger:       zap.NewNop(),
	})
}

func newPurchaseReceiptService() *service.PurchaseReceiptService {
	return service.NewPurchaseReceiptService(service.PurchaseReceiptParams{
		DB:       sharedPool,
		Repo:     repo.NewPurchaseReceiptRepo(),
		ItemRepo: repo.NewPurchaseReceiptItemRepo(),
		LvlRepo:  repo.NewStockLevelRepo(),
		MvRepo:   repo.NewStockMovementRepo(),
		WhRepo:   repo.NewWarehouseRepo(),
		Resolver: service.NewSupplyPolicyResolver(newSupplyPolicyService()),
		Logger:   zap.NewNop(),
	})
}

// chainWidePrincipal returns a manager staff principal with no single-branch
// scope (BranchID == uuid.Nil), matching a chain-wide membership
// (ADR-AUTH-001 / identity domain.Membership: "nil = chain-wide"). Used by
// lifecycle tests that exercise both requesting-branch and source-branch
// actions in one flow — a transfer order's Submit checks source_branch_id
// while Approve/Fulfil check the requesting branch, so no single branch_id
// can satisfy such a flow.
//
// It MUST be paired with chainWideCtx: since requireBranch was hardened to
// fail closed, a nil BranchID grants nothing on its own; chain-wide reach
// comes exclusively from the OPA-derived scope=="tenant" that the manager
// role resolves to. Branch-scoped authz enforcement itself is covered by the
// dedicated tests in branch_authz_test.go.
func chainWidePrincipal() auth.Principal {
	return auth.Principal{
		PersonID: uuid.New(),
		Ctx:      auth.ContextStaff,
		TenantID: tenantA,
		BranchID: uuid.Nil,
		RoleIDs:  []uuid.UUID{managerRoleID},
	}
}

var (
	scopeEngineOnce sync.Once
	scopeEngineVal  *auth.Engine
)

// chainWideCtx returns a context carrying the scope auth.RequirePermission
// resolves for a manager principal against the real OPA bundle — the same
// value production middleware plants before the service layer runs. Tests
// asserting a DENIAL must keep context.Background(): a tenant scope exempts
// every principal and would mask the very check under test.
func chainWideCtx(t *testing.T, parent context.Context) context.Context {
	t.Helper()
	scopeEngineOnce.Do(func() { scopeEngineVal = newScopeTestEngine(t) })

	// The scope is derived from ONE representative action because
	// configs/opa/bundles/authz.rego resolves scope at the ROLE level today
	// (`scope := "tenant" if has_role("manager")`, action-independent). If the
	// policy ever moves to per-action scope, this single-action derivation stops
	// representing the other call sites and must be revisited.
	var scoped context.Context
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		scoped = r.Context()
	})
	req := httptest.NewRequest(http.MethodPost, "/", nil).
		WithContext(auth.WithPrincipal(parent, chainWidePrincipal()))
	auth.RequirePermission(scopeEngineVal, "inventory.warehouse.update")(next).
		ServeHTTP(httptest.NewRecorder(), req)

	require.NotNil(t, scoped, "manager principal must pass OPA to yield a tenant-scoped context")
	scope, ok := auth.ScopeFromContext(scoped)
	require.True(t, ok)
	require.Equal(t, "tenant", scope)
	return scoped
}

func createTestWarehouse(t *testing.T, ctx context.Context, tenantID, branchID uuid.UUID) domain.Warehouse {
	t.Helper()
	svc := newWarehouseService()
	wh, err := svc.Create(ctx, tenantID, service.CreateWarehouseRequest{
		BranchID:      branchID,
		Name:          "Depo " + uuid.NewString(),
		WarehouseType: domain.WarehouseTypeDepo,
	})
	require.NoError(t, err)
	return wh
}

func createTestStockItem(t *testing.T, ctx context.Context, tenantID uuid.UUID) domain.StockItem {
	t.Helper()
	svc := newStockItemService()
	item, err := svc.Create(ctx, tenantID, service.CreateStockItemRequest{
		SKU:           "SKU-" + uuid.NewString(),
		Name:          "Un",
		Kind:          domain.StockItemKindRaw,
		CanonicalUnit: "kg",
	})
	require.NoError(t, err)
	return item
}

// seedSourceStock puts `qty` of stockItem into warehouseID via a direct 'in'
// movement (simulating a prior restock), using InventoryService — the same
// atomic path production code uses.
func seedSourceStock(t *testing.T, ctx context.Context, tenantID, warehouseID, stockItemID uuid.UUID, qty float64, unit string) {
	t.Helper()
	inv := newInventoryService()
	_, _, err := inv.RecordMovement(chainWideCtx(t, ctx), tenantID, chainWidePrincipal(), service.RecordMovementRequest{
		WarehouseID: warehouseID,
		StockItemID: stockItemID,
		Type:        domain.MovementTypeIn,
		Quantity:    qty,
		Unit:        unit,
	})
	require.NoError(t, err)
}

// ---------------------------------------------------------------------------
// End-to-end BTO -> Shipment -> receive flow
// ---------------------------------------------------------------------------

func TestTransferOrderAndShipment_FullLifecycle_AutoClosesOnReceive(t *testing.T) {
	ctx := context.Background()
	sourceWH := createTestWarehouse(t, ctx, tenantA, branchSrc)
	destWH := createTestWarehouse(t, ctx, tenantA, branchReq)
	item := createTestStockItem(t, ctx, tenantA)
	seedSourceStock(t, ctx, tenantA, sourceWH.ID, item.ID, 100, "kg")

	toSvc := newTransferOrderService()
	shSvc := newShipmentService()
	invSvc := newInventoryService()

	// 1. Requesting branch creates a draft BTO for 40kg.
	bto, items, err := toSvc.Create(ctx, tenantA, service.CreateTransferOrderRequest{
		RequestingBranchID: branchReq,
		SourceBranchID:     branchSrc,
		Priority:           domain.PriorityNormal,
		Items: []service.CreateTransferOrderItemRequest{
			{StockItemID: item.ID, RequestedQty: 40, Unit: "kg"},
		},
	})
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, domain.BTOStatusDraft, bto.Status)

	// 2. Submit.
	bto, err = toSvc.Submit(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), bto.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.BTOStatusSubmitted, bto.Status)

	// 3. Source branch approves the full requested quantity.
	bto, err = toSvc.Approve(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), bto.ID, uuid.New(), []service.ApprovalItem{
		{StockItemID: item.ID, ApprovedQty: 40},
	})
	require.NoError(t, err)
	assert.Equal(t, domain.BTOStatusApproved, bto.Status)

	// 4. Source branch begins fulfilling.
	bto, err = toSvc.Fulfil(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), bto.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.BTOStatusFulfilling, bto.Status)

	// 5. A shipment is created against the BTO and approved.
	shipment, _, err := shSvc.Create(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), service.CreateShipmentRequest{
		FromWarehouseID: sourceWH.ID,
		ToBranchID:      branchReq,
		TransferOrderID: &bto.ID,
		Priority:        domain.PriorityNormal,
		Items: []service.CreateShipmentItemRequest{
			{StockItemID: item.ID, RequestedQty: 40, Unit: "kg"},
		},
	})
	require.NoError(t, err)
	shipment, err = shSvc.Approve(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), shipment.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.ShipmentStatusApproved, shipment.Status)

	// 6. Advance to in_transit: source warehouse loses 40kg, BTO -> shipped,
	// shipped_qty denormalized onto the BTO item.
	shipment, err = shSvc.Advance(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), shipment.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.ShipmentStatusInTransit, shipment.Status)

	sourceLevel, err := invSvc.GetLevel(ctx, tenantA, sourceWH.ID, item.ID)
	require.NoError(t, err)
	assert.InDelta(t, 60.0, sourceLevel.OnHand, 0.001, "source warehouse should have shipped out 40kg of 100kg")

	btoAfterShip, err := toSvc.Get(ctx, tenantA, bto.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.BTOStatusShipped, btoAfterShip.Status, "BTO must be driven to shipped by the shipment event, not directly")

	btoItemsAfterShip, err := toSvc.ListItems(ctx, tenantA, bto.ID)
	require.NoError(t, err)
	require.Len(t, btoItemsAfterShip, 1)
	assert.InDelta(t, 40.0, btoItemsAfterShip[0].ShippedQty, 0.001)
	assert.InDelta(t, 0.0, btoItemsAfterShip[0].ReceivedQty, 0.001, "received_qty must stay zero until the shipment is actually received")

	// 7. Receive into the destination warehouse: destination gains 40kg, BTO
	// -> received -> auto-closed (single item, fully received), atomically.
	shipment, err = shSvc.Receive(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), shipment.ID, destWH.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.ShipmentStatusReceived, shipment.Status)

	destLevel, err := invSvc.GetLevel(ctx, tenantA, destWH.ID, item.ID)
	require.NoError(t, err)
	assert.InDelta(t, 40.0, destLevel.OnHand, 0.001, "destination warehouse should have received 40kg")

	// Source warehouse must be unaffected by the receive step (only the
	// advance/out step touches it) — proves the two movements are distinct
	// ledger entries, not a single shared adjustment.
	sourceLevelAfterReceive, err := invSvc.GetLevel(ctx, tenantA, sourceWH.ID, item.ID)
	require.NoError(t, err)
	assert.InDelta(t, 60.0, sourceLevelAfterReceive.OnHand, 0.001)

	btoFinal, err := toSvc.Get(ctx, tenantA, bto.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.BTOStatusClosed, btoFinal.Status, "single-item BTO must auto-close once fully received")

	btoItemsFinal, err := toSvc.ListItems(ctx, tenantA, bto.ID)
	require.NoError(t, err)
	require.Len(t, btoItemsFinal, 1)
	assert.InDelta(t, 40.0, btoItemsFinal[0].ReceivedQty, 0.001)
}

// TestShipmentReceive_RejectsWrongStatus proves the shipment status guard
// runs inside the same atomic transaction as the stock movements: a shipment
// not in_transit cannot be received, and no stock movement is recorded for
// the attempt.
func TestShipmentReceive_RejectsWrongStatus(t *testing.T) {
	ctx := context.Background()
	sourceWH := createTestWarehouse(t, ctx, tenantA, branchSrc)
	destWH := createTestWarehouse(t, ctx, tenantA, branchReq)
	item := createTestStockItem(t, ctx, tenantA)
	seedSourceStock(t, ctx, tenantA, sourceWH.ID, item.ID, 50, "kg")

	shSvc := newShipmentService()
	invSvc := newInventoryService()

	shipment, _, err := shSvc.Create(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), service.CreateShipmentRequest{
		FromWarehouseID: sourceWH.ID,
		ToBranchID:      branchReq,
		Priority:        domain.PriorityNormal,
		Items: []service.CreateShipmentItemRequest{
			{StockItemID: item.ID, RequestedQty: 10, Unit: "kg"},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, domain.ShipmentStatusDraft, shipment.Status)

	// Still draft: receive must be rejected, and must not touch stock at all.
	_, err = shSvc.Receive(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), shipment.ID, destWH.ID)
	require.Error(t, err)
	var te *pub.TransitionError
	assert.ErrorAs(t, err, &te)

	_, err = invSvc.GetLevel(ctx, tenantA, destWH.ID, item.ID)
	assert.ErrorIs(t, err, pub.ErrNotFound, "destination level must not exist: the rejected receive must not have written any movement")

	sourceLevel, err := invSvc.GetLevel(ctx, tenantA, sourceWH.ID, item.ID)
	require.NoError(t, err)
	assert.InDelta(t, 50.0, sourceLevel.OnHand, 0.001, "source stock must be untouched by a rejected receive")
}

// TestTransferOrder_CrossTenant_NotFound proves RLS hides another tenant's BTO.
func TestTransferOrder_CrossTenant_NotFound(t *testing.T) {
	ctx := context.Background()
	toSvc := newTransferOrderService()
	item := createTestStockItem(t, ctx, tenantA)

	bto, _, err := toSvc.Create(ctx, tenantA, service.CreateTransferOrderRequest{
		RequestingBranchID: branchReq,
		SourceBranchID:     branchSrc,
		Items: []service.CreateTransferOrderItemRequest{
			{StockItemID: item.ID, RequestedQty: 5, Unit: "kg"},
		},
	})
	require.NoError(t, err)

	_, err = toSvc.Get(ctx, tenantB, bto.ID)
	assert.ErrorIs(t, err, pub.ErrNotFound)
}

// TestTransferOrder_PartialFulfilment_TwoShipments_AutoClosesOnLastReceive
// proves ADR-DATA-006's "bir BTO -> N shipment, kısmi sevkiyat mümkündür":
// a single BTO line (40kg) is fulfilled by two separate shipments (25kg +
// 15kg). Each shipment independently advances/receives; the BTO must only
// transition shipped/received once (idempotent transitionLinkedBTO) and must
// auto-close only after the SECOND shipment's receipt completes the total.
func TestTransferOrder_PartialFulfilment_TwoShipments_AutoClosesOnLastReceive(t *testing.T) {
	ctx := context.Background()
	sourceWH := createTestWarehouse(t, ctx, tenantA, branchSrc)
	destWH := createTestWarehouse(t, ctx, tenantA, branchReq)
	item := createTestStockItem(t, ctx, tenantA)
	seedSourceStock(t, ctx, tenantA, sourceWH.ID, item.ID, 100, "kg")

	toSvc := newTransferOrderService()
	shSvc := newShipmentService()

	bto, _, err := toSvc.Create(ctx, tenantA, service.CreateTransferOrderRequest{
		RequestingBranchID: branchReq,
		SourceBranchID:     branchSrc,
		Items: []service.CreateTransferOrderItemRequest{
			{StockItemID: item.ID, RequestedQty: 40, Unit: "kg"},
		},
	})
	require.NoError(t, err)
	bto, err = toSvc.Submit(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), bto.ID)
	require.NoError(t, err)
	bto, err = toSvc.Approve(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), bto.ID, uuid.New(), []service.ApprovalItem{
		{StockItemID: item.ID, ApprovedQty: 40},
	})
	require.NoError(t, err)
	bto, err = toSvc.Fulfil(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), bto.ID)
	require.NoError(t, err)

	createAndAdvance := func(qty float64) domain.Shipment {
		sh, _, err := shSvc.Create(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), service.CreateShipmentRequest{
			FromWarehouseID: sourceWH.ID,
			ToBranchID:      branchReq,
			TransferOrderID: &bto.ID,
			Items: []service.CreateShipmentItemRequest{
				{StockItemID: item.ID, RequestedQty: qty, Unit: "kg"},
			},
		})
		require.NoError(t, err)
		sh, err = shSvc.Approve(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), sh.ID)
		require.NoError(t, err)
		sh, err = shSvc.Advance(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), sh.ID)
		require.NoError(t, err)
		return sh
	}

	// Shipment 1 (25kg): drives fulfilling -> shipped.
	ship1 := createAndAdvance(25)
	afterShip1, err := toSvc.Get(ctx, tenantA, bto.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.BTOStatusShipped, afterShip1.Status)

	// Shipment 2 (15kg): BTO is already "shipped" — must be a no-op transition,
	// NOT a 409 (this is the regression this test targets).
	ship2 := createAndAdvance(15)
	afterShip2, err := toSvc.Get(ctx, tenantA, bto.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.BTOStatusShipped, afterShip2.Status, "second shipment's advance must not error on an already-shipped BTO")

	btoItemsAfterShip, err := toSvc.ListItems(ctx, tenantA, bto.ID)
	require.NoError(t, err)
	require.Len(t, btoItemsAfterShip, 1)
	assert.InDelta(t, 40.0, btoItemsAfterShip[0].ShippedQty, 0.001, "shipped_qty must sum both shipments (25+15)")

	// Receive shipment 1: partial receipt (25 of 40) — the BTO must stay at
	// 'shipped' (ADR-DATA-006 Açık Karar #1, closed 2026-07-04: the received
	// transition is quantity-gated on the BTO's full total across ALL of its
	// shipments, not on any single shipment's own receive event) so that a
	// still-outstanding sibling shipment can still Advance.
	_, err = shSvc.Receive(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), ship1.ID, destWH.ID)
	require.NoError(t, err)
	afterReceive1, err := toSvc.Get(ctx, tenantA, bto.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.BTOStatusShipped, afterReceive1.Status, "BTO must not move to received until every shipment's receive completes the total")

	btoItemsAfterReceive1, err := toSvc.ListItems(ctx, tenantA, bto.ID)
	require.NoError(t, err)
	require.Len(t, btoItemsAfterReceive1, 1)
	assert.InDelta(t, 25.0, btoItemsAfterReceive1[0].ReceivedQty, 0.001)

	// Receive shipment 2: completes the total (25+15=40) -> auto-close.
	_, err = shSvc.Receive(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), ship2.ID, destWH.ID)
	require.NoError(t, err)
	afterReceive2, err := toSvc.Get(ctx, tenantA, bto.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.BTOStatusClosed, afterReceive2.Status, "BTO must auto-close once BOTH shipments are received")

	btoItemsFinal, err := toSvc.ListItems(ctx, tenantA, bto.ID)
	require.NoError(t, err)
	require.Len(t, btoItemsFinal, 1)
	assert.InDelta(t, 40.0, btoItemsFinal[0].ReceivedQty, 0.001, "received_qty must sum both shipments (25+15)")
}

// TestTransferOrder_InterleavedShipments_SecondAdvanceNotBlockedByFirstReceive
// is the regression test for ADR-DATA-006 Açık Karar #1 (closed 2026-07-04):
// unlike TestTransferOrder_PartialFulfilment_TwoShipments_AutoClosesOnLastReceive
// (which advances both shipments before receiving either), this test
// interleaves the two shipments' lifecycles — ship1 advance, ship1 receive,
// ship2 advance, ship2 receive — which is the exact sequence that triggered
// the bug: a premature, unconditional shipped->received transition on
// ship1's receive left allowedBTOTransitions[received] = {closed} and made
// ship2's Advance (fulfilling/shipped -> shipped) fail with a 409, because
// the BTO was no longer at 'shipped' when ship2 tried to advance.
func TestTransferOrder_InterleavedShipments_SecondAdvanceNotBlockedByFirstReceive(t *testing.T) {
	ctx := context.Background()
	sourceWH := createTestWarehouse(t, ctx, tenantA, branchSrc)
	destWH := createTestWarehouse(t, ctx, tenantA, branchReq)
	item := createTestStockItem(t, ctx, tenantA)
	seedSourceStock(t, ctx, tenantA, sourceWH.ID, item.ID, 100, "kg")

	toSvc := newTransferOrderService()
	shSvc := newShipmentService()

	bto, _, err := toSvc.Create(ctx, tenantA, service.CreateTransferOrderRequest{
		RequestingBranchID: branchReq,
		SourceBranchID:     branchSrc,
		Items: []service.CreateTransferOrderItemRequest{
			{StockItemID: item.ID, RequestedQty: 40, Unit: "kg"},
		},
	})
	require.NoError(t, err)
	bto, err = toSvc.Submit(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), bto.ID)
	require.NoError(t, err)
	bto, err = toSvc.Approve(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), bto.ID, uuid.New(), []service.ApprovalItem{
		{StockItemID: item.ID, ApprovedQty: 40},
	})
	require.NoError(t, err)
	bto, err = toSvc.Fulfil(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), bto.ID)
	require.NoError(t, err)

	createShipment := func(qty float64) domain.Shipment {
		sh, _, err := shSvc.Create(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), service.CreateShipmentRequest{
			FromWarehouseID: sourceWH.ID,
			ToBranchID:      branchReq,
			TransferOrderID: &bto.ID,
			Items: []service.CreateShipmentItemRequest{
				{StockItemID: item.ID, RequestedQty: qty, Unit: "kg"},
			},
		})
		require.NoError(t, err)
		return sh
	}

	// Shipment 1 (25kg): create, advance (fulfilling -> shipped), receive
	// (partial: 25 of 40 -> BTO must stay 'shipped', not jump to 'received').
	ship1 := createShipment(25)
	_, err = shSvc.Approve(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), ship1.ID)
	require.NoError(t, err)
	_, err = shSvc.Advance(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), ship1.ID)
	require.NoError(t, err)
	_, err = shSvc.Receive(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), ship1.ID, destWH.ID)
	require.NoError(t, err)

	afterReceive1, err := toSvc.Get(ctx, tenantA, bto.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.BTOStatusShipped, afterReceive1.Status, "partial receive must not close off the shipped->shipped no-op that ship2's Advance depends on")

	// Shipment 2 (15kg): created and advanced AFTER ship1 already received.
	// This is the exact step that used to 409 before the fix.
	ship2 := createShipment(15)
	_, err = shSvc.Approve(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), ship2.ID)
	require.NoError(t, err)
	_, err = shSvc.Advance(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), ship2.ID)
	require.NoError(t, err, "ship2's Advance must not be blocked by ship1's earlier receive")

	afterShip2, err := toSvc.Get(ctx, tenantA, bto.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.BTOStatusShipped, afterShip2.Status)

	// Receive shipment 2: completes the total (25+15=40) -> shipped -> received -> closed.
	_, err = shSvc.Receive(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), ship2.ID, destWH.ID)
	require.NoError(t, err)

	afterReceive2, err := toSvc.Get(ctx, tenantA, bto.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.BTOStatusClosed, afterReceive2.Status, "BTO must auto-close once the last interleaved shipment completes the total")

	btoItemsFinal, err := toSvc.ListItems(ctx, tenantA, bto.ID)
	require.NoError(t, err)
	require.Len(t, btoItemsFinal, 1)
	assert.InDelta(t, 40.0, btoItemsFinal[0].ReceivedQty, 0.001, "received_qty must sum both interleaved shipments (25+15)")
}

// TestTransferOrderApprove_SetsUnitPrice proves that the source branch can
// price a BTO item at approve time (ADR-DATA-006 eklenti / ADR-DATA-007
// SS4), and that an un-priced item on the same BTO stays nil — pricing is
// per-item, not all-or-nothing.
func TestTransferOrderApprove_SetsUnitPrice(t *testing.T) {
	ctx := context.Background()
	itemPriced := createTestStockItem(t, ctx, tenantA)
	itemFree := createTestStockItem(t, ctx, tenantA)

	toSvc := newTransferOrderService()

	bto, _, err := toSvc.Create(ctx, tenantA, service.CreateTransferOrderRequest{
		RequestingBranchID: branchReq,
		SourceBranchID:     branchSrc,
		Items: []service.CreateTransferOrderItemRequest{
			{StockItemID: itemPriced.ID, RequestedQty: 10, Unit: "kg"},
			{StockItemID: itemFree.ID, RequestedQty: 5, Unit: "kg"},
		},
	})
	require.NoError(t, err)

	// Before approval, no item has a price (ADR-DATA-007: "talep asamasinda
	// fiyat yok").
	itemsBeforeApproval, err := toSvc.ListItems(ctx, tenantA, bto.ID)
	require.NoError(t, err)
	for _, it := range itemsBeforeApproval {
		assert.Nil(t, it.UnitPrice)
		assert.Nil(t, it.Currency)
	}

	bto, err = toSvc.Submit(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), bto.ID)
	require.NoError(t, err)

	price := 12.50
	bto, err = toSvc.Approve(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), bto.ID, uuid.New(), []service.ApprovalItem{
		{StockItemID: itemPriced.ID, ApprovedQty: 10, UnitPrice: &price, Currency: "TRY"},
		{StockItemID: itemFree.ID, ApprovedQty: 5}, // no price: free/approved_suppliers policy item
	})
	require.NoError(t, err)
	assert.Equal(t, domain.BTOStatusApproved, bto.Status)

	itemsAfterApproval, err := toSvc.ListItems(ctx, tenantA, bto.ID)
	require.NoError(t, err)
	byStockItem := make(map[uuid.UUID]domain.BranchTransferOrderItem, len(itemsAfterApproval))
	for _, it := range itemsAfterApproval {
		byStockItem[it.StockItemID] = it
	}

	priced := byStockItem[itemPriced.ID]
	require.NotNil(t, priced.UnitPrice)
	assert.InDelta(t, 12.50, *priced.UnitPrice, 0.0001)
	require.NotNil(t, priced.Currency)
	assert.Equal(t, "TRY", *priced.Currency)

	free := byStockItem[itemFree.ID]
	assert.Nil(t, free.UnitPrice, "an item the source branch chooses not to price must stay nil, not default to zero")
	assert.Nil(t, free.Currency)
}

// TestTransferOrderAndShipment_PriceFlow_ReceiveSetsBranchLocalCost is the
// end-to-end proof of ADR-DATA-007 SS4: a source branch prices a BTO item at
// approve time, the shipment created against that BTO copies the price, and
// receiving the shipment stamps the destination warehouse's stock_levels row
// with that price as its branch-local cost (source=transfer).
func TestTransferOrderAndShipment_PriceFlow_ReceiveSetsBranchLocalCost(t *testing.T) {
	ctx := context.Background()
	sourceWH := createTestWarehouse(t, ctx, tenantA, branchSrc)
	destWH := createTestWarehouse(t, ctx, tenantA, branchReq)
	item := createTestStockItem(t, ctx, tenantA)
	seedSourceStock(t, ctx, tenantA, sourceWH.ID, item.ID, 100, "kg")

	toSvc := newTransferOrderService()
	shSvc := newShipmentService()
	invSvc := newInventoryService()

	bto, _, err := toSvc.Create(ctx, tenantA, service.CreateTransferOrderRequest{
		RequestingBranchID: branchReq,
		SourceBranchID:     branchSrc,
		Items: []service.CreateTransferOrderItemRequest{
			{StockItemID: item.ID, RequestedQty: 40, Unit: "kg"},
		},
	})
	require.NoError(t, err)
	bto, err = toSvc.Submit(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), bto.ID)
	require.NoError(t, err)

	price := 7.25
	bto, err = toSvc.Approve(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), bto.ID, uuid.New(), []service.ApprovalItem{
		{StockItemID: item.ID, ApprovedQty: 40, UnitPrice: &price, Currency: "TRY"},
	})
	require.NoError(t, err)
	bto, err = toSvc.Fulfil(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), bto.ID)
	require.NoError(t, err)

	// Shipment created against the priced BTO: no per-line override given,
	// so the price must be copied from the BTO item.
	shipment, shItems, err := shSvc.Create(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), service.CreateShipmentRequest{
		FromWarehouseID: sourceWH.ID,
		ToBranchID:      branchReq,
		TransferOrderID: &bto.ID,
		Items: []service.CreateShipmentItemRequest{
			{StockItemID: item.ID, RequestedQty: 40, Unit: "kg"},
		},
	})
	require.NoError(t, err)
	require.Len(t, shItems, 1)
	require.NotNil(t, shItems[0].UnitPrice, "shipment item must copy the BTO item's transfer price")
	assert.InDelta(t, 7.25, *shItems[0].UnitPrice, 0.0001)
	require.NotNil(t, shItems[0].Currency)
	assert.Equal(t, "TRY", *shItems[0].Currency)

	shipment, err = shSvc.Approve(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), shipment.ID)
	require.NoError(t, err)
	shipment, err = shSvc.Advance(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), shipment.ID)
	require.NoError(t, err)

	// Before receive: destination warehouse has no stock_levels row at all
	// yet (nothing has ever moved there), so no cost either.
	_, err = invSvc.GetLevel(ctx, tenantA, destWH.ID, item.ID)
	assert.Error(t, err, "destination level must not exist before anything is received into it")

	_, err = shSvc.Receive(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), shipment.ID, destWH.ID)
	require.NoError(t, err)

	destLevel, err := invSvc.GetLevel(ctx, tenantA, destWH.ID, item.ID)
	require.NoError(t, err)
	assert.InDelta(t, 40.0, destLevel.OnHand, 0.001)
	require.NotNil(t, destLevel.LastUnitCost, "receive must stamp the destination level's branch-local cost from the transfer price")
	assert.InDelta(t, 7.25, *destLevel.LastUnitCost, 0.0001)
	require.NotNil(t, destLevel.LastCostCurrency)
	assert.Equal(t, "TRY", *destLevel.LastCostCurrency)
	require.NotNil(t, destLevel.LastCostSource)
	assert.Equal(t, string(domain.CostSourceTransfer), *destLevel.LastCostSource)
	require.NotNil(t, destLevel.LastCostAt)
	assert.False(t, destLevel.LastCostAt.IsZero())

	// Source warehouse's own level must be unaffected: cost tracking is
	// destination-scoped, the shipment does not retroactively price the
	// source's existing stock.
	sourceLevel, err := invSvc.GetLevel(ctx, tenantA, sourceWH.ID, item.ID)
	require.NoError(t, err)
	assert.Nil(t, sourceLevel.LastUnitCost)
}

// TestTransferOrderAndShipment_NoPriceLeavesCostNil proves the negative
// path: a transfer with no price set anywhere (the existing default flow)
// must leave the destination level's cost columns nil after receive, not
// zero or some other sentinel — this is the regression guard for
// TestTransferOrderAndShipment_FullLifecycle_AutoClosesOnReceive, which
// exercises the exact same flow with no prices at all.
func TestTransferOrderAndShipment_NoPriceLeavesCostNil(t *testing.T) {
	ctx := context.Background()
	sourceWH := createTestWarehouse(t, ctx, tenantA, branchSrc)
	destWH := createTestWarehouse(t, ctx, tenantA, branchReq)
	item := createTestStockItem(t, ctx, tenantA)
	seedSourceStock(t, ctx, tenantA, sourceWH.ID, item.ID, 100, "kg")

	toSvc := newTransferOrderService()
	shSvc := newShipmentService()
	invSvc := newInventoryService()

	bto, _, err := toSvc.Create(ctx, tenantA, service.CreateTransferOrderRequest{
		RequestingBranchID: branchReq,
		SourceBranchID:     branchSrc,
		Items: []service.CreateTransferOrderItemRequest{
			{StockItemID: item.ID, RequestedQty: 40, Unit: "kg"},
		},
	})
	require.NoError(t, err)
	bto, err = toSvc.Submit(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), bto.ID)
	require.NoError(t, err)
	bto, err = toSvc.Approve(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), bto.ID, uuid.New(), []service.ApprovalItem{
		{StockItemID: item.ID, ApprovedQty: 40}, // no UnitPrice
	})
	require.NoError(t, err)
	bto, err = toSvc.Fulfil(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), bto.ID)
	require.NoError(t, err)

	shipment, shItems, err := shSvc.Create(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), service.CreateShipmentRequest{
		FromWarehouseID: sourceWH.ID,
		ToBranchID:      branchReq,
		TransferOrderID: &bto.ID,
		Items: []service.CreateShipmentItemRequest{
			{StockItemID: item.ID, RequestedQty: 40, Unit: "kg"},
		},
	})
	require.NoError(t, err)
	require.Len(t, shItems, 1)
	assert.Nil(t, shItems[0].UnitPrice)
	assert.Nil(t, shItems[0].Currency)

	shipment, err = shSvc.Approve(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), shipment.ID)
	require.NoError(t, err)
	shipment, err = shSvc.Advance(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), shipment.ID)
	require.NoError(t, err)
	_, err = shSvc.Receive(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), shipment.ID, destWH.ID)
	require.NoError(t, err)

	destLevel, err := invSvc.GetLevel(ctx, tenantA, destWH.ID, item.ID)
	require.NoError(t, err)
	assert.InDelta(t, 40.0, destLevel.OnHand, 0.001)
	assert.Nil(t, destLevel.LastUnitCost, "no price anywhere in the flow must leave the destination cost nil, not zero")
	assert.Nil(t, destLevel.LastCostCurrency)
	assert.Nil(t, destLevel.LastCostSource)
	assert.Nil(t, destLevel.LastCostAt)
}

// TestShipmentCreate_OverridesBTOPricePerLine proves that an explicit
// per-line UnitPrice on shipment create wins over the BTO item's price
// (ADR-DATA-006 eklenti: "kopyalanir/override edilebilir").
func TestShipmentCreate_OverridesBTOPricePerLine(t *testing.T) {
	ctx := context.Background()
	sourceWH := createTestWarehouse(t, ctx, tenantA, branchSrc)
	item := createTestStockItem(t, ctx, tenantA)
	seedSourceStock(t, ctx, tenantA, sourceWH.ID, item.ID, 100, "kg")

	toSvc := newTransferOrderService()
	shSvc := newShipmentService()

	bto, _, err := toSvc.Create(ctx, tenantA, service.CreateTransferOrderRequest{
		RequestingBranchID: branchReq,
		SourceBranchID:     branchSrc,
		Items: []service.CreateTransferOrderItemRequest{
			{StockItemID: item.ID, RequestedQty: 40, Unit: "kg"},
		},
	})
	require.NoError(t, err)
	bto, err = toSvc.Submit(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), bto.ID)
	require.NoError(t, err)
	btoPrice := 7.25
	bto, err = toSvc.Approve(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), bto.ID, uuid.New(), []service.ApprovalItem{
		{StockItemID: item.ID, ApprovedQty: 40, UnitPrice: &btoPrice, Currency: "TRY"},
	})
	require.NoError(t, err)

	overridePrice := 9.99
	_, shItems, err := shSvc.Create(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), service.CreateShipmentRequest{
		FromWarehouseID: sourceWH.ID,
		ToBranchID:      branchReq,
		TransferOrderID: &bto.ID,
		Items: []service.CreateShipmentItemRequest{
			{StockItemID: item.ID, RequestedQty: 40, Unit: "kg", UnitPrice: &overridePrice, Currency: "USD"},
		},
	})
	require.NoError(t, err)
	require.Len(t, shItems, 1)
	require.NotNil(t, shItems[0].UnitPrice)
	assert.InDelta(t, 9.99, *shItems[0].UnitPrice, 0.0001, "explicit per-line price must override the BTO item's price")
	require.NotNil(t, shItems[0].Currency)
	assert.Equal(t, "USD", *shItems[0].Currency)
}

// TestTransferOrder_PartialDelivery_CostSurvivesLaterPricelessReceive covers
// task item 5's "kısmi teslimatta davranış" and directly proves the
// clobber-protection documented on ShipmentService.Receive: a priced
// partial-fulfilment BTO stamps the destination cost correctly across two
// shipments, and a later, unrelated priceless receive into the SAME
// (warehouse, stock_item) must never null out that already-recorded cost.
func TestTransferOrder_PartialDelivery_CostSurvivesLaterPricelessReceive(t *testing.T) {
	ctx := context.Background()
	sourceWH := createTestWarehouse(t, ctx, tenantA, branchSrc)
	destWH := createTestWarehouse(t, ctx, tenantA, branchReq)
	item := createTestStockItem(t, ctx, tenantA)
	seedSourceStock(t, ctx, tenantA, sourceWH.ID, item.ID, 100, "kg")

	toSvc := newTransferOrderService()
	shSvc := newShipmentService()
	invSvc := newInventoryService()

	bto, _, err := toSvc.Create(ctx, tenantA, service.CreateTransferOrderRequest{
		RequestingBranchID: branchReq,
		SourceBranchID:     branchSrc,
		Items: []service.CreateTransferOrderItemRequest{
			{StockItemID: item.ID, RequestedQty: 40, Unit: "kg"},
		},
	})
	require.NoError(t, err)
	bto, err = toSvc.Submit(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), bto.ID)
	require.NoError(t, err)
	price := 8.40
	bto, err = toSvc.Approve(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), bto.ID, uuid.New(), []service.ApprovalItem{
		{StockItemID: item.ID, ApprovedQty: 40, UnitPrice: &price, Currency: "TRY"},
	})
	require.NoError(t, err)
	bto, err = toSvc.Fulfil(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), bto.ID)
	require.NoError(t, err)

	createAndAdvance := func(qty float64) domain.Shipment {
		sh, _, err := shSvc.Create(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), service.CreateShipmentRequest{
			FromWarehouseID: sourceWH.ID,
			ToBranchID:      branchReq,
			TransferOrderID: &bto.ID,
			Items: []service.CreateShipmentItemRequest{
				{StockItemID: item.ID, RequestedQty: qty, Unit: "kg"},
			},
		})
		require.NoError(t, err)
		sh, err = shSvc.Approve(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), sh.ID)
		require.NoError(t, err)
		sh, err = shSvc.Advance(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), sh.ID)
		require.NoError(t, err)
		return sh
	}

	// Both partial shipments are created and advanced (fulfilling -> shipped)
	// before either is received, matching ADR-DATA-006's "one BTO, N
	// shipments" flow (see TestTransferOrder_PartialFulfilment_...): the BTO
	// only leaves "shipped" once its first Receive runs.
	ship1 := createAndAdvance(25)
	ship2 := createAndAdvance(15)

	// Receive shipment 1 (25kg of 40kg): destination gets its first cost stamp.
	_, err = shSvc.Receive(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), ship1.ID, destWH.ID)
	require.NoError(t, err)

	afterPartial, err := invSvc.GetLevel(ctx, tenantA, destWH.ID, item.ID)
	require.NoError(t, err)
	assert.InDelta(t, 25.0, afterPartial.OnHand, 0.001)
	require.NotNil(t, afterPartial.LastUnitCost, "first partial receive must stamp the cost")
	assert.InDelta(t, 8.40, *afterPartial.LastUnitCost, 0.0001)

	// Receive shipment 2 (15kg): completes the BTO (40/40), same price.
	_, err = shSvc.Receive(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), ship2.ID, destWH.ID)
	require.NoError(t, err)

	afterFull, err := invSvc.GetLevel(ctx, tenantA, destWH.ID, item.ID)
	require.NoError(t, err)
	assert.InDelta(t, 40.0, afterFull.OnHand, 0.001)
	require.NotNil(t, afterFull.LastUnitCost, "second partial receive must keep the cost set")
	assert.InDelta(t, 8.40, *afterFull.LastUnitCost, 0.0001)
	require.NotNil(t, afterFull.LastCostSource)
	assert.Equal(t, string(domain.CostSourceTransfer), *afterFull.LastCostSource)

	// An unrelated, priceless ad-hoc shipment (no BTO link) into the SAME
	// (warehouse, stock_item) must add stock without nulling the
	// already-recorded cost -- the "doluysa" guard in
	// ShipmentService.Receive must not clobber a known cost with a later
	// priceless movement.
	adhoc, _, err := shSvc.Create(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), service.CreateShipmentRequest{
		FromWarehouseID: sourceWH.ID,
		ToBranchID:      branchReq,
		Items: []service.CreateShipmentItemRequest{
			{StockItemID: item.ID, RequestedQty: 10, Unit: "kg"},
		},
	})
	require.NoError(t, err)
	adhoc, err = shSvc.Approve(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), adhoc.ID)
	require.NoError(t, err)
	adhoc, err = shSvc.Advance(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), adhoc.ID)
	require.NoError(t, err)
	_, err = shSvc.Receive(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), adhoc.ID, destWH.ID)
	require.NoError(t, err)

	final, err := invSvc.GetLevel(ctx, tenantA, destWH.ID, item.ID)
	require.NoError(t, err)
	assert.InDelta(t, 50.0, final.OnHand, 0.001, "on_hand must still accumulate (40+10)")
	require.NotNil(t, final.LastUnitCost, "a later priceless receive must NOT clobber a previously recorded cost")
	assert.InDelta(t, 8.40, *final.LastUnitCost, 0.0001)
	require.NotNil(t, final.LastCostSource)
	assert.Equal(t, string(domain.CostSourceTransfer), *final.LastCostSource)
}

// TestShipmentAdvance_RejectsInsufficientStock proves the availability guard:
// AdjustOnHand's GREATEST(0, ...) clamp must never be reached via Advance —
// an over-ship is rejected outright rather than silently manufacturing stock.
func TestShipmentAdvance_RejectsInsufficientStock(t *testing.T) {
	ctx := context.Background()
	sourceWH := createTestWarehouse(t, ctx, tenantA, branchSrc)
	item := createTestStockItem(t, ctx, tenantA)
	seedSourceStock(t, ctx, tenantA, sourceWH.ID, item.ID, 10, "kg")

	shSvc := newShipmentService()
	invSvc := newInventoryService()

	sh, _, err := shSvc.Create(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), service.CreateShipmentRequest{
		FromWarehouseID: sourceWH.ID,
		ToBranchID:      branchReq,
		Items: []service.CreateShipmentItemRequest{
			{StockItemID: item.ID, RequestedQty: 40, Unit: "kg"},
		},
	})
	require.NoError(t, err)
	sh, err = shSvc.Approve(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), sh.ID)
	require.NoError(t, err)

	_, err = shSvc.Advance(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), sh.ID)
	require.Error(t, err)
	var ve *pub.ValidationError
	assert.ErrorAs(t, err, &ve)

	// Source stock must be completely untouched by the rejected advance.
	level, err := invSvc.GetLevel(ctx, tenantA, sourceWH.ID, item.ID)
	require.NoError(t, err)
	assert.InDelta(t, 10.0, level.OnHand, 0.001, "rejected advance must not have written any movement")
}

// ---------------------------------------------------------------------------
// SupplyPolicyService (ADR-DATA-007)
// ---------------------------------------------------------------------------

func newSupplyPolicyService() *service.SupplyPolicyService {
	return service.NewSupplyPolicyService(service.SupplyPolicyParams{
		DB: sharedPool, Repo: repo.NewSupplyPolicyRepo(), StockRepo: repo.NewStockItemRepo(), Logger: zap.NewNop(),
	})
}

// TestSupplyPolicyService_CreateAndList proves the basic persistence round
// trip and that List surfaces every tenant row (management listing, not the
// resolver).
func TestSupplyPolicyService_CreateAndList(t *testing.T) {
	ctx := context.Background()
	tenantID := uuid.New()
	item := createTestStockItem(t, ctx, tenantID)

	svc := newSupplyPolicyService()
	created, err := svc.Create(ctx, tenantID, service.CreateSupplyPolicyRequest{
		Scope:       domain.SupplyScopeStockItem,
		StockItemID: &item.ID,
		Mode:        domain.SupplyModeApprovedSuppliers,
		ApprovedSupplierIDs: []uuid.UUID{
			uuid.MustParse("11111111-0000-0000-0000-000000000001"),
			uuid.MustParse("11111111-0000-0000-0000-000000000002"),
		},
	})
	require.NoError(t, err)
	assert.NotEqual(t, uuid.Nil, created.ID)
	assert.Nil(t, created.BranchID, "v1 Create must always write a tenant-wide (branch_id NULL) row")
	assert.Len(t, created.ApprovedSupplierIDs, 2)

	list, err := svc.List(ctx, tenantID)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, created.ID, list[0].ID)
}

// TestSupplyPolicyService_CreateIsAppendOnly proves a second Create for the
// same resolution key does NOT update the first row (DATA-002 immutability
// ruhu): List must return BOTH rows, and the resolver (proven at the domain
// layer, see domain/supply_policy_test.go) picks the winner by effective_from.
func TestSupplyPolicyService_CreateIsAppendOnly(t *testing.T) {
	ctx := context.Background()
	tenantID := uuid.New()
	item := createTestStockItem(t, ctx, tenantID)

	svc := newSupplyPolicyService()
	_, err := svc.Create(ctx, tenantID, service.CreateSupplyPolicyRequest{
		Scope: domain.SupplyScopeStockItem, StockItemID: &item.ID, Mode: domain.SupplyModeExclusiveHQ,
		EffectiveFrom: time.Now().Add(-2 * time.Hour),
	})
	require.NoError(t, err)
	_, err = svc.Create(ctx, tenantID, service.CreateSupplyPolicyRequest{
		Scope: domain.SupplyScopeStockItem, StockItemID: &item.ID, Mode: domain.SupplyModeFree,
		EffectiveFrom: time.Now().Add(-1 * time.Hour),
	})
	require.NoError(t, err)

	list, err := svc.List(ctx, tenantID)
	require.NoError(t, err)
	assert.Len(t, list, 2, "policy change must append a new row, never update the existing one")
}

// TestSupplyPolicyService_EffectivePolicyFor_NoRowDefaultsToExclusiveHQ
// proves the Faz-1 safe default reaches all the way through the service
// (repo load + domain.ResolvePolicy) with zero configured policy rows.
func TestSupplyPolicyService_EffectivePolicyFor_NoRowDefaultsToExclusiveHQ(t *testing.T) {
	ctx := context.Background()
	tenantID := uuid.New()
	item := createTestStockItem(t, ctx, tenantID)

	svc := newSupplyPolicyService()
	mode, approved, err := svc.EffectivePolicyFor(ctx, tenantID, item.ID, branchReq)
	require.NoError(t, err)
	assert.Equal(t, domain.SupplyModeExclusiveHQ, mode)
	assert.Nil(t, approved)
}

// TestSupplyPolicyService_EffectivePolicyFor_TenantIsolation proves a policy
// row created under one tenant never leaks into another tenant's
// resolution — RLS hides the row entirely, so the resolver falls back to
// the exclusive_hq default rather than erroring.
func TestSupplyPolicyService_EffectivePolicyFor_TenantIsolation(t *testing.T) {
	ctx := context.Background()
	item := createTestStockItem(t, ctx, tenantA)

	svc := newSupplyPolicyService()
	_, err := svc.Create(ctx, tenantA, service.CreateSupplyPolicyRequest{
		Scope: domain.SupplyScopeStockItem, StockItemID: &item.ID, Mode: domain.SupplyModeFree,
	})
	require.NoError(t, err)

	// tenantB has no matching stock item (RLS-hidden), so GetByID fails —
	// this is the expected not-found behaviour, not a leak.
	_, _, err = svc.EffectivePolicyFor(ctx, tenantB, item.ID, branchReq)
	assert.ErrorIs(t, err, pub.ErrNotFound)
}

// TestSupplyPolicyService_EffectivePolicyFor_ResolvesLatestTenantItemPolicy
// exercises the full stack (repo -> domain.ResolvePolicy) for the
// tenant+item tier, proving EffectivePolicyFor picks the most recent
// effective_from row.
func TestSupplyPolicyService_EffectivePolicyFor_ResolvesLatestTenantItemPolicy(t *testing.T) {
	ctx := context.Background()
	tenantID := uuid.New()
	item := createTestStockItem(t, ctx, tenantID)

	svc := newSupplyPolicyService()
	_, err := svc.Create(ctx, tenantID, service.CreateSupplyPolicyRequest{
		Scope: domain.SupplyScopeStockItem, StockItemID: &item.ID, Mode: domain.SupplyModeExclusiveHQ,
		EffectiveFrom: time.Now().Add(-24 * time.Hour),
	})
	require.NoError(t, err)
	supplierID := uuid.New()
	_, err = svc.Create(ctx, tenantID, service.CreateSupplyPolicyRequest{
		Scope: domain.SupplyScopeStockItem, StockItemID: &item.ID, Mode: domain.SupplyModeApprovedSuppliers,
		ApprovedSupplierIDs: []uuid.UUID{supplierID}, EffectiveFrom: time.Now().Add(-1 * time.Hour),
	})
	require.NoError(t, err)

	mode, approved, err := svc.EffectivePolicyFor(ctx, tenantID, item.ID, branchReq)
	require.NoError(t, err)
	assert.Equal(t, domain.SupplyModeApprovedSuppliers, mode)
	assert.Equal(t, []uuid.UUID{supplierID}, approved)
}
