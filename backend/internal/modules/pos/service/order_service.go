package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/fx"
	"go.uber.org/zap"

	catalogpub "onlinemenu.tr/internal/modules/catalog/public"
	"onlinemenu.tr/internal/modules/pos/domain"
	pub "onlinemenu.tr/internal/modules/pos/public"
	"onlinemenu.tr/internal/modules/pos/repo"
	"onlinemenu.tr/internal/platform/auth"
	"onlinemenu.tr/internal/platform/db"
)

// ErrInvalidOrderStatus is returned when /advance is asked for a status that
// is not a kitchen advance target and has no dedicated endpoint either
// (unknown strings, "pending").
var ErrInvalidOrderStatus = errors.New("pos/service/order: invalid order status")

// ErrUseDedicatedEndpoint is returned when /advance is asked for accepted,
// rejected or cancelled. Those transitions have their own endpoints behind
// stricter permissions; see domain.IsKitchenAdvanceTarget.
var ErrUseDedicatedEndpoint = errors.New("pos/service/order: status requires its dedicated endpoint")

// ErrPriceMismatch is returned when a staff order line's unit_price_amount
// does not equal the product's catalog price plus the selected modifiers'
// deltas (docs/pos-ux-spec.md bulgu #14 / P0).
//
// Until this existed, Place copied the client's number straight into the
// order: a POS terminal could write any price it liked, and the varyant
// feature (P1) would have turned that into a supported discount channel. The
// QR guest path never had the hole — its prices are derived from the catalog
// read model and the diner's numbers never enter that derivation — so this
// brings the staff path level with it.
var ErrPriceMismatch = errors.New("pos/service/order: line price does not match the catalog")

// ErrInvalidOrderLine wraps a catalog-side rejection of an order line: the
// product is not sellable, or a selected modifier is not attached to it / is
// selected too many times. It is distinct from ErrPriceMismatch because the
// cashier's remedy differs — one means "refresh your product list", the other
// "your total is stale".
var ErrInvalidOrderLine = errors.New("pos/service/order: invalid order line")

// OrderService manages order lifecycle within a check or as standalone (delivery/takeaway).
type OrderService struct {
	db        *db.Pool
	orderRepo *repo.OrderRepo
	// pricer re-derives staff line prices from the catalog. It is a required
	// dependency, not an optional one: a nil pricer would silently reopen the
	// hole ErrPriceMismatch exists to close, so Place fails closed instead.
	pricer catalogpub.StaffPricer
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
	Pricer    catalogpub.StaffPricer
	Logger    *zap.Logger
}

func NewOrderService(p OrderParams) *OrderService {
	return &OrderService{
		db:        p.DB,
		orderRepo: p.OrderRepo,
		checkRepo: p.CheckRepo,
		tableRepo: p.TableRepo,
		pricer:    p.Pricer,
		logger:    p.Logger,
	}
}

// Place creates a new order (with items). The acting principal must belong
// to the requested branch_id (ADR-AUTH-001 layer 3 / security sprint); there
// is no persisted entity yet at this point, so the client-supplied
// branch_id is what gets validated.
//
// When o.CheckID is set, the check must still be open and must belong to
// o.BranchID. Until 2026-09-15 neither was verified: production accepted
// orders onto closed and cancelled adisyons (201), because the only status
// enforcement lived in CheckService.Close — by which time the food had
// already been sent to the kitchen and booked against a settled check. The
// check row is taken FOR UPDATE inside this same transaction, so a cashier
// closing the check concurrently either loses the race or makes this call
// fail; a plain read would leave a TOCTOU window the lock closes for free.
//
// A nil o.CheckID (takeaway/delivery — masasız satış) skips the guard: those
// orders legitimately have no check to validate.
func (s *OrderService) Place(ctx context.Context, tenantID uuid.UUID, principal auth.Principal, o domain.Order) (domain.Order, error) {
	if err := requireBranch(ctx, principal, o.BranchID); err != nil {
		return domain.Order{}, err
	}
	if !o.OrderChannel.Valid() {
		return domain.Order{}, fmt.Errorf("pos/service/order: invalid channel %q", o.OrderChannel)
	}
	if err := s.repriceItems(ctx, tenantID, o.BranchID, o.Items); err != nil {
		return domain.Order{}, err
	}
	o.TenantID = tenantID
	o.Status = domain.OrderStatusPending

	var created domain.Order
	err := s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		if err := s.lockWritableCheck(ctx, tx, o.CheckID, o.BranchID); err != nil {
			return err
		}
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
		return domain.Order{}, wrapErr(err, "pos/service/order: place: %w")
	}
	return created, nil
}

// repriceItems re-derives every line's price from the catalog and rejects the
// order when the client's unit_price_amount disagrees (ErrPriceMismatch).
//
// It also OVERWRITES the snapshot columns the client supplies alongside the
// price — product name, list price, currency and tax rate — with the
// catalog's own values. Rejecting on the price while trusting the tax rate
// would leave the VAT breakdown in the day-end report and on the fiscal
// receipt client-controlled, which is the same hole one field over.
//
// The comparison, not a silent correction, is what the caller gets for the
// unit price: a POS whose cached price is stale must be told its total is
// wrong before the cashier reads it out to the customer.
//
// branchID is the branch the order is being placed at: since ADR-DATA-009 a
// product may carry that branch's own price, or not be sold there at all, so
// pricing a counter sale without naming the branch would bill the tenant
// default at a branch that charges something else.
//
// items is mutated in place; the caller passes the slice it is about to
// persist.
func (s *OrderService) repriceItems(ctx context.Context, tenantID, branchID uuid.UUID, items []domain.OrderItem) error {
	if s.pricer == nil {
		return errors.New("pos/service/order: staff pricer not wired")
	}
	if len(items) == 0 {
		return nil
	}

	lines := make([]catalogpub.StaffCartLine, len(items))
	for i, it := range items {
		lines[i] = catalogpub.StaffCartLine{
			ProductID:   it.ProductID,
			Quantity:    it.Quantity,
			ModifierIDs: it.ModifierIDs,
		}
	}

	priced, err := s.pricer.PriceStaffCart(ctx, tenantID, branchID, lines)
	if err != nil {
		var invalid *catalogpub.ValidationError
		if errors.As(err, &invalid) {
			return fmt.Errorf("pos/service/order: %s: %w", invalid.Msg, ErrInvalidOrderLine)
		}
		return fmt.Errorf("pos/service/order: price order lines: %w", err)
	}
	// PriceStaffCart contracts one line out per line in, in input order. A
	// short slice would silently re-price the wrong item, so this is checked
	// rather than assumed.
	if len(priced) != len(items) {
		return fmt.Errorf("pos/service/order: pricer returned %d lines for %d items", len(priced), len(items))
	}

	for i := range items {
		p := priced[i]
		if items[i].UnitPriceAmount != p.UnitPriceAmount {
			return fmt.Errorf("pos/service/order: line %d (%s): sent %d, catalog says %d: %w",
				i, p.ProductName, items[i].UnitPriceAmount, p.UnitPriceAmount, ErrPriceMismatch)
		}
		items[i].ProductName = p.ProductName
		items[i].ProductPriceAmount = p.BasePriceAmount
		items[i].ProductCurrency = p.Currency
		items[i].TaxRateBPS = p.TaxRateBPS
	}
	return nil
}

// lockWritableCheck locks the order's check and rejects the placement when it
// can no longer receive one. A nil checkID is a no-op (see Place).
func (s *OrderService) lockWritableCheck(ctx context.Context, tx pgx.Tx, checkID *uuid.UUID, branchID uuid.UUID) error {
	if checkID == nil || *checkID == uuid.Nil {
		return nil
	}
	if s.checkRepo == nil {
		return errors.New("check repo not wired")
	}
	current, err := s.checkRepo.GetForUpdate(ctx, tx, *checkID)
	if err != nil {
		return err
	}
	return assertCheckWritable(current, branchID)
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
			ModifierIDs:        l.ModifierIDs,
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

// ListByIDs returns the requested orders with their items in a single read
// transaction, so a client showing many order details at once (the kitchen
// display board) makes one call instead of one per order.
//
// It is a partial-result read by design: ids that do not exist, or belong to
// another tenant (excluded by RLS inside the transaction), are absent from
// the result rather than turning the whole call into ErrNotFound. Callers
// must not infer existence from the response length.
//
// Like GetByID, it applies no branch check — see ListActiveByBranch's note:
// the HTTP caller is already gated by pos.order.read, and the single-order
// path this replaces reads the same rows under the same rules.
func (s *OrderService) ListByIDs(ctx context.Context, tenantID uuid.UUID, orderIDs []uuid.UUID) ([]domain.Order, error) {
	if len(orderIDs) == 0 {
		return nil, nil
	}
	var orders []domain.Order
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		orders, err = s.orderRepo.ListByIDs(ctx, tx, orderIDs)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("pos/service/order: list by ids: %w", err)
	}
	return orders, nil
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
// The current status is read with a row lock (lockOrderForTransition) so the
// transition check and the guarded UPDATE are race-free against any other
// concurrent transition attempt on the same order. The acting principal
// must belong to the order's branch (ADR-AUTH-001 layer 3 / security
// sprint) — checked before the transition check, so a branch-forbidden
// caller gets 403 rather than a 409 that would otherwise leak the order's
// current status. An order on a closed or cancelled check is refused with
// pub.ErrCheckNotOpen: accepting it would send food to the kitchen for a
// check that can no longer be billed.
func (s *OrderService) Accept(ctx context.Context, tenantID uuid.UUID, principal auth.Principal, orderID, acceptedBy uuid.UUID) (domain.Order, error) {
	var o domain.Order
	err := s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		current, err := s.lockOrderForTransition(ctx, tx, principal, orderID)
		if err != nil {
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
		current, err := s.lockOrderForTransition(ctx, tx, principal, orderID)
		if err != nil {
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

// AdvanceStatus moves an order through the kitchen stages preparing → ready
// → delivered, validating the move against the order status machine.
//
// Only domain.IsKitchenAdvanceTarget statuses are accepted, checked before
// any database work. accepted/rejected/cancelled are refused with
// ErrUseDedicatedEndpoint rather than silently allowed: this endpoint is
// gated by pos.order.advance, which kitchen/bar hold, and until 2026-09-19 it
// let them accept, reject and cancel orders the policy forbids them to touch.
//
// Branch (403) and check (409 check_not_open) are verified in
// lockOrderForTransition, before the transition check, per Accept's rationale.
func (s *OrderService) AdvanceStatus(ctx context.Context, tenantID uuid.UUID, principal auth.Principal, orderID uuid.UUID, status domain.OrderStatus) (domain.Order, error) {
	if !domain.IsKitchenAdvanceTarget(status) {
		switch status {
		case domain.OrderStatusAccepted, domain.OrderStatusRejected, domain.OrderStatusCancelled:
			return domain.Order{}, fmt.Errorf("pos/service/order: advance to %q: %w", status, ErrUseDedicatedEndpoint)
		default:
			return domain.Order{}, fmt.Errorf("pos/service/order: advance to %q: %w", status, ErrInvalidOrderStatus)
		}
	}
	o, err := s.transition(ctx, tenantID, principal, orderID, status, map[string]any{})
	if err != nil {
		return domain.Order{}, wrapErr(err, "pos/service/order: advance status: %w")
	}
	return o, nil
}

// Cancel cancels a single live order (pending/accepted/preparing/ready). It is
// the counter-side replacement for advance {status:"cancelled"} and is gated
// by the same permission as Reject, so kitchen/bar cannot reach it.
func (s *OrderService) Cancel(ctx context.Context, tenantID uuid.UUID, principal auth.Principal, orderID, cancelledBy uuid.UUID) (domain.Order, error) {
	o, err := s.transition(ctx, tenantID, principal, orderID, domain.OrderStatusCancelled, map[string]any{
		"cancelled_by": cancelledBy,
	})
	if err != nil {
		return domain.Order{}, wrapErr(err, "pos/service/order: cancel: %w")
	}
	return o, nil
}

// transition applies a plain status change (no accept/reject bookkeeping
// columns) and records it as an order.status_changed event. extra is merged
// into the event payload.
func (s *OrderService) transition(ctx context.Context, tenantID uuid.UUID, principal auth.Principal, orderID uuid.UUID, status domain.OrderStatus, extra map[string]any) (domain.Order, error) {
	var o domain.Order
	err := s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		current, err := s.lockOrderForTransition(ctx, tx, principal, orderID)
		if err != nil {
			return err
		}
		if err := domain.TransitionOrderStatus(current.Status, status); err != nil {
			return err
		}

		o, err = s.orderRepo.AdvanceStatus(ctx, tx, orderID, status, current.Status)
		if err != nil {
			return err
		}
		payload := map[string]any{
			"tenant_id": tenantID,
			"order_id":  orderID,
			"status":    status,
		}
		for k, v := range extra {
			payload[k] = v
		}
		return repo.InsertOutbox(ctx, tx, tenantID, "order", orderID.String(), "order.status_changed", payload)
	})
	return o, err
}

// lockOrderForTransition loads and row-locks an order about to change status,
// after verifying the principal's branch and that the order's check (if any)
// is still open.
//
// Lock order is check → order, the same as CheckService.Cancel (which locks
// the check, then cancels its orders) and Place. Locking the order first and
// the check second would deadlock a kitchen action against a concurrent check
// cancel. The order is therefore first read WITHOUT a lock just to learn its
// check_id and branch_id — both immutable after insert, so the unlocked read
// cannot go stale in a way that matters.
//
// Branch is checked before the check status so a caller from another branch
// gets 403 and learns nothing about the check (see assertCheckWritable).
func (s *OrderService) lockOrderForTransition(ctx context.Context, tx pgx.Tx, principal auth.Principal, orderID uuid.UUID) (domain.Order, error) {
	peek, err := s.orderRepo.GetHeader(ctx, tx, orderID)
	if err != nil {
		return domain.Order{}, err
	}
	if err := requireBranch(ctx, principal, peek.BranchID); err != nil {
		return domain.Order{}, err
	}
	if err := s.lockWritableCheck(ctx, tx, peek.CheckID, peek.BranchID); err != nil {
		return domain.Order{}, err
	}
	return s.orderRepo.GetForUpdate(ctx, tx, orderID)
}
