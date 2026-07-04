package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/fx"
	"go.uber.org/zap"

	"onlinemenu.tr/internal/modules/inventory/domain"
	pub "onlinemenu.tr/internal/modules/inventory/public"
	"onlinemenu.tr/internal/modules/inventory/repo"
	"onlinemenu.tr/internal/platform/auth"
	"onlinemenu.tr/internal/platform/db"
)

// purchaseReceiptReferenceType tags stock_movements created by a purchase
// receipt (ADR-DATA-007 karar 3), mirroring shipment_service.go's
// refType := "shipment" convention.
const purchaseReceiptReferenceType = "purchase_receipt"

// PurchaseReceiptService creates elden fiş / faturasız alım belgeleri
// (ADR-DATA-007 karar 3) and, in the same atomic transaction: enforces the
// effective supply policy on every line (exclusive_hq is rejected outright;
// approved_suppliers requires the receipt's supplier to be on the item's
// approved list), records the stock movement (in) + on-hand adjustment, and
// establishes the branch-local cost (stock_levels.last_unit_cost,
// source=purchase_receipt).
//
// It depends on pub.SupplyPolicyResolver (not SupplyPolicyService directly):
// accept-interfaces keeps this service decoupled from supply policy's
// persistence/resolution internals, matching how ShipmentService depends
// only on repo.* types it owns and never reaches into another service.
type PurchaseReceiptService struct {
	db       *db.Pool
	repo     *repo.PurchaseReceiptRepo
	itemRepo *repo.PurchaseReceiptItemRepo
	lvlRepo  *repo.StockLevelRepo
	mvRepo   *repo.StockMovementRepo
	whRepo   *repo.WarehouseRepo
	resolver pub.SupplyPolicyResolver
	logger   *zap.Logger
}

// PurchaseReceiptParams groups fx-injected dependencies for
// NewPurchaseReceiptService.
type PurchaseReceiptParams struct {
	fx.In

	DB       *db.Pool
	Repo     *repo.PurchaseReceiptRepo
	ItemRepo *repo.PurchaseReceiptItemRepo
	LvlRepo  *repo.StockLevelRepo
	MvRepo   *repo.StockMovementRepo
	WhRepo   *repo.WarehouseRepo
	Resolver pub.SupplyPolicyResolver
	Logger   *zap.Logger
}

// NewPurchaseReceiptService constructs a PurchaseReceiptService for fx injection.
func NewPurchaseReceiptService(p PurchaseReceiptParams) *PurchaseReceiptService {
	return &PurchaseReceiptService{
		db:       p.DB,
		repo:     p.Repo,
		itemRepo: p.ItemRepo,
		lvlRepo:  p.LvlRepo,
		mvRepo:   p.MvRepo,
		whRepo:   p.WhRepo,
		resolver: p.Resolver,
		logger:   p.Logger,
	}
}

// CreateReceiptItemRequest is one purchase receipt line item. LineTotal
// defaults to Quantity*UnitPrice when zero, so callers need only supply it
// when the declared line amount deliberately differs (rounding, a bundled
// discount on the physical receipt).
type CreateReceiptItemRequest struct {
	StockItemID uuid.UUID
	Quantity    float64
	Unit        string
	UnitPrice   float64
	LineTotal   float64
	Brand       string
}

// CreateReceiptRequest carries the parameters for creating a purchase
// receipt. SupplierPartyID is the receipt-wide supplier (a single elden fiş
// is one physical purchase from one supplier/pazar stall); it may be nil —
// see ErrSupplyPolicyViolation for when that is rejected. Total defaults to
// the sum of the line items' LineTotal when zero.
type CreateReceiptRequest struct {
	WarehouseID     uuid.UUID
	SupplierPartyID *uuid.UUID
	SupplierName    string
	ReceiptNo       string
	ReceiptDate     time.Time
	Total           float64
	Currency        string
	Note            string
	CreatedBy       *uuid.UUID
	Items           []CreateReceiptItemRequest
}

// CreateReceipt persists a new purchase receipt with its line items,
// enforces the effective supply policy per line, and records the resulting
// stock movement/level/cost changes — all in a single WithTenantTx. If any
// line violates its item's supply policy, the entire receipt (and every
// movement it would have produced) is rejected: nothing is written.
func (s *PurchaseReceiptService) CreateReceipt(ctx context.Context, tenantID uuid.UUID, principal auth.Principal, req CreateReceiptRequest) (domain.PurchaseReceipt, []domain.PurchaseReceiptItem, error) {
	if err := validateCreateReceipt(req); err != nil {
		return domain.PurchaseReceipt{}, nil, err
	}

	receiptID, err := uuid.NewV7()
	if err != nil {
		return domain.PurchaseReceipt{}, nil, fmt.Errorf("inventory/service: create purchase receipt: generate id: %w", err)
	}

	currency := req.Currency
	if currency == "" {
		currency = "TRY"
	}
	receiptDate := req.ReceiptDate
	if receiptDate.IsZero() {
		receiptDate = time.Now()
	}
	total := req.Total
	if total == 0 {
		for _, it := range req.Items {
			total += lineTotalOf(it)
		}
	}

	var receipt domain.PurchaseReceipt
	var items []domain.PurchaseReceiptItem
	err = s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		wh, err := s.whRepo.GetByID(ctx, tx, req.WarehouseID)
		if err != nil {
			return err
		}
		if err := requireBranch(ctx, principal, wh.BranchID); err != nil {
			return err
		}

		// Supply policy enforcement (ADR-DATA-007 karar 3): every line is
		// checked BEFORE any write, so a single violating line rejects the
		// whole receipt atomically — no partial receipt/movement is ever
		// persisted.
		for _, it := range req.Items {
			if err := s.enforceSupplyPolicy(ctx, tenantID, wh.BranchID, req.SupplierPartyID, it.StockItemID); err != nil {
				return err
			}
		}

		receipt, err = s.repo.Create(ctx, tx, domain.PurchaseReceipt{
			ID:              receiptID,
			TenantID:        tenantID,
			WarehouseID:     req.WarehouseID,
			SupplierPartyID: req.SupplierPartyID,
			SupplierName:    req.SupplierName,
			ReceiptNo:       req.ReceiptNo,
			ReceiptDate:     receiptDate,
			Total:           total,
			Currency:        currency,
			Note:            req.Note,
			CreatedBy:       req.CreatedBy,
		})
		if err != nil {
			return fmt.Errorf("create purchase receipt: %w", err)
		}

		refType := purchaseReceiptReferenceType
		for _, it := range req.Items {
			itemID, err := uuid.NewV7()
			if err != nil {
				return fmt.Errorf("generate purchase receipt item id: %w", err)
			}
			created, err := s.itemRepo.Add(ctx, tx, domain.PurchaseReceiptItem{
				ID:          itemID,
				TenantID:    tenantID,
				ReceiptID:   receipt.ID,
				StockItemID: it.StockItemID,
				Quantity:    it.Quantity,
				Unit:        it.Unit,
				UnitPrice:   it.UnitPrice,
				LineTotal:   lineTotalOf(it),
				Brand:       it.Brand,
			})
			if err != nil {
				return fmt.Errorf("add purchase receipt item: %w", err)
			}
			items = append(items, created)

			if _, err := s.mvRepo.Create(ctx, tx, domain.StockMovement{
				TenantID:      tenantID,
				WarehouseID:   req.WarehouseID,
				StockItemID:   it.StockItemID,
				Type:          domain.MovementTypeIn,
				Quantity:      it.Quantity,
				ReferenceID:   &receipt.ID,
				ReferenceType: &refType,
			}); err != nil {
				return fmt.Errorf("record in movement: %w", err)
			}
			if _, err := s.lvlRepo.AdjustOnHand(ctx, tx, tenantID, req.WarehouseID, it.StockItemID,
				signedDelta(domain.MovementTypeIn, it.Quantity), it.Unit); err != nil {
				return fmt.Errorf("adjust level: %w", err)
			}
			if err := s.lvlRepo.SetLastCost(ctx, tx, tenantID, req.WarehouseID, it.StockItemID,
				it.UnitPrice, currency, domain.CostSourcePurchaseReceipt); err != nil {
				return fmt.Errorf("set last cost: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return domain.PurchaseReceipt{}, nil, wrapErr(err, "inventory/service: create purchase receipt: %w")
	}
	return receipt, items, nil
}

// enforceSupplyPolicy resolves the effective supply policy for stockItemID
// at branchID and checks it against the receipt's supplier
// (ADR-DATA-007 karar 3):
//
//   - exclusive_hq: always rejected — the item may only be sourced via a
//     branch transfer order, never a local purchase.
//   - approved_suppliers: supplierPartyID must be set AND present in the
//     resolved approved list; a nil supplier is rejected exactly like an
//     unapproved one (no "assume approved" special case for anonymous
//     purchases of an approved_suppliers-gated item).
//
// NOTE (module boundary vs. connection pool): resolver.EffectivePolicyFor is
// the pub.SupplyPolicyResolver contract (Wave A's API) — its implementation
// opens its OWN WithTenantReadTx, a second pool connection, distinct from
// the WithTenantTx write transaction CreateReceipt is already inside when
// this is called. This is correct per the module-boundary rule (a consumer
// module reaches another module's data only through its public interface,
// never its repo), but it does mean CreateReceipt briefly holds two pool
// connections per call. Under pool exhaustion (many concurrent receipts each
// holding a write connection while trying to acquire a read connection) this
// can stall waiting for a free connection; it cannot deadlock outright
// (WithTenantReadTx uses a plain, non-blocking-on-itself acquire), but it is
// a latency/throughput coupling worth knowing about before raising
// concurrent receipt volume. Left as-is (see the sprint report) rather than
// widened here, since resolving it would mean either giving
// PurchaseReceiptService direct repo access (violating the boundary) or
// changing pub.SupplyPolicyResolver's contract (Wave A's API, out of this
// task's scope).
//   - free: no supplier constraint.
func (s *PurchaseReceiptService) enforceSupplyPolicy(ctx context.Context, tenantID, branchID uuid.UUID, supplierPartyID *uuid.UUID, stockItemID uuid.UUID) error {
	mode, approved, err := s.resolver.EffectivePolicyFor(ctx, tenantID, stockItemID, branchID)
	if err != nil {
		return fmt.Errorf("resolve supply policy for stock item %s: %w", stockItemID, err)
	}
	switch mode {
	case pub.SupplyModeExclusiveHQ:
		return &pub.ErrSupplyPolicyViolation{Msg: fmt.Sprintf(
			"stock item %s is exclusive_hq: it may only be sourced via a branch transfer order, not a purchase receipt", stockItemID)}
	case pub.SupplyModeApprovedSuppliers:
		if supplierPartyID == nil || !containsUUID(approved, *supplierPartyID) {
			return &pub.ErrSupplyPolicyViolation{Msg: fmt.Sprintf(
				"stock item %s requires an approved supplier; supplier_party_id is missing or not on the approved list", stockItemID)}
		}
	case pub.SupplyModeFree:
		// no supplier constraint
	default:
		return &pub.ErrSupplyPolicyViolation{Msg: fmt.Sprintf("stock item %s: unrecognised supply mode %q", stockItemID, mode)}
	}
	return nil
}

// Get returns a purchase receipt by id.
func (s *PurchaseReceiptService) Get(ctx context.Context, tenantID, id uuid.UUID) (domain.PurchaseReceipt, error) {
	var rcpt domain.PurchaseReceipt
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		rcpt, err = s.repo.GetByID(ctx, tx, id)
		return err
	})
	if err != nil {
		return domain.PurchaseReceipt{}, wrapErr(err, "inventory/service: get purchase receipt: %w")
	}
	return rcpt, nil
}

// ListItems returns the line items of a purchase receipt.
func (s *PurchaseReceiptService) ListItems(ctx context.Context, tenantID, id uuid.UUID) ([]domain.PurchaseReceiptItem, error) {
	var items []domain.PurchaseReceiptItem
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		items, err = s.itemRepo.ListByReceipt(ctx, tx, id)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("inventory/service: list purchase receipt items: %w", err)
	}
	return items, nil
}

// ListByWarehouse returns purchase receipts recorded against a warehouse.
func (s *PurchaseReceiptService) ListByWarehouse(ctx context.Context, tenantID, warehouseID uuid.UUID) ([]domain.PurchaseReceipt, error) {
	var out []domain.PurchaseReceipt
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		out, err = s.repo.ListByWarehouse(ctx, tx, warehouseID)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("inventory/service: list purchase receipts by warehouse: %w", err)
	}
	return out, nil
}

func lineTotalOf(it CreateReceiptItemRequest) float64 {
	if it.LineTotal != 0 {
		return it.LineTotal
	}
	return it.Quantity * it.UnitPrice
}

func containsUUID(ids []uuid.UUID, id uuid.UUID) bool {
	for _, existing := range ids {
		if existing == id {
			return true
		}
	}
	return false
}

func validateCreateReceipt(req CreateReceiptRequest) error {
	if req.WarehouseID == uuid.Nil {
		return &pub.ValidationError{Msg: "warehouse_id is required"}
	}
	if len(req.Items) == 0 {
		return &pub.ValidationError{Msg: "at least one item is required"}
	}
	for _, it := range req.Items {
		if it.StockItemID == uuid.Nil {
			return &pub.ValidationError{Msg: "item stock_item_id is required"}
		}
		if it.Quantity <= 0 {
			return &pub.ValidationError{Msg: "item quantity must be positive"}
		}
		if it.Unit == "" {
			return &pub.ValidationError{Msg: "item unit is required"}
		}
		if it.UnitPrice < 0 {
			return &pub.ValidationError{Msg: "item unit_price must not be negative"}
		}
		if it.LineTotal < 0 {
			return &pub.ValidationError{Msg: "item line_total must not be negative"}
		}
	}
	if req.Total < 0 {
		return &pub.ValidationError{Msg: "total must not be negative"}
	}
	return nil
}
