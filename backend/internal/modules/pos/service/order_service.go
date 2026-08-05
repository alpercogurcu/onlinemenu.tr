package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/fx"
	"go.uber.org/zap"

	"onlinemenu.tr/internal/modules/pos/domain"
	pub "onlinemenu.tr/internal/modules/pos/public"
	"onlinemenu.tr/internal/modules/pos/repo"
	"onlinemenu.tr/internal/platform/auth"
	"onlinemenu.tr/internal/platform/db"
)

// OrderService manages order lifecycle within a check or as standalone (delivery/takeaway).
type OrderService struct {
	db        *db.Pool
	orderRepo *repo.OrderRepo
	// checkRepo/tableRepo are used only by PlaceGuest, which must open a
	// guest check inside the same transaction as the order it carries (see
	// openCheckTx). The staff paths still go through CheckService.
	checkRepo *repo.CheckRepo
	tableRepo *repo.TableRepo
	logger    *zap.Logger
}

// OrderParams groups fx-injected dependencies.
type OrderParams struct {
	fx.In

	DB        *db.Pool
	OrderRepo *repo.OrderRepo
	CheckRepo *repo.CheckRepo
	TableRepo *repo.TableRepo
	Logger    *zap.Logger
}

func NewOrderService(p OrderParams) *OrderService {
	return &OrderService{
		db:        p.DB,
		orderRepo: p.OrderRepo,
		checkRepo: p.CheckRepo,
		tableRepo: p.TableRepo,
		logger:    p.Logger,
	}
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

// PlaceGuest places an anonymous QR order (ADR-ARCH-006 §8).
//
// It takes no auth.Principal and never calls requireBranch: a diner is not
// staff, and handing Place a synthesized principal would answer "may this
// person act on this branch" with a fabricated yes. The branch/table pairing
// is instead verified against persisted rows, inside the transaction.
//
// Everything happens in ONE write transaction — check resolution, the order,
// its items, the caller's own binding row (link) and the outbox event — so a
// placed order can never exist without the row that makes it reachable by the
// diner who placed it.
//
// Lock order is check → table, deliberately matching CheckService.Close /
// Cancel. Reversing it (lock the table, then look for its open check) would
// deadlock a diner submitting a cart against a cashier closing that table's
// check at the same moment.
//
//  1. The table's open check, locked. Found: the order joins it (staff- or
//     guest-opened alike, per the ADR). Its branch must match the QR's.
//  2. No open check: open one via openCheckTx with guestTableGuard —
//     opened_by NULL / opened_by_kind guest_qr / source online_qr, and the
//     table moves to occupied. A cleaning table stops here (ErrTableNotReady).
//  3. The order itself: order_channel dine_in + source online_qr + pending.
//  4. The order.placed outbox event, carrying source so the KDS sees the
//     order through the existing pipeline with no WS change.
func (s *OrderService) PlaceGuest(ctx context.Context, req pub.GuestOrderRequest, link pub.GuestOrderLinker) (pub.GuestOrderResult, error) {
	if s.checkRepo == nil || s.tableRepo == nil {
		return pub.GuestOrderResult{}, fmt.Errorf("pos/service/order: place guest: check/table repo not wired")
	}
	if len(req.Lines) == 0 {
		return pub.GuestOrderResult{}, fmt.Errorf("pos/service/order: place guest: no lines")
	}

	// One retry, and only for ErrTableOccupied: two diners at the same empty
	// table submit at once, both find no open check to lock (an empty result
	// set locks nothing), and both try to open one. The loser blocks on the
	// table row, then sees it occupied by the winner's brand-new check —
	// a 409 that would be nonsense to a diner sitting at their own table.
	// Retrying the whole transaction lets the loser observe the committed
	// check and join it, exactly as a later order would.
	//
	// The retry is safe to repeat wholesale because the failed attempt rolled
	// back completely (order, check, table status and the caller's link row);
	// and it is bounded at one because a second occupied verdict means the
	// table is genuinely held by something this diner cannot join.
	result, err := s.placeGuestOnce(ctx, req, link)
	if errors.Is(err, pub.ErrTableOccupied) {
		result, err = s.placeGuestOnce(ctx, req, link)
	}
	if err != nil {
		return pub.GuestOrderResult{}, err
	}
	return result, nil
}

func (s *OrderService) placeGuestOnce(ctx context.Context, req pub.GuestOrderRequest, link pub.GuestOrderLinker) (pub.GuestOrderResult, error) {
	var result pub.GuestOrderResult
	err := s.db.WithTenantTx(ctx, req.TenantID, func(tx pgx.Tx) error {
		check, err := s.resolveGuestCheck(ctx, tx, req)
		if err != nil {
			return err
		}

		order := domain.Order{
			TenantID:     req.TenantID,
			BranchID:     req.BranchID,
			CheckID:      &check.ID,
			OrderChannel: domain.OrderChannelDineIn,
			Source:       domain.SourceOnlineQR,
			Status:       domain.OrderStatusPending,
			Note:         req.Note,
			Items:        guestOrderItems(req.Lines),
		}
		created, err := s.orderRepo.Create(ctx, tx, order)
		if err != nil {
			return err
		}

		result = pub.GuestOrderResult{
			OrderID: created.ID,
			CheckID: check.ID,
			Status:  string(created.Status),
		}
		if link != nil {
			if err := link(ctx, tx, result); err != nil {
				return err
			}
		}

		return repo.InsertOutbox(ctx, tx, req.TenantID, "order", created.ID.String(), "order.placed", map[string]any{
			"tenant_id":     req.TenantID,
			"order_id":      created.ID,
			"branch_id":     created.BranchID,
			"check_id":      created.CheckID,
			"order_channel": created.OrderChannel,
			"source":        string(created.Source),
			"item_count":    len(created.Items),
		})
	})
	if err != nil {
		return pub.GuestOrderResult{}, mapGuestErr(err)
	}
	return result, nil
}

// resolveGuestCheck returns the check a guest order attaches to, opening one
// when the table has none. See PlaceGuest for the lock ordering rationale.
func (s *OrderService) resolveGuestCheck(ctx context.Context, tx pgx.Tx, req pub.GuestOrderRequest) (domain.Check, error) {
	existing, err := s.checkRepo.GetOpenByTableForUpdate(ctx, tx, req.TableID)
	switch {
	case err == nil:
		if existing.BranchID != req.BranchID {
			// The QR sticker outlived a table move between branches.
			return domain.Check{}, pub.ErrTableBranchMismatch
		}
		return existing, nil
	case errors.Is(err, repo.ErrNotFound):
		// No open check — fall through and open one.
	default:
		return domain.Check{}, err
	}

	return openCheckTx(ctx, tx, s.checkRepo, s.tableRepo, domain.Check{
		TenantID:     req.TenantID,
		BranchID:     req.BranchID,
		TableID:      &req.TableID,
		Pax:          1,
		Status:       domain.CheckStatusOpen,
		OpenedBy:     nil,
		OpenedByKind: domain.OpenedByKindGuestQR,
		Source:       domain.SourceOnlineQR,
	}, guestTableGuard)
}

// guestOrderItems converts already-priced public lines into order items.
// pos does not re-price here: the caller (storefront) derived every amount
// from the catalog read model, and the client's own numbers never entered
// that derivation.
func guestOrderItems(lines []pub.GuestOrderLine) []domain.OrderItem {
	items := make([]domain.OrderItem, len(lines))
	for i, l := range lines {
		currency := l.Currency
		if currency == "" {
			currency = "TRY"
		}
		items[i] = domain.OrderItem{
			ProductID:          l.ProductID,
			ProductName:        l.Name,
			ProductPriceAmount: l.BasePriceAmount,
			ProductCurrency:    currency,
			TaxRateBPS:         l.TaxRateBPS,
			Quantity:           l.Quantity,
			UnitPriceAmount:    l.UnitPriceAmount,
			Note:               l.Note,
		}
	}
	return items
}

// mapGuestErr keeps the guest-facing sentinels intact (the storefront maps
// them to its own status codes) and adds operation context to everything else.
func mapGuestErr(err error) error {
	switch {
	case errors.Is(err, pub.ErrTableNotReady),
		errors.Is(err, pub.ErrTableOccupied),
		errors.Is(err, pub.ErrTableBranchMismatch),
		errors.Is(err, pub.ErrTableNotFound):
		return err
	case errors.Is(err, repo.ErrTableOccupied):
		return pub.ErrTableOccupied
	case errors.Is(err, repo.ErrNotFound):
		// The only row a guest placement looks up by id is the table.
		return pub.ErrTableNotFound
	default:
		return fmt.Errorf("pos/service/order: place guest: %w", err)
	}
}

// GetGuestOrder returns a diner-facing projection of one order.
//
// It performs NO authorization: "is this order this diner's?" is answered by
// the storefront from storefront_guest_orders before it calls here. Passing a
// raw order id straight from a request into this method would be a bug — see
// GuestOrderReader's doc comment.
func (s *OrderService) GetGuestOrder(ctx context.Context, tenantID, orderID uuid.UUID) (pub.GuestOrderView, error) {
	o, err := s.GetByID(ctx, tenantID, orderID)
	if err != nil {
		return pub.GuestOrderView{}, err
	}
	items := make([]pub.GuestOrderItemView, len(o.Items))
	for i, it := range o.Items {
		items[i] = pub.GuestOrderItemView{
			ProductName:     it.ProductName,
			Quantity:        it.Quantity,
			UnitPriceAmount: it.UnitPriceAmount,
			Note:            it.Note,
		}
	}
	return pub.GuestOrderView{
		ID:        o.ID,
		CheckID:   o.CheckID,
		Status:    string(o.Status),
		Note:      o.Note,
		Items:     items,
		CreatedAt: o.CreatedAt,
		UpdatedAt: o.UpdatedAt,
	}, nil
}

// GetGuestTable returns the narrow table projection the storefront needs to
// validate a scanned QR code (pub.GuestTableReader).
func (s *OrderService) GetGuestTable(ctx context.Context, tenantID, tableID uuid.UUID) (pub.GuestTable, error) {
	if s.tableRepo == nil {
		return pub.GuestTable{}, fmt.Errorf("pos/service/order: get guest table: table repo not wired")
	}
	var t domain.Table
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		t, err = s.tableRepo.GetTableByID(ctx, tx, tableID)
		return err
	})
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return pub.GuestTable{}, pub.ErrTableNotFound
		}
		return pub.GuestTable{}, fmt.Errorf("pos/service/order: get guest table: %w", err)
	}
	return pub.GuestTable{
		ID:       t.ID,
		BranchID: t.BranchID,
		Label:    t.Name,
		Status:   t.Status,
		IsActive: t.IsActive,
	}, nil
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

// ListActiveByBranch returns domain.KitchenActiveOrderStatuses orders
// (pending/accepted/preparing/ready) for a branch. Used by ws.Hub to build
// the kitchen WebSocket snapshot sent on connect.
// No principal is required: callers within the pos module (the WS hub
// reacting to its own outbox events) already know tenantID/branchID are
// trustworthy — HTTP-facing callers must gate this behind their own
// permission + branch checks, as ws.Hub does before invoking it.
func (s *OrderService) ListActiveByBranch(ctx context.Context, tenantID, branchID uuid.UUID) ([]domain.Order, error) {
	var orders []domain.Order
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		orders, err = s.orderRepo.ListActiveByBranch(ctx, tx, branchID)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("pos/service/order: list active by branch: %w", err)
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
