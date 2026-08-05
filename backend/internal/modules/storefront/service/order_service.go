package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/fx"
	"go.uber.org/zap"

	catalogpub "onlinemenu.tr/internal/modules/catalog/public"
	pospub "onlinemenu.tr/internal/modules/pos/public"
	"onlinemenu.tr/internal/modules/storefront/domain"
	pub "onlinemenu.tr/internal/modules/storefront/public"
	"onlinemenu.tr/internal/modules/storefront/repo"
	"onlinemenu.tr/internal/platform/auth"
	"onlinemenu.tr/internal/platform/db"
)

// OrderService turns a submitted cart into a pos order and answers "where is
// my order" for the session that placed it.
type OrderService struct {
	db          *db.Pool
	catalog     catalogpub.StorefrontMenuReader
	placer      pospub.GuestOrderPlacer
	orders      pospub.GuestOrderReader
	guestOrders *repo.GuestOrderRepo
	logger      *zap.Logger
}

// OrderParams groups fx-injected dependencies.
type OrderParams struct {
	fx.In

	DB          *db.Pool
	Catalog     catalogpub.StorefrontMenuReader
	Placer      pospub.GuestOrderPlacer
	Orders      pospub.GuestOrderReader
	GuestOrders *repo.GuestOrderRepo
	Logger      *zap.Logger
}

func NewOrderService(p OrderParams) *OrderService {
	return &OrderService{
		db:          p.DB,
		catalog:     p.Catalog,
		placer:      p.Placer,
		orders:      p.Orders,
		guestOrders: p.GuestOrders,
		logger:      p.Logger,
	}
}

// PlacedOrder is what a diner gets back after submitting a cart.
type PlacedOrder struct {
	OrderID uuid.UUID
	CheckID uuid.UUID
	Status  string
	// Total is the sum of quantity × unit price over the re-priced lines
	// (kuruş) — the amount the diner actually committed to, computed from
	// the server's own numbers.
	Total int64
}

// GuestOrderStatus is a diner-facing order projection.
type GuestOrderStatus struct {
	OrderID   uuid.UUID
	Status    string
	Note      string
	Items     []GuestOrderStatusItem
	Total     int64
	CreatedAt time.Time
	UpdatedAt time.Time
}

// GuestOrderStatusItem is one line of GuestOrderStatus.
type GuestOrderStatusItem struct {
	Name            string
	Quantity        int
	UnitPriceAmount int64
	Note            string
}

// Place re-prices the cart server-side and hands it to pos.
//
// The client's own price fields are not "validated" — domain.GuestCartLine
// has none, so there is nothing to compare against. Every amount that reaches
// the order comes from catalog's storefront read model, resolved for the
// branch in the guest's signed session.
//
// The storefront_guest_orders binding is written INSIDE pos's placement
// transaction (pospub.GuestOrderLinker): an order that exists but is
// unreachable by the diner who placed it would be worse than no order at all
// — the diner would retry and end up billed twice.
func (s *OrderService) Place(ctx context.Context, guest auth.GuestSession, cart domain.GuestCart) (PlacedOrder, error) {
	if len(cart.Lines) == 0 {
		return PlacedOrder{}, &pub.ValidationError{Msg: "sepet boş"}
	}

	lines := make([]catalogpub.CartLine, len(cart.Lines))
	for i, l := range cart.Lines {
		lines[i] = catalogpub.CartLine{
			ProductID:   l.ProductID,
			Quantity:    l.Quantity,
			ModifierIDs: l.ModifierIDs,
		}
	}

	priced, err := s.catalog.PriceCart(ctx, guest.TenantID, guest.BranchID, lines)
	if err != nil {
		var validation *catalogpub.ValidationError
		if errors.As(err, &validation) {
			return PlacedOrder{}, &pub.ValidationError{Msg: validation.Msg}
		}
		return PlacedOrder{}, fmt.Errorf("storefront/service/order: price cart: %w", err)
	}

	// The per-line notes below are matched POSITIONALLY (PriceCart returns one
	// line per input line, in order). That contract is load-bearing — a short
	// or reordered result would attach "soğansız" to the wrong dish — so it is
	// checked rather than assumed: a violation is a bug in catalog, not
	// something to render to a diner.
	if len(priced) != len(cart.Lines) {
		return PlacedOrder{}, fmt.Errorf(
			"storefront/service/order: priced %d lines for a %d-line cart", len(priced), len(cart.Lines))
	}

	orderLines := make([]pospub.GuestOrderLine, len(priced))
	var total int64
	for i, p := range priced {
		orderLines[i] = pospub.GuestOrderLine{
			ProductID:       p.ProductID,
			Name:            p.ProductName,
			BasePriceAmount: p.BasePriceAmount,
			UnitPriceAmount: p.UnitPriceAmount,
			Currency:        p.Currency,
			TaxRateBPS:      p.TaxRateBPS,
			Quantity:        p.Quantity,
			Note:            lineNote(p, cart.Lines[i].Note),
		}
		total += p.UnitPriceAmount * int64(p.Quantity)
	}

	result, err := s.placer.PlaceGuestOrder(ctx, pospub.GuestOrderRequest{
		TenantID:       guest.TenantID,
		BranchID:       guest.BranchID,
		TableID:        guest.TableID,
		QRCodeID:       guest.QRCodeID,
		GuestSessionID: guest.SessionID,
		Lines:          orderLines,
		Note:           cart.Note,
	}, func(ctx context.Context, tx pgx.Tx, placed pospub.GuestOrderResult) error {
		return s.guestOrders.Link(ctx, tx, domain.GuestOrderLink{
			OrderID:        placed.OrderID,
			TenantID:       guest.TenantID,
			QRCodeID:       guest.QRCodeID,
			GuestSessionID: guest.SessionID,
			CheckID:        placed.CheckID,
		})
	})
	if err != nil {
		return PlacedOrder{}, mapPlacementErr(err)
	}

	return PlacedOrder{
		OrderID: result.OrderID,
		CheckID: result.CheckID,
		Status:  result.Status,
		Total:   total,
	}, nil
}

// lineNote folds the selected modifiers into the order item's note.
//
// order_items has no modifier columns, so the kitchen would otherwise never
// learn that this coffee is decaf: the price delta alone travels in
// unit_price_amount. Rendering the names into the note is what makes the
// selection visible on the KDS ticket.
func lineNote(p catalogpub.PricedLine, guestNote string) string {
	if len(p.Modifiers) == 0 {
		return guestNote
	}
	names := make([]string, len(p.Modifiers))
	for i, m := range p.Modifiers {
		names[i] = m.Name
	}
	rendered := strings.Join(names, ", ")
	if guestNote == "" {
		return rendered
	}
	return rendered + " | " + guestNote
}

// mapPlacementErr keeps pos's guest sentinels intact so the HTTP layer can
// give the diner an actionable message, and wraps everything else.
func mapPlacementErr(err error) error {
	switch {
	case errors.Is(err, pospub.ErrTableNotReady),
		errors.Is(err, pospub.ErrTableOccupied),
		errors.Is(err, pospub.ErrTableBranchMismatch),
		errors.Is(err, pospub.ErrTableNotFound):
		return err
	default:
		return fmt.Errorf("storefront/service/order: place: %w", err)
	}
}

// ListMine returns the orders this guest session placed, newest first.
func (s *OrderService) ListMine(ctx context.Context, guest auth.GuestSession) ([]GuestOrderStatus, error) {
	var links []domain.GuestOrderLink
	err := s.db.WithTenantReadTx(ctx, guest.TenantID, func(tx pgx.Tx) error {
		var err error
		links, err = s.guestOrders.ListBySession(ctx, tx, guest.SessionID)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("storefront/service/order: list mine: %w", err)
	}

	// The pos reads happen AFTER the link transaction closes, never inside
	// it: pospub.GuestOrderReader opens its own transaction on another pooled
	// connection, and calling it while holding one would risk pool starvation
	// (the same reason CheckService.Close reads payments before its write tx).
	out := make([]GuestOrderStatus, 0, len(links))
	for _, link := range links {
		view, err := s.orders.GetGuestOrder(ctx, guest.TenantID, link.OrderID)
		if err != nil {
			if errors.Is(err, pospub.ErrNotFound) {
				// A link whose order vanished is data corruption, not a
				// diner-facing condition: skip it, but make it visible.
				s.logger.Error("storefront: guest order link points at a missing order",
					zap.String("order_id", link.OrderID.String()),
					zap.String("tenant_id", link.TenantID.String()),
				)
				continue
			}
			return nil, fmt.Errorf("storefront/service/order: list mine read: %w", err)
		}
		out = append(out, toGuestOrderStatus(view))
	}
	return out, nil
}

// GetMine returns one order, but only if this session placed it.
//
// The session id is part of the lookup's WHERE clause (GuestOrderRepo
// .GetForSession), not a comparison afterwards: another diner's order id must
// be indistinguishable from a nonexistent one, or the endpoint becomes an
// oracle for guessing order ids.
func (s *OrderService) GetMine(ctx context.Context, guest auth.GuestSession, orderID uuid.UUID) (GuestOrderStatus, error) {
	var link domain.GuestOrderLink
	err := s.db.WithTenantReadTx(ctx, guest.TenantID, func(tx pgx.Tx) error {
		var err error
		link, err = s.guestOrders.GetForSession(ctx, tx, guest.SessionID, orderID)
		return err
	})
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return GuestOrderStatus{}, pub.ErrGuestForbidden
		}
		return GuestOrderStatus{}, fmt.Errorf("storefront/service/order: get mine: %w", err)
	}

	view, err := s.orders.GetGuestOrder(ctx, guest.TenantID, link.OrderID)
	if err != nil {
		if errors.Is(err, pospub.ErrNotFound) {
			return GuestOrderStatus{}, pub.ErrGuestForbidden
		}
		return GuestOrderStatus{}, fmt.Errorf("storefront/service/order: get mine read: %w", err)
	}
	return toGuestOrderStatus(view), nil
}

func toGuestOrderStatus(view pospub.GuestOrderView) GuestOrderStatus {
	items := make([]GuestOrderStatusItem, len(view.Items))
	var total int64
	for i, it := range view.Items {
		items[i] = GuestOrderStatusItem{
			Name:            it.ProductName,
			Quantity:        it.Quantity,
			UnitPriceAmount: it.UnitPriceAmount,
			Note:            it.Note,
		}
		total += it.UnitPriceAmount * int64(it.Quantity)
	}
	return GuestOrderStatus{
		OrderID:   view.ID,
		Status:    view.Status,
		Note:      view.Note,
		Items:     items,
		Total:     total,
		CreatedAt: view.CreatedAt,
		UpdatedAt: view.UpdatedAt,
	}
}
