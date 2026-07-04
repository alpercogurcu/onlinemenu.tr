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

// TransferOrderService manages branch transfer orders (ADR-DATA-006).
//
// IMPORTANT: this service never sets Status to BTOStatusShipped or
// BTOStatusReceived — those transitions are owned exclusively by
// ShipmentService, which reacts to the linked Shipment's own status changes
// (ADR-DATA-006 ownership rule). There is deliberately no exported method
// here that lets an HTTP caller set those two values directly.
type TransferOrderService struct {
	db       *db.Pool
	repo     *repo.TransferOrderRepo
	itemRepo *repo.TransferOrderItemRepo
	logger   *zap.Logger
}

// TransferOrderParams groups fx-injected dependencies for NewTransferOrderService.
type TransferOrderParams struct {
	fx.In

	DB       *db.Pool
	Repo     *repo.TransferOrderRepo
	ItemRepo *repo.TransferOrderItemRepo
	Logger   *zap.Logger
}

// NewTransferOrderService constructs a TransferOrderService for fx injection.
func NewTransferOrderService(p TransferOrderParams) *TransferOrderService {
	return &TransferOrderService{db: p.DB, repo: p.Repo, itemRepo: p.ItemRepo, logger: p.Logger}
}

// CreateTransferOrderItemRequest is one requested line item.
type CreateTransferOrderItemRequest struct {
	StockItemID  uuid.UUID
	RequestedQty float64
	Unit         string
	Note         string
}

// CreateTransferOrderRequest carries the parameters for creating a BTO.
type CreateTransferOrderRequest struct {
	RequestingBranchID    uuid.UUID
	SourceBranchID        uuid.UUID
	Priority              domain.Priority
	RequestedDeliveryDate *time.Time
	Note                  string
	CreatedBy             *uuid.UUID
	Items                 []CreateTransferOrderItemRequest
}

// Create persists a new BTO in draft status with its line items.
func (s *TransferOrderService) Create(ctx context.Context, tenantID uuid.UUID, req CreateTransferOrderRequest) (domain.BranchTransferOrder, []domain.BranchTransferOrderItem, error) {
	if req.Priority == "" {
		req.Priority = domain.PriorityNormal
	}
	if err := validateCreateTransferOrder(req); err != nil {
		return domain.BranchTransferOrder{}, nil, err
	}

	var order domain.BranchTransferOrder
	var items []domain.BranchTransferOrderItem
	err := s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		order, err = s.repo.Create(ctx, tx, domain.BranchTransferOrder{
			TenantID:              tenantID,
			RequestingBranchID:    req.RequestingBranchID,
			SourceBranchID:        req.SourceBranchID,
			Status:                domain.BTOStatusDraft,
			Priority:              req.Priority,
			RequestedDeliveryDate: req.RequestedDeliveryDate,
			Note:                  req.Note,
			CreatedBy:             req.CreatedBy,
		})
		if err != nil {
			return fmt.Errorf("create transfer order: %w", err)
		}

		for _, it := range req.Items {
			created, err := s.itemRepo.Add(ctx, tx, domain.BranchTransferOrderItem{
				TenantID:        tenantID,
				TransferOrderID: order.ID,
				StockItemID:     it.StockItemID,
				RequestedQty:    it.RequestedQty,
				Unit:            it.Unit,
				Note:            it.Note,
			})
			if err != nil {
				return fmt.Errorf("add transfer order item: %w", err)
			}
			items = append(items, created)
		}
		return nil
	})
	if err != nil {
		return domain.BranchTransferOrder{}, nil, fmt.Errorf("inventory/service: create transfer order: %w", err)
	}
	return order, items, nil
}

// Get returns a BTO by id.
func (s *TransferOrderService) Get(ctx context.Context, tenantID, id uuid.UUID) (domain.BranchTransferOrder, error) {
	var order domain.BranchTransferOrder
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		order, err = s.repo.GetByID(ctx, tx, id)
		return err
	})
	if err != nil {
		return domain.BranchTransferOrder{}, wrapErr(err, "inventory/service: get transfer order: %w")
	}
	return order, nil
}

// ListItems returns the line items of a BTO.
func (s *TransferOrderService) ListItems(ctx context.Context, tenantID, id uuid.UUID) ([]domain.BranchTransferOrderItem, error) {
	var items []domain.BranchTransferOrderItem
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		items, err = s.itemRepo.ListByTransferOrder(ctx, tx, id)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("inventory/service: list transfer order items: %w", err)
	}
	return items, nil
}

// ListByRequestingBranch returns BTOs requested by a branch.
func (s *TransferOrderService) ListByRequestingBranch(ctx context.Context, tenantID, branchID uuid.UUID) ([]domain.BranchTransferOrder, error) {
	var out []domain.BranchTransferOrder
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		out, err = s.repo.ListByRequestingBranch(ctx, tx, branchID)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("inventory/service: list transfer orders by requesting branch: %w", err)
	}
	return out, nil
}

// ListBySourceBranch returns BTOs to be fulfilled by a source branch.
func (s *TransferOrderService) ListBySourceBranch(ctx context.Context, tenantID, branchID uuid.UUID) ([]domain.BranchTransferOrder, error) {
	var out []domain.BranchTransferOrder
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		out, err = s.repo.ListBySourceBranch(ctx, tx, branchID)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("inventory/service: list transfer orders by source branch: %w", err)
	}
	return out, nil
}

// Submit transitions a BTO from draft to submitted (requesting branch action).
func (s *TransferOrderService) Submit(ctx context.Context, tenantID uuid.UUID, principal auth.Principal, id uuid.UUID) (domain.BranchTransferOrder, error) {
	return s.transition(ctx, tenantID, principal, id, domain.BTOStatusSubmitted, requestingBranchOf, transitionOpts{setSubmittedAt: true})
}

// ApprovalItem carries the approved quantity for one BTO line item, and
// optionally the transfer (sale) price for that item (ADR-DATA-006 eklenti /
// ADR-DATA-007 SS4). UnitPrice is nil when the source branch does not price
// the item at approval time (e.g. free/approved_suppliers policy items,
// whose branch-local cost comes from a local purchase, not a transfer
// price). Currency defaults to "TRY" (matching the billing module's
// convention) when UnitPrice is set but Currency is left empty.
type ApprovalItem struct {
	StockItemID uuid.UUID
	ApprovedQty float64
	UnitPrice   *float64
	Currency    string
}

// Approve transitions a BTO from submitted to approved (source branch action),
// recording per-item approved quantities (partial approval supported) and,
// where the source branch supplies one, the per-item transfer price
// (ADR-DATA-006 eklenti / ADR-DATA-007 SS4). The requesting branch never
// sets a price; only the approving (source) branch may.
func (s *TransferOrderService) Approve(ctx context.Context, tenantID uuid.UUID, principal auth.Principal, id, approvedBy uuid.UUID, approvals []ApprovalItem) (domain.BranchTransferOrder, error) {
	var updated domain.BranchTransferOrder
	err := s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		order, err := s.repo.GetByID(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := requireBranch(ctx, principal, order.SourceBranchID); err != nil {
			return err
		}
		if err := domain.TransitionBTOStatus(order.Status, domain.BTOStatusApproved); err != nil {
			return &pub.TransitionError{Msg: err.Error()}
		}

		items, err := s.itemRepo.ListByTransferOrder(ctx, tx, id)
		if err != nil {
			return fmt.Errorf("list items for approval: %w", err)
		}
		itemIDByStockItem := make(map[uuid.UUID]uuid.UUID, len(items))
		for _, item := range items {
			itemIDByStockItem[item.StockItemID] = item.ID
		}
		for _, a := range approvals {
			itemID, ok := itemIDByStockItem[a.StockItemID]
			if !ok {
				return &pub.ValidationError{Msg: fmt.Sprintf("stock_item_id %s is not on this transfer order", a.StockItemID)}
			}
			if err := s.itemRepo.SetApprovedQty(ctx, tx, itemID, a.ApprovedQty); err != nil {
				return fmt.Errorf("set approved qty: %w", err)
			}
			if a.UnitPrice != nil {
				if *a.UnitPrice < 0 {
					return &pub.ValidationError{Msg: fmt.Sprintf("unit_price for stock_item_id %s must not be negative", a.StockItemID)}
				}
				currency := a.Currency
				if currency == "" {
					currency = "TRY"
				}
				if err := s.itemRepo.SetUnitPrice(ctx, tx, itemID, *a.UnitPrice, currency); err != nil {
					return fmt.Errorf("set unit price: %w", err)
				}
			}
		}

		now := time.Now()
		updated, err = s.repo.UpdateStatus(ctx, tx, id, domain.BTOStatusApproved, nil, &now, &approvedBy)
		if err != nil {
			return fmt.Errorf("update status: %w", err)
		}
		return nil
	})
	if err != nil {
		return domain.BranchTransferOrder{}, wrapErr(err, "inventory/service: approve transfer order: %w")
	}
	return updated, nil
}

// Reject transitions a BTO from submitted to rejected (source branch action).
func (s *TransferOrderService) Reject(ctx context.Context, tenantID uuid.UUID, principal auth.Principal, id uuid.UUID) (domain.BranchTransferOrder, error) {
	return s.transition(ctx, tenantID, principal, id, domain.BTOStatusRejected, sourceBranchOf, transitionOpts{})
}

// Cancel transitions a BTO to cancelled. Branch ownership of this action
// depends on the BTO's CURRENT status per ADR-DATA-006's state table:
// draft/submitted -> cancelled is a requesting-branch action ("iptal" /
// "geri çek"); approved -> cancelled is a source-branch action ("iptal,
// henüz sevk yok" — the source branch withdraws before shipping anything).
//
// Ratified (2026-07-04 sprint review): the status-aware split follows
// ADR-DATA-006's state table; the flat "requesting-only" rule in the sprint
// task matrix was a simplification and is superseded by the ADR.
func (s *TransferOrderService) Cancel(ctx context.Context, tenantID uuid.UUID, principal auth.Principal, id uuid.UUID) (domain.BranchTransferOrder, error) {
	var updated domain.BranchTransferOrder
	err := s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		order, err := s.repo.GetByID(ctx, tx, id)
		if err != nil {
			return err
		}
		branchOf := requestingBranchOf
		if order.Status == domain.BTOStatusApproved {
			branchOf = sourceBranchOf
		}
		if err := requireBranch(ctx, principal, branchOf(order)); err != nil {
			return err
		}
		if err := domain.TransitionBTOStatus(order.Status, domain.BTOStatusCancelled); err != nil {
			return &pub.TransitionError{Msg: err.Error()}
		}
		updated, err = s.repo.UpdateStatus(ctx, tx, id, domain.BTOStatusCancelled, nil, nil, nil)
		if err != nil {
			return fmt.Errorf("update status: %w", err)
		}
		return nil
	})
	if err != nil {
		return domain.BranchTransferOrder{}, wrapErr(err, "inventory/service: cancel transfer order: %w")
	}
	return updated, nil
}

// Fulfil transitions a BTO from approved to fulfilling (source branch begins
// preparation/production).
func (s *TransferOrderService) Fulfil(ctx context.Context, tenantID uuid.UUID, principal auth.Principal, id uuid.UUID) (domain.BranchTransferOrder, error) {
	return s.transition(ctx, tenantID, principal, id, domain.BTOStatusFulfilling, sourceBranchOf, transitionOpts{})
}

type transitionOpts struct {
	setSubmittedAt bool
}

// requestingBranchOf and sourceBranchOf select the branch that must own a
// BTO transition, for use with transition's branchOf parameter.
func requestingBranchOf(o domain.BranchTransferOrder) uuid.UUID { return o.RequestingBranchID }
func sourceBranchOf(o domain.BranchTransferOrder) uuid.UUID     { return o.SourceBranchID }

func (s *TransferOrderService) transition(ctx context.Context, tenantID uuid.UUID, principal auth.Principal, id uuid.UUID, to domain.BTOStatus, branchOf func(domain.BranchTransferOrder) uuid.UUID, opts transitionOpts) (domain.BranchTransferOrder, error) {
	var updated domain.BranchTransferOrder
	err := s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		order, err := s.repo.GetByID(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := requireBranch(ctx, principal, branchOf(order)); err != nil {
			return err
		}
		if err := domain.TransitionBTOStatus(order.Status, to); err != nil {
			return &pub.TransitionError{Msg: err.Error()}
		}

		var submittedAt *time.Time
		if opts.setSubmittedAt {
			now := time.Now()
			submittedAt = &now
		}
		updated, err = s.repo.UpdateStatus(ctx, tx, id, to, submittedAt, nil, nil)
		if err != nil {
			return fmt.Errorf("update status: %w", err)
		}
		return nil
	})
	if err != nil {
		return domain.BranchTransferOrder{}, wrapErr(err, "inventory/service: transition transfer order: %w")
	}
	return updated, nil
}

func validateCreateTransferOrder(req CreateTransferOrderRequest) error {
	if req.RequestingBranchID == uuid.Nil {
		return &pub.ValidationError{Msg: "requesting_branch_id is required"}
	}
	if req.SourceBranchID == uuid.Nil {
		return &pub.ValidationError{Msg: "source_branch_id is required"}
	}
	if req.RequestingBranchID == req.SourceBranchID {
		return &pub.ValidationError{Msg: "requesting_branch_id and source_branch_id must differ"}
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
