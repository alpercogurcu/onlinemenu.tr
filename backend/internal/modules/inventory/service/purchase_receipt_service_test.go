package service_test

// PurchaseReceiptService tests (ADR-DATA-007 karar 3): supply policy
// enforcement matrix, branch-local cost recording, and atomicity. These run
// against the shared testcontainers pool from integration_test.go's TestMain.

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"onlinemenu.tr/internal/modules/inventory/domain"
	pub "onlinemenu.tr/internal/modules/inventory/public"
	"onlinemenu.tr/internal/modules/inventory/service"
)

// ---------------------------------------------------------------------------
// Supply policy enforcement matrix
// ---------------------------------------------------------------------------

// TestPurchaseReceipt_PolicyEnforcement_ExclusiveHQ_Rejected proves an
// exclusive_hq item can never be sourced via a purchase receipt, regardless
// of whether a supplier is named.
func TestPurchaseReceipt_PolicyEnforcement_ExclusiveHQ_Rejected(t *testing.T) {
	ctx := context.Background()
	wh := createTestWarehouse(t, ctx, tenantA, branchSrc)
	item := createTestStockItem(t, ctx, tenantA)

	policySvc := newSupplyPolicyService()
	_, err := policySvc.Create(ctx, tenantA, service.CreateSupplyPolicyRequest{
		Scope: domain.SupplyScopeStockItem, StockItemID: &item.ID, Mode: domain.SupplyModeExclusiveHQ,
	})
	require.NoError(t, err)

	rcptSvc := newPurchaseReceiptService()
	_, _, err = rcptSvc.CreateReceipt(ctx, tenantA, chainWidePrincipal(), service.CreateReceiptRequest{
		WarehouseID: wh.ID,
		Currency:    "TRY",
		Items: []service.CreateReceiptItemRequest{
			{StockItemID: item.ID, Quantity: 5, Unit: "kg", UnitPrice: 10},
		},
	})
	var spv *pub.ErrSupplyPolicyViolation
	assert.ErrorAs(t, err, &spv, "exclusive_hq item must reject a purchase receipt line")
}

// TestPurchaseReceipt_PolicyEnforcement_ApprovedSuppliers_ApprovedSupplier_OK
// proves an approved_suppliers item may be purchased when the receipt's
// supplier_party_id is on the approved list.
func TestPurchaseReceipt_PolicyEnforcement_ApprovedSuppliers_ApprovedSupplier_OK(t *testing.T) {
	ctx := context.Background()
	wh := createTestWarehouse(t, ctx, tenantA, branchSrc)
	item := createTestStockItem(t, ctx, tenantA)
	supplierID := uuid.New()

	policySvc := newSupplyPolicyService()
	_, err := policySvc.Create(ctx, tenantA, service.CreateSupplyPolicyRequest{
		Scope: domain.SupplyScopeStockItem, StockItemID: &item.ID, Mode: domain.SupplyModeApprovedSuppliers,
		ApprovedSupplierIDs: []uuid.UUID{supplierID},
	})
	require.NoError(t, err)

	rcptSvc := newPurchaseReceiptService()
	rcpt, items, err := rcptSvc.CreateReceipt(ctx, tenantA, chainWidePrincipal(), service.CreateReceiptRequest{
		WarehouseID:     wh.ID,
		SupplierPartyID: &supplierID,
		Currency:        "TRY",
		Items: []service.CreateReceiptItemRequest{
			{StockItemID: item.ID, Quantity: 5, Unit: "kg", UnitPrice: 10},
		},
	})
	require.NoError(t, err)
	assert.NotEqual(t, uuid.Nil, rcpt.ID)
	require.Len(t, items, 1)
}

// TestPurchaseReceipt_PolicyEnforcement_ApprovedSuppliers_UnapprovedSupplier_Rejected
// proves a supplier NOT on the approved list is rejected, even though a
// supplier was named.
func TestPurchaseReceipt_PolicyEnforcement_ApprovedSuppliers_UnapprovedSupplier_Rejected(t *testing.T) {
	ctx := context.Background()
	wh := createTestWarehouse(t, ctx, tenantA, branchSrc)
	item := createTestStockItem(t, ctx, tenantA)
	approvedSupplierID := uuid.New()
	unapprovedSupplierID := uuid.New()

	policySvc := newSupplyPolicyService()
	_, err := policySvc.Create(ctx, tenantA, service.CreateSupplyPolicyRequest{
		Scope: domain.SupplyScopeStockItem, StockItemID: &item.ID, Mode: domain.SupplyModeApprovedSuppliers,
		ApprovedSupplierIDs: []uuid.UUID{approvedSupplierID},
	})
	require.NoError(t, err)

	rcptSvc := newPurchaseReceiptService()
	_, _, err = rcptSvc.CreateReceipt(ctx, tenantA, chainWidePrincipal(), service.CreateReceiptRequest{
		WarehouseID:     wh.ID,
		SupplierPartyID: &unapprovedSupplierID,
		Currency:        "TRY",
		Items: []service.CreateReceiptItemRequest{
			{StockItemID: item.ID, Quantity: 5, Unit: "kg", UnitPrice: 10},
		},
	})
	var spv *pub.ErrSupplyPolicyViolation
	assert.ErrorAs(t, err, &spv, "a supplier not on the approved list must be rejected")
}

// TestPurchaseReceipt_PolicyEnforcement_ApprovedSuppliers_NilSupplier_Rejected
// proves a nil supplier_party_id is rejected exactly like an unapproved one
// — there is no "assume approved" special case for an anonymous purchase of
// an approved_suppliers-gated item.
func TestPurchaseReceipt_PolicyEnforcement_ApprovedSuppliers_NilSupplier_Rejected(t *testing.T) {
	ctx := context.Background()
	wh := createTestWarehouse(t, ctx, tenantA, branchSrc)
	item := createTestStockItem(t, ctx, tenantA)

	policySvc := newSupplyPolicyService()
	_, err := policySvc.Create(ctx, tenantA, service.CreateSupplyPolicyRequest{
		Scope: domain.SupplyScopeStockItem, StockItemID: &item.ID, Mode: domain.SupplyModeApprovedSuppliers,
		ApprovedSupplierIDs: []uuid.UUID{uuid.New()},
	})
	require.NoError(t, err)

	rcptSvc := newPurchaseReceiptService()
	_, _, err = rcptSvc.CreateReceipt(ctx, tenantA, chainWidePrincipal(), service.CreateReceiptRequest{
		WarehouseID: wh.ID,
		Currency:    "TRY",
		Items: []service.CreateReceiptItemRequest{
			{StockItemID: item.ID, Quantity: 5, Unit: "kg", UnitPrice: 10},
		},
	})
	var spv *pub.ErrSupplyPolicyViolation
	assert.ErrorAs(t, err, &spv, "a nil supplier_party_id must be rejected for an approved_suppliers item")
}

// TestPurchaseReceipt_PolicyEnforcement_Free_OK proves a free-mode item may
// be purchased with no supplier at all.
func TestPurchaseReceipt_PolicyEnforcement_Free_OK(t *testing.T) {
	ctx := context.Background()
	wh := createTestWarehouse(t, ctx, tenantA, branchSrc)
	item := createTestStockItem(t, ctx, tenantA)

	policySvc := newSupplyPolicyService()
	_, err := policySvc.Create(ctx, tenantA, service.CreateSupplyPolicyRequest{
		Scope: domain.SupplyScopeStockItem, StockItemID: &item.ID, Mode: domain.SupplyModeFree,
	})
	require.NoError(t, err)

	rcptSvc := newPurchaseReceiptService()
	rcpt, items, err := rcptSvc.CreateReceipt(ctx, tenantA, chainWidePrincipal(), service.CreateReceiptRequest{
		WarehouseID:  wh.ID,
		SupplierName: "Semt Pazarı",
		Currency:     "TRY",
		Items: []service.CreateReceiptItemRequest{
			{StockItemID: item.ID, Quantity: 3, Unit: "kg", UnitPrice: 20},
		},
	})
	require.NoError(t, err)
	assert.NotEqual(t, uuid.Nil, rcpt.ID)
	require.Len(t, items, 1)
}

// ---------------------------------------------------------------------------
// Branch-local cost (ADR-DATA-007)
// ---------------------------------------------------------------------------

// TestPurchaseReceipt_SetsLastCostAndOnHand proves a successful receipt (a)
// increments stock_levels.on_hand and (b) records the branch-local cost with
// source=purchase_receipt.
func TestPurchaseReceipt_SetsLastCostAndOnHand(t *testing.T) {
	ctx := context.Background()
	wh := createTestWarehouse(t, ctx, tenantA, branchSrc)
	item := createTestStockItem(t, ctx, tenantA)

	policySvc := newSupplyPolicyService()
	_, err := policySvc.Create(ctx, tenantA, service.CreateSupplyPolicyRequest{
		Scope: domain.SupplyScopeStockItem, StockItemID: &item.ID, Mode: domain.SupplyModeFree,
	})
	require.NoError(t, err)

	rcptSvc := newPurchaseReceiptService()
	_, _, err = rcptSvc.CreateReceipt(ctx, tenantA, chainWidePrincipal(), service.CreateReceiptRequest{
		WarehouseID: wh.ID,
		Currency:    "TRY",
		Items: []service.CreateReceiptItemRequest{
			{StockItemID: item.ID, Quantity: 8, Unit: "kg", UnitPrice: 15.5},
		},
	})
	require.NoError(t, err)

	invSvc := newInventoryService()
	level, err := invSvc.GetLevel(ctx, tenantA, wh.ID, item.ID)
	require.NoError(t, err)
	assert.InDelta(t, 8.0, level.OnHand, 0.001, "on_hand must be incremented by the receipt quantity")
	require.NotNil(t, level.LastUnitCost)
	assert.InDelta(t, 15.5, *level.LastUnitCost, 0.001)
	require.NotNil(t, level.LastCostSource)
	assert.Equal(t, string(domain.CostSourcePurchaseReceipt), *level.LastCostSource)
	require.NotNil(t, level.LastCostCurrency)
	assert.Equal(t, "TRY", *level.LastCostCurrency)
}

// ---------------------------------------------------------------------------
// Atomicity
// ---------------------------------------------------------------------------

// TestPurchaseReceipt_Atomicity_OneViolatingLineRejectsWholeReceipt proves
// that when a multi-line receipt has one policy-violating line, NOTHING is
// persisted: no receipt row, no receipt items, no stock movement, no level
// change for either line — including the otherwise-valid free-mode line.
func TestPurchaseReceipt_Atomicity_OneViolatingLineRejectsWholeReceipt(t *testing.T) {
	ctx := context.Background()
	wh := createTestWarehouse(t, ctx, tenantA, branchSrc)
	freeItem := createTestStockItem(t, ctx, tenantA)
	exclusiveItem := createTestStockItem(t, ctx, tenantA)

	policySvc := newSupplyPolicyService()
	_, err := policySvc.Create(ctx, tenantA, service.CreateSupplyPolicyRequest{
		Scope: domain.SupplyScopeStockItem, StockItemID: &freeItem.ID, Mode: domain.SupplyModeFree,
	})
	require.NoError(t, err)
	_, err = policySvc.Create(ctx, tenantA, service.CreateSupplyPolicyRequest{
		Scope: domain.SupplyScopeStockItem, StockItemID: &exclusiveItem.ID, Mode: domain.SupplyModeExclusiveHQ,
	})
	require.NoError(t, err)

	rcptSvc := newPurchaseReceiptService()
	_, _, err = rcptSvc.CreateReceipt(ctx, tenantA, chainWidePrincipal(), service.CreateReceiptRequest{
		WarehouseID: wh.ID,
		Currency:    "TRY",
		Items: []service.CreateReceiptItemRequest{
			{StockItemID: freeItem.ID, Quantity: 5, Unit: "kg", UnitPrice: 10},
			{StockItemID: exclusiveItem.ID, Quantity: 5, Unit: "kg", UnitPrice: 10},
		},
	})
	var spv *pub.ErrSupplyPolicyViolation
	require.ErrorAs(t, err, &spv)

	receipts, err := rcptSvc.ListByWarehouse(ctx, tenantA, wh.ID)
	require.NoError(t, err)
	assert.Empty(t, receipts, "no purchase_receipts row must exist after a rejected receipt")

	invSvc := newInventoryService()
	_, err = invSvc.GetLevel(ctx, tenantA, wh.ID, freeItem.ID)
	assert.ErrorIs(t, err, pub.ErrNotFound, "the valid line's stock movement/level must NOT have been written either — the whole receipt is one transaction")
	_, err = invSvc.GetLevel(ctx, tenantA, wh.ID, exclusiveItem.ID)
	assert.ErrorIs(t, err, pub.ErrNotFound)
}

// ---------------------------------------------------------------------------
// Get / List
// ---------------------------------------------------------------------------

// TestPurchaseReceipt_GetAndListRoundTrip proves the basic persistence round
// trip through Get/ListItems/ListByWarehouse.
func TestPurchaseReceipt_GetAndListRoundTrip(t *testing.T) {
	ctx := context.Background()
	wh := createTestWarehouse(t, ctx, tenantA, branchSrc)
	item := createTestStockItem(t, ctx, tenantA)

	policySvc := newSupplyPolicyService()
	_, err := policySvc.Create(ctx, tenantA, service.CreateSupplyPolicyRequest{
		Scope: domain.SupplyScopeStockItem, StockItemID: &item.ID, Mode: domain.SupplyModeFree,
	})
	require.NoError(t, err)

	rcptSvc := newPurchaseReceiptService()
	created, _, err := rcptSvc.CreateReceipt(ctx, tenantA, chainWidePrincipal(), service.CreateReceiptRequest{
		WarehouseID:  wh.ID,
		SupplierName: "Semt Pazarı",
		Currency:     "TRY",
		Items: []service.CreateReceiptItemRequest{
			{StockItemID: item.ID, Quantity: 2, Unit: "kg", UnitPrice: 30},
		},
	})
	require.NoError(t, err)

	fetched, err := rcptSvc.Get(ctx, tenantA, created.ID)
	require.NoError(t, err)
	assert.Equal(t, created.ID, fetched.ID)
	assert.Equal(t, "Semt Pazarı", fetched.SupplierName)

	items, err := rcptSvc.ListItems(ctx, tenantA, created.ID)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.InDelta(t, 60.0, items[0].LineTotal, 0.001, "line_total must default to quantity*unit_price")

	list, err := rcptSvc.ListByWarehouse(ctx, tenantA, wh.ID)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, created.ID, list[0].ID)
}
