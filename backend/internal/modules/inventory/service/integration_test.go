package service_test

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
	"go.uber.org/zap"

	"onlinemenu.tr/internal/modules/inventory/domain"
	pub "onlinemenu.tr/internal/modules/inventory/public"
	"onlinemenu.tr/internal/modules/inventory/repo"
	"onlinemenu.tr/internal/modules/inventory/service"
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
		DB: sharedPool, Repo: repo.NewStockItemRepo(), Logger: zap.NewNop(),
	})
}

func newInventoryService() *service.InventoryService {
	return service.NewInventoryService(service.Params{
		DB: sharedPool, LvlRepo: repo.NewStockLevelRepo(), MvRepo: repo.NewStockMovementRepo(), Logger: zap.NewNop(),
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
		Logger:       zap.NewNop(),
	})
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
	_, _, err := inv.RecordMovement(ctx, tenantID, service.RecordMovementRequest{
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
	bto, err = toSvc.Submit(ctx, tenantA, bto.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.BTOStatusSubmitted, bto.Status)

	// 3. Source branch approves the full requested quantity.
	bto, err = toSvc.Approve(ctx, tenantA, bto.ID, uuid.New(), []service.ApprovalItem{
		{StockItemID: item.ID, ApprovedQty: 40},
	})
	require.NoError(t, err)
	assert.Equal(t, domain.BTOStatusApproved, bto.Status)

	// 4. Source branch begins fulfilling.
	bto, err = toSvc.Fulfil(ctx, tenantA, bto.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.BTOStatusFulfilling, bto.Status)

	// 5. A shipment is created against the BTO and approved.
	shipment, _, err := shSvc.Create(ctx, tenantA, service.CreateShipmentRequest{
		FromWarehouseID: sourceWH.ID,
		ToBranchID:      branchReq,
		TransferOrderID: &bto.ID,
		Priority:        domain.PriorityNormal,
		Items: []service.CreateShipmentItemRequest{
			{StockItemID: item.ID, RequestedQty: 40, Unit: "kg"},
		},
	})
	require.NoError(t, err)
	shipment, err = shSvc.Approve(ctx, tenantA, shipment.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.ShipmentStatusApproved, shipment.Status)

	// 6. Advance to in_transit: source warehouse loses 40kg, BTO -> shipped,
	// shipped_qty denormalized onto the BTO item.
	shipment, err = shSvc.Advance(ctx, tenantA, shipment.ID)
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
	shipment, err = shSvc.Receive(ctx, tenantA, shipment.ID, destWH.ID)
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

	shipment, _, err := shSvc.Create(ctx, tenantA, service.CreateShipmentRequest{
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
	_, err = shSvc.Receive(ctx, tenantA, shipment.ID, destWH.ID)
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
	bto, err = toSvc.Submit(ctx, tenantA, bto.ID)
	require.NoError(t, err)
	bto, err = toSvc.Approve(ctx, tenantA, bto.ID, uuid.New(), []service.ApprovalItem{
		{StockItemID: item.ID, ApprovedQty: 40},
	})
	require.NoError(t, err)
	bto, err = toSvc.Fulfil(ctx, tenantA, bto.ID)
	require.NoError(t, err)

	createAndAdvance := func(qty float64) domain.Shipment {
		sh, _, err := shSvc.Create(ctx, tenantA, service.CreateShipmentRequest{
			FromWarehouseID: sourceWH.ID,
			ToBranchID:      branchReq,
			TransferOrderID: &bto.ID,
			Items: []service.CreateShipmentItemRequest{
				{StockItemID: item.ID, RequestedQty: qty, Unit: "kg"},
			},
		})
		require.NoError(t, err)
		sh, err = shSvc.Approve(ctx, tenantA, sh.ID)
		require.NoError(t, err)
		sh, err = shSvc.Advance(ctx, tenantA, sh.ID)
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

	// Receive shipment 1: partial receipt, BTO -> received, but NOT closed yet
	// (shipment 2's 15kg has not arrived).
	_, err = shSvc.Receive(ctx, tenantA, ship1.ID, destWH.ID)
	require.NoError(t, err)
	afterReceive1, err := toSvc.Get(ctx, tenantA, bto.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.BTOStatusReceived, afterReceive1.Status)

	btoItemsAfterReceive1, err := toSvc.ListItems(ctx, tenantA, bto.ID)
	require.NoError(t, err)
	require.Len(t, btoItemsAfterReceive1, 1)
	assert.InDelta(t, 25.0, btoItemsAfterReceive1[0].ReceivedQty, 0.001)

	// Receive shipment 2: completes the total (25+15=40) -> auto-close.
	_, err = shSvc.Receive(ctx, tenantA, ship2.ID, destWH.ID)
	require.NoError(t, err)
	afterReceive2, err := toSvc.Get(ctx, tenantA, bto.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.BTOStatusClosed, afterReceive2.Status, "BTO must auto-close once BOTH shipments are received")

	btoItemsFinal, err := toSvc.ListItems(ctx, tenantA, bto.ID)
	require.NoError(t, err)
	require.Len(t, btoItemsFinal, 1)
	assert.InDelta(t, 40.0, btoItemsFinal[0].ReceivedQty, 0.001, "received_qty must sum both shipments (25+15)")
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

	sh, _, err := shSvc.Create(ctx, tenantA, service.CreateShipmentRequest{
		FromWarehouseID: sourceWH.ID,
		ToBranchID:      branchReq,
		Items: []service.CreateShipmentItemRequest{
			{StockItemID: item.ID, RequestedQty: 40, Unit: "kg"},
		},
	})
	require.NoError(t, err)
	sh, err = shSvc.Approve(ctx, tenantA, sh.ID)
	require.NoError(t, err)

	_, err = shSvc.Advance(ctx, tenantA, sh.ID)
	require.Error(t, err)
	var ve *pub.ValidationError
	assert.ErrorAs(t, err, &ve)

	// Source stock must be completely untouched by the rejected advance.
	level, err := invSvc.GetLevel(ctx, tenantA, sourceWH.ID, item.ID)
	require.NoError(t, err)
	assert.InDelta(t, 10.0, level.OnHand, 0.001, "rejected advance must not have written any movement")
}
