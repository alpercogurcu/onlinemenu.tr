package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/fx"
	"go.uber.org/zap"

	"onlinemenu.tr/internal/modules/inventory/domain"
	pub "onlinemenu.tr/internal/modules/inventory/public"
	"onlinemenu.tr/internal/modules/inventory/repo"
	"onlinemenu.tr/internal/platform/db"
)

// ShipmentService manages the physical fulfilment of stock shipments and is
// the SOLE writer of "received" for any linked BranchTransferOrder
// (ADR-DATA-006). It reaches directly into StockMovementRepo/StockLevelRepo
// (rather than going through InventoryService, which opens its own
// transaction) so that the status guard, the stock ledger writes and the
// BTO denormalization all commit atomically in one WithTenantTx.
type ShipmentService struct {
	db           *db.Pool
	repo         *repo.ShipmentRepo
	itemRepo     *repo.ShipmentItemRepo
	lvlRepo      *repo.StockLevelRepo
	mvRepo       *repo.StockMovementRepo
	transferRepo *repo.TransferOrderRepo
	transferItem *repo.TransferOrderItemRepo
	logger       *zap.Logger
}

// ShipmentParams groups fx-injected dependencies for NewShipmentService.
type ShipmentParams struct {
	fx.In

	DB           *db.Pool
	Repo         *repo.ShipmentRepo
	ItemRepo     *repo.ShipmentItemRepo
	LvlRepo      *repo.StockLevelRepo
	MvRepo       *repo.StockMovementRepo
	TransferRepo *repo.TransferOrderRepo
	TransferItem *repo.TransferOrderItemRepo
	Logger       *zap.Logger
}

// NewShipmentService constructs a ShipmentService for fx injection.
func NewShipmentService(p ShipmentParams) *ShipmentService {
	return &ShipmentService{
		db:           p.DB,
		repo:         p.Repo,
		itemRepo:     p.ItemRepo,
		lvlRepo:      p.LvlRepo,
		mvRepo:       p.MvRepo,
		transferRepo: p.TransferRepo,
		transferItem: p.TransferItem,
		logger:       p.Logger,
	}
}

// CreateShipmentItemRequest is one shipment line item.
type CreateShipmentItemRequest struct {
	StockItemID  uuid.UUID
	RequestedQty float64
	Unit         string
}

// CreateShipmentRequest carries the parameters for creating a shipment.
// TransferOrderID is optional: a shipment may exist standalone (ad-hoc
// restock) with no requesting BTO.
type CreateShipmentRequest struct {
	FromWarehouseID uuid.UUID
	ToBranchID      uuid.UUID
	TransferOrderID *uuid.UUID
	Priority        domain.Priority
	Note            string
	CreatedBy       *uuid.UUID
	Items           []CreateShipmentItemRequest
}

// Create persists a new shipment in draft status with its line items.
func (s *ShipmentService) Create(ctx context.Context, tenantID uuid.UUID, req CreateShipmentRequest) (domain.Shipment, []domain.ShipmentItem, error) {
	if req.Priority == "" {
		req.Priority = domain.PriorityNormal
	}
	if err := validateCreateShipment(req); err != nil {
		return domain.Shipment{}, nil, err
	}

	var shipment domain.Shipment
	var items []domain.ShipmentItem
	err := s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		shipment, err = s.repo.Create(ctx, tx, domain.Shipment{
			TenantID:        tenantID,
			FromWarehouseID: req.FromWarehouseID,
			ToBranchID:      req.ToBranchID,
			TransferOrderID: req.TransferOrderID,
			Status:          domain.ShipmentStatusDraft,
			Priority:        req.Priority,
			Note:            req.Note,
			CreatedBy:       req.CreatedBy,
		})
		if err != nil {
			return fmt.Errorf("create shipment: %w", err)
		}

		for _, it := range req.Items {
			created, err := s.itemRepo.Add(ctx, tx, domain.ShipmentItem{
				ShipmentID:   shipment.ID,
				TenantID:     tenantID,
				StockItemID:  it.StockItemID,
				RequestedQty: it.RequestedQty,
				Unit:         it.Unit,
			})
			if err != nil {
				return fmt.Errorf("add shipment item: %w", err)
			}
			items = append(items, created)
		}
		return nil
	})
	if err != nil {
		return domain.Shipment{}, nil, fmt.Errorf("inventory/service: create shipment: %w", err)
	}
	return shipment, items, nil
}

// Get returns a shipment by id.
func (s *ShipmentService) Get(ctx context.Context, tenantID, id uuid.UUID) (domain.Shipment, error) {
	var sh domain.Shipment
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		sh, err = s.repo.GetByID(ctx, tx, id)
		return err
	})
	if err != nil {
		return domain.Shipment{}, wrapErr(err, "inventory/service: get shipment: %w")
	}
	return sh, nil
}

// ListItems returns the line items of a shipment.
func (s *ShipmentService) ListItems(ctx context.Context, tenantID, id uuid.UUID) ([]domain.ShipmentItem, error) {
	var items []domain.ShipmentItem
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		items, err = s.itemRepo.ListByShipment(ctx, tx, id)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("inventory/service: list shipment items: %w", err)
	}
	return items, nil
}

// ListByWarehouse returns shipments originating from a warehouse.
func (s *ShipmentService) ListByWarehouse(ctx context.Context, tenantID, warehouseID uuid.UUID) ([]domain.Shipment, error) {
	var out []domain.Shipment
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		out, err = s.repo.ListByWarehouse(ctx, tx, warehouseID)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("inventory/service: list shipments by warehouse: %w", err)
	}
	return out, nil
}

// Approve transitions a shipment from draft to approved.
func (s *ShipmentService) Approve(ctx context.Context, tenantID, id uuid.UUID) (domain.Shipment, error) {
	var updated domain.Shipment
	err := s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		sh, err := s.repo.GetByID(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := domain.TransitionShipmentStatus(sh.Status, domain.ShipmentStatusApproved); err != nil {
			return &pub.TransitionError{Msg: err.Error()}
		}
		updated, err = s.repo.UpdateStatus(ctx, tx, id, domain.ShipmentStatusApproved, nil, nil)
		return err
	})
	if err != nil {
		return domain.Shipment{}, wrapErr(err, "inventory/service: approve shipment: %w")
	}
	return updated, nil
}

// Advance transitions a shipment from approved to in_transit: for each line
// item, the full requested_qty is shipped (recorded as an 'out' stock
// movement from the source warehouse and denormalized onto the linked BTO's
// shipped_qty, if any). This is a single atomic transaction.
func (s *ShipmentService) Advance(ctx context.Context, tenantID, id uuid.UUID) (domain.Shipment, error) {
	var updated domain.Shipment
	err := s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		sh, err := s.repo.GetByID(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := domain.TransitionShipmentStatus(sh.Status, domain.ShipmentStatusInTransit); err != nil {
			return &pub.TransitionError{Msg: err.Error()}
		}

		items, err := s.itemRepo.ListByShipment(ctx, tx, id)
		if err != nil {
			return fmt.Errorf("list shipment items: %w", err)
		}

		refType := "shipment"
		for _, item := range items {
			qty := item.RequestedQty

			// Availability guard: without this, AdjustOnHand's GREATEST(0, ...)
			// clamp would silently manufacture stock — the source clamps to 0
			// while shipped_qty/the destination still record the full qty,
			// creating units from nothing. Faz 2 may want a softer "allow
			// backorder" mode; Faz 1 rejects the advance outright.
			//
			// NOTE (known limitation, not fixed here): this SELECT is not
			// FOR UPDATE, so two concurrent Advance calls against the same
			// warehouse+stock_item can both read a passing OnHand and both
			// proceed — the row-level GREATEST(0, ...) clamp then serializes
			// the writes and can still net to a manufactured over-ship. This
			// guard closes the easy sequential case; row locking (mirroring
			// pos.CheckRepo.GetForUpdate) would be needed to close the
			// concurrent case — left for Faz 2.
			level, err := s.lvlRepo.GetByStockItem(ctx, tx, sh.FromWarehouseID, item.StockItemID)
			if err != nil && !errors.Is(err, repo.ErrNotFound) {
				return fmt.Errorf("check source level: %w", err)
			}
			if level.OnHand < qty {
				return &pub.ValidationError{Msg: fmt.Sprintf(
					"insufficient stock for item %s in source warehouse: requested %.3f, available %.3f",
					item.StockItemID, qty, level.OnHand)}
			}

			if _, err := s.mvRepo.Create(ctx, tx, domain.StockMovement{
				TenantID:      tenantID,
				WarehouseID:   sh.FromWarehouseID,
				StockItemID:   item.StockItemID,
				Type:          domain.MovementTypeOut,
				Quantity:      qty,
				ReferenceID:   &sh.ID,
				ReferenceType: &refType,
			}); err != nil {
				return fmt.Errorf("record out movement: %w", err)
			}
			if _, err := s.lvlRepo.AdjustOnHand(ctx, tx, tenantID, sh.FromWarehouseID, item.StockItemID,
				signedDelta(domain.MovementTypeOut, qty), item.Unit); err != nil {
				return fmt.Errorf("adjust source level: %w", err)
			}
			if err := s.itemRepo.SetShippedQty(ctx, tx, id, item.StockItemID, qty); err != nil {
				return fmt.Errorf("set shipped qty: %w", err)
			}
			if sh.TransferOrderID != nil {
				if err := s.transferItem.AddShippedQty(ctx, tx, *sh.TransferOrderID, item.StockItemID, qty); err != nil {
					return fmt.Errorf("denormalize shipped qty to transfer order: %w", err)
				}
			}
		}

		now := time.Now()
		updated, err = s.repo.UpdateStatus(ctx, tx, id, domain.ShipmentStatusInTransit, &now, nil)
		if err != nil {
			return fmt.Errorf("update shipment status: %w", err)
		}

		if sh.TransferOrderID != nil {
			if err := s.transitionLinkedBTO(ctx, tx, *sh.TransferOrderID, domain.BTOStatusShipped); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return domain.Shipment{}, wrapErr(err, "inventory/service: advance shipment: %w")
	}
	return updated, nil
}

// Receive transitions a shipment from in_transit to received: for each line
// item, the shipped_qty is received into toWarehouseID (recorded as an 'in'
// stock movement) and denormalized onto the linked BTO's received_qty. If
// every item of the linked BTO is now fully received, the BTO auto-closes
// (received -> closed). All of this — status guard, stock movements, BTO
// denormalization and auto-close — commits in a single WithTenantTx.
func (s *ShipmentService) Receive(ctx context.Context, tenantID, id, toWarehouseID uuid.UUID) (domain.Shipment, error) {
	if toWarehouseID == uuid.Nil {
		return domain.Shipment{}, &pub.ValidationError{Msg: "to_warehouse_id is required"}
	}

	var updated domain.Shipment
	err := s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		sh, err := s.repo.GetByID(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := domain.TransitionShipmentStatus(sh.Status, domain.ShipmentStatusReceived); err != nil {
			return &pub.TransitionError{Msg: err.Error()}
		}

		items, err := s.itemRepo.ListByShipment(ctx, tx, id)
		if err != nil {
			return fmt.Errorf("list shipment items: %w", err)
		}

		refType := "shipment"
		for _, item := range items {
			qty := item.ShippedQty
			if qty <= 0 {
				continue
			}
			if _, err := s.mvRepo.Create(ctx, tx, domain.StockMovement{
				TenantID:      tenantID,
				WarehouseID:   toWarehouseID,
				StockItemID:   item.StockItemID,
				Type:          domain.MovementTypeIn,
				Quantity:      qty,
				ReferenceID:   &sh.ID,
				ReferenceType: &refType,
			}); err != nil {
				return fmt.Errorf("record in movement: %w", err)
			}
			if _, err := s.lvlRepo.AdjustOnHand(ctx, tx, tenantID, toWarehouseID, item.StockItemID,
				signedDelta(domain.MovementTypeIn, qty), item.Unit); err != nil {
				return fmt.Errorf("adjust destination level: %w", err)
			}
			if err := s.itemRepo.SetReceivedQty(ctx, tx, id, item.StockItemID, qty); err != nil {
				return fmt.Errorf("set received qty: %w", err)
			}
			if sh.TransferOrderID != nil {
				if err := s.transferItem.AddReceivedQty(ctx, tx, *sh.TransferOrderID, item.StockItemID, qty); err != nil {
					return fmt.Errorf("denormalize received qty to transfer order: %w", err)
				}
			}
		}

		now := time.Now()
		updated, err = s.repo.UpdateStatus(ctx, tx, id, domain.ShipmentStatusReceived, nil, &now)
		if err != nil {
			return fmt.Errorf("update shipment status: %w", err)
		}

		if sh.TransferOrderID != nil {
			if err := s.transitionLinkedBTO(ctx, tx, *sh.TransferOrderID, domain.BTOStatusReceived); err != nil {
				return err
			}
			allReceived, err := s.transferItem.AllReceived(ctx, tx, *sh.TransferOrderID)
			if err != nil {
				return fmt.Errorf("check all received: %w", err)
			}
			if allReceived {
				if err := s.transitionLinkedBTO(ctx, tx, *sh.TransferOrderID, domain.BTOStatusClosed); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return domain.Shipment{}, wrapErr(err, "inventory/service: receive shipment: %w")
	}
	return updated, nil
}

// Cancel transitions a shipment to cancelled (only reachable from draft/approved).
func (s *ShipmentService) Cancel(ctx context.Context, tenantID, id uuid.UUID) (domain.Shipment, error) {
	var updated domain.Shipment
	err := s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		sh, err := s.repo.GetByID(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := domain.TransitionShipmentStatus(sh.Status, domain.ShipmentStatusCancelled); err != nil {
			return &pub.TransitionError{Msg: err.Error()}
		}
		updated, err = s.repo.UpdateStatus(ctx, tx, id, domain.ShipmentStatusCancelled, nil, nil)
		return err
	})
	if err != nil {
		return domain.Shipment{}, wrapErr(err, "inventory/service: cancel shipment: %w")
	}
	return updated, nil
}

// transitionLinkedBTO validates and persists a BTO status transition driven
// by a shipment event. This is the ONLY code path in the module allowed to
// move a BTO to shipped/received/closed (ADR-DATA-006 ownership rule) — no
// TransferOrderService method exposes these transitions to an HTTP caller.
// transitionLinkedBTO is idempotent with respect to the BTO already being at
// the target status: ADR-DATA-006 Faz 1 allows one BTO to be fulfilled by N
// shipments (partial fulfilment). The first shipment to advance/receive
// drives the BTO's shipped/received transition; subsequent shipments for the
// same BTO reaching the same shipment-side event must NOT be treated as an
// invalid re-transition — they simply have nothing left to push on the BTO
// (its shipped_qty/received_qty per-item denormalization still runs
// unconditionally in Advance/Receive, regardless of this no-op).
func (s *ShipmentService) transitionLinkedBTO(ctx context.Context, tx pgx.Tx, transferOrderID uuid.UUID, to domain.BTOStatus) error {
	order, err := s.transferRepo.GetByID(ctx, tx, transferOrderID)
	if err != nil {
		return fmt.Errorf("get linked transfer order: %w", err)
	}
	if order.Status == to {
		return nil
	}
	if err := domain.TransitionBTOStatus(order.Status, to); err != nil {
		return &pub.TransitionError{Msg: err.Error()}
	}
	if _, err := s.transferRepo.UpdateStatus(ctx, tx, transferOrderID, to, nil, nil, nil); err != nil {
		return fmt.Errorf("update linked transfer order status: %w", err)
	}
	return nil
}

func validateCreateShipment(req CreateShipmentRequest) error {
	if req.FromWarehouseID == uuid.Nil {
		return &pub.ValidationError{Msg: "from_warehouse_id is required"}
	}
	if req.ToBranchID == uuid.Nil {
		return &pub.ValidationError{Msg: "to_branch_id is required"}
	}
	if !req.Priority.Valid() {
		return &pub.ValidationError{Msg: fmt.Sprintf("invalid priority %q", req.Priority)}
	}
	if len(req.Items) == 0 {
		return &pub.ValidationError{Msg: "at least one item is required"}
	}
	for _, it := range req.Items {
		if it.StockItemID == uuid.Nil {
			return &pub.ValidationError{Msg: "item stock_item_id is required"}
		}
		if it.RequestedQty <= 0 {
			return &pub.ValidationError{Msg: "item requested_qty must be positive"}
		}
	}
	return nil
}
