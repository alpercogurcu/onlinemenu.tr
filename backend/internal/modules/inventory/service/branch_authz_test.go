package service_test

// Branch-scope authorization tests (ADR-AUTH-001 layer 3, ADR-DATA-006,
// docs/lessons-from-b2b.md item 6 — "authz rules must be bound to a test or
// the work isn't done"). These run against the shared testcontainers pool
// from integration_test.go's TestMain.
//
// Pattern per rule (as directed by the security sprint task): allowed branch
// -> success; foreign branch -> pub.ErrBranchForbidden; chain-wide principal
// (the realistic shape of a manager's membership, per identity module's
// "nil = chain-wide" contract) -> exempt.
//
// The scope=="tenant" exemption path (for a hypothetical BRANCH-scoped
// manager membership, where Principal.BranchID is set but OPA still resolves
// scope=tenant) is a distinct code path from the chain-wide BranchID==nil
// path exercised here, and is covered separately in
// branch_authz_scope_test.go (no DB required there — it drives the real OPA
// engine + requireBranch directly).

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"onlinemenu.tr/internal/modules/inventory/domain"
	pub "onlinemenu.tr/internal/modules/inventory/public"
	"onlinemenu.tr/internal/modules/inventory/service"
	"onlinemenu.tr/internal/platform/auth"
)

// branchPrincipal returns a staff principal scoped to a single branch — the
// counterpart to chainWidePrincipal for asserting that a principal belonging
// to ONE branch cannot act on another branch's resource.
func branchPrincipal(branchID uuid.UUID) auth.Principal {
	return auth.Principal{
		PersonID: uuid.New(),
		Ctx:      auth.ContextStaff,
		TenantID: tenantA,
		BranchID: branchID,
		RoleIDs:  []uuid.UUID{uuid.New()},
	}
}

// ---------------------------------------------------------------------------
// Branch transfer order authz
// ---------------------------------------------------------------------------

func newDraftBTOForAuthz(t *testing.T, ctx context.Context, toSvc *service.TransferOrderService, itemID uuid.UUID) domain.BranchTransferOrder {
	t.Helper()
	bto, _, err := toSvc.Create(ctx, tenantA, service.CreateTransferOrderRequest{
		RequestingBranchID: branchReq,
		SourceBranchID:     branchSrc,
		Items: []service.CreateTransferOrderItemRequest{
			{StockItemID: itemID, RequestedQty: 5, Unit: "kg"},
		},
	})
	require.NoError(t, err)
	return bto
}

func TestTransferOrderAuthz_Submit(t *testing.T) {
	ctx := context.Background()
	toSvc := newTransferOrderService()
	item := createTestStockItem(t, ctx, tenantA)

	t.Run("requesting branch may submit", func(t *testing.T) {
		bto := newDraftBTOForAuthz(t, ctx, toSvc, item.ID)
		updated, err := toSvc.Submit(ctx, tenantA, branchPrincipal(branchReq), bto.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.BTOStatusSubmitted, updated.Status)
	})

	t.Run("source branch is forbidden from submitting", func(t *testing.T) {
		bto := newDraftBTOForAuthz(t, ctx, toSvc, item.ID)
		_, err := toSvc.Submit(ctx, tenantA, branchPrincipal(branchSrc), bto.ID)
		assert.ErrorIs(t, err, pub.ErrBranchForbidden)
	})

	t.Run("manager (tenant scope) is exempt", func(t *testing.T) {
		bto := newDraftBTOForAuthz(t, ctx, toSvc, item.ID)
		updated, err := toSvc.Submit(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), bto.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.BTOStatusSubmitted, updated.Status)
	})
}

func newSubmittedBTOForAuthz(t *testing.T, ctx context.Context, toSvc *service.TransferOrderService, itemID uuid.UUID) domain.BranchTransferOrder {
	t.Helper()
	bto := newDraftBTOForAuthz(t, ctx, toSvc, itemID)
	bto, err := toSvc.Submit(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), bto.ID)
	require.NoError(t, err)
	return bto
}

func TestTransferOrderAuthz_Approve(t *testing.T) {
	ctx := context.Background()
	toSvc := newTransferOrderService()
	item := createTestStockItem(t, ctx, tenantA)
	approvals := []service.ApprovalItem{{StockItemID: item.ID, ApprovedQty: 5}}

	t.Run("source branch may approve", func(t *testing.T) {
		bto := newSubmittedBTOForAuthz(t, ctx, toSvc, item.ID)
		updated, err := toSvc.Approve(ctx, tenantA, branchPrincipal(branchSrc), bto.ID, uuid.New(), approvals)
		require.NoError(t, err)
		assert.Equal(t, domain.BTOStatusApproved, updated.Status)
	})

	t.Run("requesting branch is forbidden from approving", func(t *testing.T) {
		bto := newSubmittedBTOForAuthz(t, ctx, toSvc, item.ID)
		_, err := toSvc.Approve(ctx, tenantA, branchPrincipal(branchReq), bto.ID, uuid.New(), approvals)
		assert.ErrorIs(t, err, pub.ErrBranchForbidden)
	})

	t.Run("manager (tenant scope) is exempt", func(t *testing.T) {
		bto := newSubmittedBTOForAuthz(t, ctx, toSvc, item.ID)
		updated, err := toSvc.Approve(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), bto.ID, uuid.New(), approvals)
		require.NoError(t, err)
		assert.Equal(t, domain.BTOStatusApproved, updated.Status)
	})
}

func TestTransferOrderAuthz_Reject(t *testing.T) {
	ctx := context.Background()
	toSvc := newTransferOrderService()
	item := createTestStockItem(t, ctx, tenantA)

	t.Run("source branch may reject", func(t *testing.T) {
		bto := newSubmittedBTOForAuthz(t, ctx, toSvc, item.ID)
		updated, err := toSvc.Reject(ctx, tenantA, branchPrincipal(branchSrc), bto.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.BTOStatusRejected, updated.Status)
	})

	t.Run("requesting branch is forbidden from rejecting", func(t *testing.T) {
		bto := newSubmittedBTOForAuthz(t, ctx, toSvc, item.ID)
		_, err := toSvc.Reject(ctx, tenantA, branchPrincipal(branchReq), bto.ID)
		assert.ErrorIs(t, err, pub.ErrBranchForbidden)
	})

	t.Run("manager (tenant scope) is exempt", func(t *testing.T) {
		bto := newSubmittedBTOForAuthz(t, ctx, toSvc, item.ID)
		updated, err := toSvc.Reject(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), bto.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.BTOStatusRejected, updated.Status)
	})
}

func newApprovedBTOForAuthz(t *testing.T, ctx context.Context, toSvc *service.TransferOrderService, itemID uuid.UUID) domain.BranchTransferOrder {
	t.Helper()
	bto := newSubmittedBTOForAuthz(t, ctx, toSvc, itemID)
	bto, err := toSvc.Approve(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), bto.ID, uuid.New(), []service.ApprovalItem{
		{StockItemID: itemID, ApprovedQty: 5},
	})
	require.NoError(t, err)
	return bto
}

func TestTransferOrderAuthz_Fulfil(t *testing.T) {
	ctx := context.Background()
	toSvc := newTransferOrderService()
	item := createTestStockItem(t, ctx, tenantA)

	t.Run("source branch may fulfil", func(t *testing.T) {
		bto := newApprovedBTOForAuthz(t, ctx, toSvc, item.ID)
		updated, err := toSvc.Fulfil(ctx, tenantA, branchPrincipal(branchSrc), bto.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.BTOStatusFulfilling, updated.Status)
	})

	t.Run("requesting branch is forbidden from fulfilling", func(t *testing.T) {
		bto := newApprovedBTOForAuthz(t, ctx, toSvc, item.ID)
		_, err := toSvc.Fulfil(ctx, tenantA, branchPrincipal(branchReq), bto.ID)
		assert.ErrorIs(t, err, pub.ErrBranchForbidden)
	})

	t.Run("manager (tenant scope) is exempt", func(t *testing.T) {
		bto := newApprovedBTOForAuthz(t, ctx, toSvc, item.ID)
		updated, err := toSvc.Fulfil(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), bto.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.BTOStatusFulfilling, updated.Status)
	})
}

// TestTransferOrderAuthz_Cancel encodes the status-aware ownership rule from
// ADR-DATA-006's state table (draft/submitted -> requesting_branch,
// approved -> source_branch), ratified in the 2026-07-04 sprint review over
// the flat "requesting-only" simplification. If the rule ever changes, only the
// "approved: ..." subtests below need to flip expectations (the production
// code's `branchOf` selection in TransferOrderService.Cancel would also
// change to always use requestingBranchOf).
func TestTransferOrderAuthz_Cancel(t *testing.T) {
	ctx := context.Background()
	toSvc := newTransferOrderService()
	item := createTestStockItem(t, ctx, tenantA)

	t.Run("draft: requesting branch may cancel", func(t *testing.T) {
		bto := newDraftBTOForAuthz(t, ctx, toSvc, item.ID)
		updated, err := toSvc.Cancel(ctx, tenantA, branchPrincipal(branchReq), bto.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.BTOStatusCancelled, updated.Status)
	})

	t.Run("draft: source branch is forbidden from cancelling", func(t *testing.T) {
		bto := newDraftBTOForAuthz(t, ctx, toSvc, item.ID)
		_, err := toSvc.Cancel(ctx, tenantA, branchPrincipal(branchSrc), bto.ID)
		assert.ErrorIs(t, err, pub.ErrBranchForbidden)
	})

	t.Run("submitted: requesting branch may cancel (geri çek)", func(t *testing.T) {
		bto := newSubmittedBTOForAuthz(t, ctx, toSvc, item.ID)
		updated, err := toSvc.Cancel(ctx, tenantA, branchPrincipal(branchReq), bto.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.BTOStatusCancelled, updated.Status)
	})

	t.Run("approved: source branch may cancel (ADR-DATA-006, henüz sevk yok)", func(t *testing.T) {
		bto := newApprovedBTOForAuthz(t, ctx, toSvc, item.ID)
		updated, err := toSvc.Cancel(ctx, tenantA, branchPrincipal(branchSrc), bto.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.BTOStatusCancelled, updated.Status)
	})

	t.Run("approved: requesting branch is forbidden from cancelling", func(t *testing.T) {
		bto := newApprovedBTOForAuthz(t, ctx, toSvc, item.ID)
		_, err := toSvc.Cancel(ctx, tenantA, branchPrincipal(branchReq), bto.ID)
		assert.ErrorIs(t, err, pub.ErrBranchForbidden)
	})

	t.Run("manager (tenant scope) is exempt regardless of status", func(t *testing.T) {
		bto := newApprovedBTOForAuthz(t, ctx, toSvc, item.ID)
		updated, err := toSvc.Cancel(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), bto.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.BTOStatusCancelled, updated.Status)
	})
}

// ---------------------------------------------------------------------------
// Shipment authz
// ---------------------------------------------------------------------------

func TestShipmentAuthz_Create(t *testing.T) {
	ctx := context.Background()
	sourceWH := createTestWarehouse(t, ctx, tenantA, branchSrc)
	item := createTestStockItem(t, ctx, tenantA)
	shSvc := newShipmentService()

	newReq := func() service.CreateShipmentRequest {
		return service.CreateShipmentRequest{
			FromWarehouseID: sourceWH.ID,
			ToBranchID:      branchReq,
			Items:           []service.CreateShipmentItemRequest{{StockItemID: item.ID, RequestedQty: 1, Unit: "kg"}},
		}
	}

	t.Run("from_warehouse's branch may create", func(t *testing.T) {
		_, _, err := shSvc.Create(ctx, tenantA, branchPrincipal(branchSrc), newReq())
		require.NoError(t, err)
	})

	t.Run("foreign branch is forbidden from creating", func(t *testing.T) {
		_, _, err := shSvc.Create(ctx, tenantA, branchPrincipal(branchReq), newReq())
		assert.ErrorIs(t, err, pub.ErrBranchForbidden)
	})

	t.Run("manager (tenant scope) is exempt", func(t *testing.T) {
		_, _, err := shSvc.Create(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), newReq())
		require.NoError(t, err)
	})
}

func newDraftShipmentForAuthz(t *testing.T, ctx context.Context, shSvc *service.ShipmentService, whID, itemID uuid.UUID) domain.Shipment {
	t.Helper()
	sh, _, err := shSvc.Create(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), service.CreateShipmentRequest{
		FromWarehouseID: whID,
		ToBranchID:      branchReq,
		Items:           []service.CreateShipmentItemRequest{{StockItemID: itemID, RequestedQty: 10, Unit: "kg"}},
	})
	require.NoError(t, err)
	return sh
}

func TestShipmentAuthz_Approve(t *testing.T) {
	ctx := context.Background()
	sourceWH := createTestWarehouse(t, ctx, tenantA, branchSrc)
	item := createTestStockItem(t, ctx, tenantA)
	shSvc := newShipmentService()

	t.Run("from_warehouse's branch may approve", func(t *testing.T) {
		sh := newDraftShipmentForAuthz(t, ctx, shSvc, sourceWH.ID, item.ID)
		updated, err := shSvc.Approve(ctx, tenantA, branchPrincipal(branchSrc), sh.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.ShipmentStatusApproved, updated.Status)
	})

	t.Run("foreign branch is forbidden from approving", func(t *testing.T) {
		sh := newDraftShipmentForAuthz(t, ctx, shSvc, sourceWH.ID, item.ID)
		_, err := shSvc.Approve(ctx, tenantA, branchPrincipal(branchReq), sh.ID)
		assert.ErrorIs(t, err, pub.ErrBranchForbidden)
	})

	t.Run("manager (tenant scope) is exempt", func(t *testing.T) {
		sh := newDraftShipmentForAuthz(t, ctx, shSvc, sourceWH.ID, item.ID)
		updated, err := shSvc.Approve(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), sh.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.ShipmentStatusApproved, updated.Status)
	})
}

func TestShipmentAuthz_Advance(t *testing.T) {
	ctx := context.Background()
	item := createTestStockItem(t, ctx, tenantA)
	shSvc := newShipmentService()

	newApprovedShipment := func() domain.Shipment {
		wh := createTestWarehouse(t, ctx, tenantA, branchSrc)
		seedSourceStock(t, ctx, tenantA, wh.ID, item.ID, 100, "kg")
		sh := newDraftShipmentForAuthz(t, ctx, shSvc, wh.ID, item.ID)
		sh, err := shSvc.Approve(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), sh.ID)
		require.NoError(t, err)
		return sh
	}

	t.Run("from_warehouse's branch may advance", func(t *testing.T) {
		sh := newApprovedShipment()
		updated, err := shSvc.Advance(ctx, tenantA, branchPrincipal(branchSrc), sh.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.ShipmentStatusInTransit, updated.Status)
	})

	t.Run("foreign branch is forbidden from advancing", func(t *testing.T) {
		sh := newApprovedShipment()
		_, err := shSvc.Advance(ctx, tenantA, branchPrincipal(branchReq), sh.ID)
		assert.ErrorIs(t, err, pub.ErrBranchForbidden)
	})

	t.Run("manager (tenant scope) is exempt", func(t *testing.T) {
		sh := newApprovedShipment()
		updated, err := shSvc.Advance(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), sh.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.ShipmentStatusInTransit, updated.Status)
	})
}

func TestShipmentAuthz_Cancel(t *testing.T) {
	ctx := context.Background()
	sourceWH := createTestWarehouse(t, ctx, tenantA, branchSrc)
	item := createTestStockItem(t, ctx, tenantA)
	shSvc := newShipmentService()

	t.Run("from_warehouse's branch may cancel", func(t *testing.T) {
		sh := newDraftShipmentForAuthz(t, ctx, shSvc, sourceWH.ID, item.ID)
		updated, err := shSvc.Cancel(ctx, tenantA, branchPrincipal(branchSrc), sh.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.ShipmentStatusCancelled, updated.Status)
	})

	t.Run("foreign branch is forbidden from cancelling", func(t *testing.T) {
		sh := newDraftShipmentForAuthz(t, ctx, shSvc, sourceWH.ID, item.ID)
		_, err := shSvc.Cancel(ctx, tenantA, branchPrincipal(branchReq), sh.ID)
		assert.ErrorIs(t, err, pub.ErrBranchForbidden)
	})

	t.Run("manager (tenant scope) is exempt", func(t *testing.T) {
		sh := newDraftShipmentForAuthz(t, ctx, shSvc, sourceWH.ID, item.ID)
		updated, err := shSvc.Cancel(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), sh.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.ShipmentStatusCancelled, updated.Status)
	})
}

func TestShipmentAuthz_Receive(t *testing.T) {
	ctx := context.Background()
	item := createTestStockItem(t, ctx, tenantA)
	shSvc := newShipmentService()

	newInTransitShipment := func() (domain.Shipment, domain.Warehouse) {
		sourceWH := createTestWarehouse(t, ctx, tenantA, branchSrc)
		destWH := createTestWarehouse(t, ctx, tenantA, branchReq)
		seedSourceStock(t, ctx, tenantA, sourceWH.ID, item.ID, 100, "kg")
		sh := newDraftShipmentForAuthz(t, ctx, shSvc, sourceWH.ID, item.ID)
		sh, err := shSvc.Approve(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), sh.ID)
		require.NoError(t, err)
		sh, err = shSvc.Advance(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), sh.ID)
		require.NoError(t, err)
		return sh, destWH
	}

	t.Run("destination warehouse's branch may receive", func(t *testing.T) {
		sh, destWH := newInTransitShipment()
		updated, err := shSvc.Receive(ctx, tenantA, branchPrincipal(branchReq), sh.ID, destWH.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.ShipmentStatusReceived, updated.Status)
	})

	t.Run("source branch (not destination) is forbidden from receiving", func(t *testing.T) {
		sh, destWH := newInTransitShipment()
		_, err := shSvc.Receive(ctx, tenantA, branchPrincipal(branchSrc), sh.ID, destWH.ID)
		assert.ErrorIs(t, err, pub.ErrBranchForbidden)
	})

	t.Run("manager (tenant scope) is exempt", func(t *testing.T) {
		sh, destWH := newInTransitShipment()
		updated, err := shSvc.Receive(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), sh.ID, destWH.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.ShipmentStatusReceived, updated.Status)
	})
}

// ---------------------------------------------------------------------------
// Purchase receipt authz
// ---------------------------------------------------------------------------

// TestPurchaseReceiptAuthz_CreateReceipt proves ADR-DATA-007 karar 3's
// branch-scope rule: a purchase receipt is authorized off its
// warehouse_id's branch (requireBranch), exactly like ShipmentService.Create
// off from_warehouse_id.
func TestPurchaseReceiptAuthz_CreateReceipt(t *testing.T) {
	ctx := context.Background()
	wh := createTestWarehouse(t, ctx, tenantA, branchSrc)
	item := createTestStockItem(t, ctx, tenantA)
	rcptSvc := newPurchaseReceiptService()

	policySvc := newSupplyPolicyService()
	_, err := policySvc.Create(ctx, tenantA, service.CreateSupplyPolicyRequest{
		Scope: domain.SupplyScopeStockItem, StockItemID: &item.ID, Mode: domain.SupplyModeFree,
	})
	require.NoError(t, err)

	newReq := func() service.CreateReceiptRequest {
		return service.CreateReceiptRequest{
			WarehouseID: wh.ID,
			Currency:    "TRY",
			Items:       []service.CreateReceiptItemRequest{{StockItemID: item.ID, Quantity: 1, Unit: "kg", UnitPrice: 5}},
		}
	}

	t.Run("warehouse's branch may create a receipt", func(t *testing.T) {
		_, _, err := rcptSvc.CreateReceipt(ctx, tenantA, branchPrincipal(branchSrc), newReq())
		require.NoError(t, err)
	})

	t.Run("foreign branch is forbidden from creating a receipt", func(t *testing.T) {
		_, _, err := rcptSvc.CreateReceipt(ctx, tenantA, branchPrincipal(branchReq), newReq())
		assert.ErrorIs(t, err, pub.ErrBranchForbidden)
	})

	t.Run("manager (tenant scope) is exempt", func(t *testing.T) {
		_, _, err := rcptSvc.CreateReceipt(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), newReq())
		require.NoError(t, err)
	})
}

// ---------------------------------------------------------------------------
// Stock movement authz
// ---------------------------------------------------------------------------

func TestInventoryAuthz_RecordMovement(t *testing.T) {
	ctx := context.Background()
	wh := createTestWarehouse(t, ctx, tenantA, branchSrc)
	item := createTestStockItem(t, ctx, tenantA)
	inv := newInventoryService()

	newReq := func() service.RecordMovementRequest {
		return service.RecordMovementRequest{WarehouseID: wh.ID, StockItemID: item.ID, Type: domain.MovementTypeIn, Quantity: 5, Unit: "kg"}
	}

	t.Run("warehouse's branch may record a movement", func(t *testing.T) {
		_, _, err := inv.RecordMovement(ctx, tenantA, branchPrincipal(branchSrc), newReq())
		require.NoError(t, err)
	})

	t.Run("foreign branch is forbidden from recording a movement", func(t *testing.T) {
		_, _, err := inv.RecordMovement(ctx, tenantA, branchPrincipal(branchReq), newReq())
		assert.ErrorIs(t, err, pub.ErrBranchForbidden)
	})

	t.Run("manager (tenant scope) is exempt", func(t *testing.T) {
		_, _, err := inv.RecordMovement(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), newReq())
		require.NoError(t, err)
	})
}

// ---------------------------------------------------------------------------
// Warehouse authz
// ---------------------------------------------------------------------------

func TestWarehouseAuthz_Update(t *testing.T) {
	ctx := context.Background()
	whSvc := newWarehouseService()

	newUpdateReq := func(whID uuid.UUID) service.UpdateWarehouseRequest {
		return service.UpdateWarehouseRequest{ID: whID, BranchID: branchSrc, Name: "Depo Updated", WarehouseType: domain.WarehouseTypeDepo, IsActive: true}
	}

	t.Run("own branch may update", func(t *testing.T) {
		wh := createTestWarehouse(t, ctx, tenantA, branchSrc)
		updated, err := whSvc.Update(ctx, tenantA, branchPrincipal(branchSrc), newUpdateReq(wh.ID))
		require.NoError(t, err)
		assert.Equal(t, "Depo Updated", updated.Name)
	})

	t.Run("foreign branch is forbidden, even claiming the owning branch_id in the body", func(t *testing.T) {
		// Authorization must be based on the PERSISTED warehouse's branch,
		// not req.BranchID (which the caller controls and which here
		// truthfully names the warehouse's real branch — the forbidden
		// party is branchReq, which the principal actually belongs to).
		wh := createTestWarehouse(t, ctx, tenantA, branchSrc)
		_, err := whSvc.Update(ctx, tenantA, branchPrincipal(branchReq), newUpdateReq(wh.ID))
		assert.ErrorIs(t, err, pub.ErrBranchForbidden)
	})

	t.Run("manager (tenant scope) is exempt", func(t *testing.T) {
		wh := createTestWarehouse(t, ctx, tenantA, branchSrc)
		updated, err := whSvc.Update(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), newUpdateReq(wh.ID))
		require.NoError(t, err)
		assert.Equal(t, "Depo Updated", updated.Name)
	})
}

func TestWarehouseAuthz_Delete(t *testing.T) {
	ctx := context.Background()
	whSvc := newWarehouseService()

	t.Run("own branch may delete", func(t *testing.T) {
		wh := createTestWarehouse(t, ctx, tenantA, branchSrc)
		err := whSvc.Delete(ctx, tenantA, branchPrincipal(branchSrc), wh.ID)
		require.NoError(t, err)
	})

	t.Run("foreign branch is forbidden from deleting", func(t *testing.T) {
		wh := createTestWarehouse(t, ctx, tenantA, branchSrc)
		err := whSvc.Delete(ctx, tenantA, branchPrincipal(branchReq), wh.ID)
		assert.ErrorIs(t, err, pub.ErrBranchForbidden)
	})

	t.Run("manager (tenant scope) is exempt", func(t *testing.T) {
		wh := createTestWarehouse(t, ctx, tenantA, branchSrc)
		err := whSvc.Delete(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), wh.ID)
		require.NoError(t, err)
	})
}
