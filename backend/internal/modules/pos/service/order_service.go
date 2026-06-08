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

// Place creates a new order (with items).
func (s *OrderService) Place(ctx context.Context, tenantID uuid.UUID, o domain.Order) (domain.Order, error) {
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
func (s *OrderService) Accept(ctx context.Context, tenantID, orderID, acceptedBy uuid.UUID) (domain.Order, error) {
	var o domain.Order
	err := s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		o, err = s.orderRepo.Accept(ctx, tx, orderID, acceptedBy)
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

// Reject marks an order as rejected with a reason.
func (s *OrderService) Reject(ctx context.Context, tenantID, orderID, rejectedBy uuid.UUID, reason string) (domain.Order, error) {
	var o domain.Order
	err := s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		o, err = s.orderRepo.Reject(ctx, tx, orderID, rejectedBy, reason)
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

// AdvanceStatus transitions an accepted order through preparing → ready → delivered.
func (s *OrderService) AdvanceStatus(ctx context.Context, tenantID, orderID uuid.UUID, status domain.OrderStatus) (domain.Order, error) {
	if !status.Valid() {
		return domain.Order{}, fmt.Errorf("pos/service/order: invalid status %q", status)
	}
	var o domain.Order
	err := s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		o, err = s.orderRepo.AdvanceStatus(ctx, tx, orderID, status)
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
