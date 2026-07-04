package service

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/fx"
	"go.uber.org/zap"

	"onlinemenu.tr/internal/modules/pos/domain"
	"onlinemenu.tr/internal/modules/pos/repo"
	"onlinemenu.tr/internal/platform/auth"
	"onlinemenu.tr/internal/platform/db"
)

// OrderService manages order lifecycle within a check or as standalone (delivery/takeaway).
type OrderService struct {
	db        *db.Pool
	orderRepo *repo.OrderRepo
	logger    *zap.Logger
}

// OrderParams groups fx-injected dependencies.
type OrderParams struct {
	fx.In

	DB        *db.Pool
	OrderRepo *repo.OrderRepo
	Logger    *zap.Logger
}

func NewOrderService(p OrderParams) *OrderService {
	return &OrderService{db: p.DB, orderRepo: p.OrderRepo, logger: p.Logger}
}

// Place creates a new order (with items). The acting principal must belong
// to the requested branch_id (ADR-AUTH-001 layer 3 / security sprint); there
// is no persisted entity yet at this point, so the client-supplied
// branch_id is what gets validated.
func (s *OrderService) Place(ctx context.Context, tenantID uuid.UUID, principal auth.Principal, o domain.Order) (domain.Order, error) {
	if err := requireBranch(ctx, principal, o.BranchID); err != nil {
		return domain.Order{}, err
	}
	if !o.OrderChannel.Valid() {
		return domain.Order{}, fmt.Errorf("pos/service/order: invalid channel %q", o.OrderChannel)
	}
	o.TenantID = tenantID
	o.Status = domain.OrderStatusPending

	var created domain.Order
	err := s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		created, err = s.orderRepo.Create(ctx, tx, o)
		if err != nil {
			return err
		}
		return repo.InsertOutbox(ctx, tx, tenantID, "order", created.ID.String(), "order.placed", map[string]any{
			"tenant_id":     tenantID,
			"order_id":      created.ID,
			"branch_id":     created.BranchID,
			"check_id":      created.CheckID,
			"order_channel": created.OrderChannel,
			"item_count":    len(created.Items),
		})
	})
	if err != nil {
		return domain.Order{}, fmt.Errorf("pos/service/order: place: %w", err)
	}
	return created, nil
}

// GetByID returns an order with its items.
func (s *OrderService) GetByID(ctx context.Context, tenantID, orderID uuid.UUID) (domain.Order, error) {
	var o domain.Order
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		o, err = s.orderRepo.GetByID(ctx, tx, orderID)
		return err
	})
	if err != nil {
		return domain.Order{}, wrapErr(err, "pos/service/order: get by id: %w")
	}
	return o, nil
}

// ListByCheck returns all orders for a check.
func (s *OrderService) ListByCheck(ctx context.Context, tenantID, checkID uuid.UUID) ([]domain.Order, error) {
	var orders []domain.Order
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		orders, err = s.orderRepo.ListByCheck(ctx, tx, checkID)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("pos/service/order: list by check: %w", err)
	}
	return orders, nil
}

// Accept marks an order as accepted by staff.
// The current status is read with a row lock (GetForUpdate) so the
// transition check and the guarded UPDATE are race-free against any other
// concurrent transition attempt on the same order. The acting principal
// must belong to the order's branch (ADR-AUTH-001 layer 3 / security
// sprint) — checked right after loading, before the transition check, so a
// branch-forbidden caller gets 403 rather than a 409 that would otherwise
// leak the order's current status.
func (s *OrderService) Accept(ctx context.Context, tenantID uuid.UUID, principal auth.Principal, orderID, acceptedBy uuid.UUID) (domain.Order, error) {
	var o domain.Order
	err := s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		current, err := s.orderRepo.GetForUpdate(ctx, tx, orderID)
		if err != nil {
			return err
		}
		if err := requireBranch(ctx, principal, current.BranchID); err != nil {
			return err
		}
		if err := domain.TransitionOrderStatus(current.Status, domain.OrderStatusAccepted); err != nil {
			return err
		}

		o, err = s.orderRepo.Accept(ctx, tx, orderID, acceptedBy, current.Status)
		if err != nil {
			return err
		}
		return repo.InsertOutbox(ctx, tx, tenantID, "order", orderID.String(), "order.accepted", map[string]any{
			"tenant_id":   tenantID,
			"order_id":    orderID,
			"accepted_by": acceptedBy,
		})
	})
	if err != nil {
		return domain.Order{}, wrapErr(err, "pos/service/order: accept: %w")
	}
	return o, nil
}

// Reject marks an order as rejected with a reason. The acting principal
// must belong to the order's branch (ADR-AUTH-001 layer 3 / security
// sprint) — checked before the transition check, per Accept's rationale.
func (s *OrderService) Reject(ctx context.Context, tenantID uuid.UUID, principal auth.Principal, orderID, rejectedBy uuid.UUID, reason string) (domain.Order, error) {
	var o domain.Order
	err := s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		current, err := s.orderRepo.GetForUpdate(ctx, tx, orderID)
		if err != nil {
			return err
		}
		if err := requireBranch(ctx, principal, current.BranchID); err != nil {
			return err
		}
		if err := domain.TransitionOrderStatus(current.Status, domain.OrderStatusRejected); err != nil {
			return err
		}

		o, err = s.orderRepo.Reject(ctx, tx, orderID, rejectedBy, reason, current.Status)
		if err != nil {
			return err
		}
		return repo.InsertOutbox(ctx, tx, tenantID, "order", orderID.String(), "order.rejected", map[string]any{
			"tenant_id":   tenantID,
			"order_id":    orderID,
			"rejected_by": rejectedBy,
			"reason":      reason,
		})
	})
	if err != nil {
		return domain.Order{}, wrapErr(err, "pos/service/order: reject: %w")
	}
	return o, nil
}

// AdvanceStatus transitions an accepted order through preparing → ready → delivered
// (or cancels it), validating the move against the order status machine.
// The acting principal must belong to the order's branch (ADR-AUTH-001
// layer 3 / security sprint) — checked before the transition check, per
// Accept's rationale.
func (s *OrderService) AdvanceStatus(ctx context.Context, tenantID uuid.UUID, principal auth.Principal, orderID uuid.UUID, status domain.OrderStatus) (domain.Order, error) {
	if !status.Valid() {
		return domain.Order{}, fmt.Errorf("pos/service/order: invalid status %q", status)
	}
	var o domain.Order
	err := s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		current, err := s.orderRepo.GetForUpdate(ctx, tx, orderID)
		if err != nil {
			return err
		}
		if err := requireBranch(ctx, principal, current.BranchID); err != nil {
			return err
		}
		if err := domain.TransitionOrderStatus(current.Status, status); err != nil {
			return err
		}

		o, err = s.orderRepo.AdvanceStatus(ctx, tx, orderID, status, current.Status)
		if err != nil {
			return err
		}
		return repo.InsertOutbox(ctx, tx, tenantID, "order", orderID.String(), "order.status_changed", map[string]any{
			"tenant_id": tenantID,
			"order_id":  orderID,
			"status":    status,
		})
	})
	if err != nil {
		return domain.Order{}, wrapErr(err, "pos/service/order: advance status: %w")
	}
	return o, nil
}
