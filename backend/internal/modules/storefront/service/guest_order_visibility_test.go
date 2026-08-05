package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	catalogpub "onlinemenu.tr/internal/modules/catalog/public"
	pospub "onlinemenu.tr/internal/modules/pos/public"
	"onlinemenu.tr/internal/modules/storefront/domain"
	pub "onlinemenu.tr/internal/modules/storefront/public"
	"onlinemenu.tr/internal/modules/storefront/repo"
	"onlinemenu.tr/internal/modules/storefront/service"
	"onlinemenu.tr/internal/platform/auth"
)

// "Which orders may this diner read" is the second-most exposed decision in
// the storefront after token resolution, and it is the one the pricing unit
// tests cannot reach: the answer lives in storefront_guest_orders — a WHERE
// clause plus an RLS policy — not in Go. These run against the real table so
// that a widened lookup (session id compared in Go, tenant dropped from the
// policy) fails here rather than in production.

// linkingPlacer stands in for pos: it accepts the placement and runs the
// caller's linker inside a real tenant transaction, exactly where pos runs it
// (pospub.GuestOrderLinker's contract), so the binding row written here is the
// one production would write.
type linkingPlacer struct {
	tenantID uuid.UUID
	orderID  uuid.UUID
	checkID  uuid.UUID
}

func (p *linkingPlacer) PlaceGuestOrder(ctx context.Context, _ pospub.GuestOrderRequest, link pospub.GuestOrderLinker) (pospub.GuestOrderResult, error) {
	result := pospub.GuestOrderResult{OrderID: p.orderID, CheckID: p.checkID, Status: "pending"}
	err := sharedPool.WithTenantTx(ctx, p.tenantID, func(tx pgx.Tx) error {
		return link(ctx, tx, result)
	})
	if err != nil {
		return pospub.GuestOrderResult{}, err
	}
	return result, nil
}

// mapOrderReader answers only for orders it knows about; anything else is
// pos's ErrNotFound, which is what a foreign order id looks like from here.
type mapOrderReader struct {
	views map[uuid.UUID]pospub.GuestOrderView
}

func (r *mapOrderReader) GetGuestOrder(_ context.Context, _, orderID uuid.UUID) (pospub.GuestOrderView, error) {
	view, ok := r.views[orderID]
	if !ok {
		return pospub.GuestOrderView{}, pospub.ErrNotFound
	}
	return view, nil
}

type staticCatalog struct{ line catalogpub.PricedLine }

func (c staticCatalog) GetStorefrontMenu(_ context.Context, _, _ uuid.UUID) ([]catalogpub.StorefrontCategory, error) {
	return nil, nil
}

func (c staticCatalog) PriceCart(_ context.Context, _, _ uuid.UUID, lines []catalogpub.CartLine) ([]catalogpub.PricedLine, error) {
	out := make([]catalogpub.PricedLine, len(lines))
	for i, l := range lines {
		priced := c.line
		priced.ProductID = l.ProductID
		priced.Quantity = l.Quantity
		out[i] = priced
	}
	return out, nil
}

func guestSessionFor(t *testing.T, seed qrSeed) auth.GuestSession {
	t.Helper()
	svc := newSessionService(t, stubTables{table: pospub.GuestTable{
		BranchID: seed.branchID, Label: "Masa 1", IsActive: true,
	}})
	resolved, err := svc.ResolveToken(context.Background(), seed.rawToken)
	require.NoError(t, err)
	return auth.GuestSession{
		TenantID:  resolved.TenantID,
		BranchID:  resolved.BranchID,
		TableID:   resolved.TableID,
		QRCodeID:  resolved.QRCodeID,
		SessionID: resolved.SessionID,
		ExpiresAt: time.Now().Add(time.Hour),
	}
}

// placeFor puts one order on the given session's table and returns its id,
// going through OrderService.Place so the binding is written the production
// way (inside the placement transaction) rather than inserted by the test.
func placeFor(t *testing.T, guest auth.GuestSession, reader *mapOrderReader, name string) uuid.UUID {
	t.Helper()

	placer := &linkingPlacer{tenantID: guest.TenantID, orderID: uuid.New(), checkID: uuid.New()}
	svc := service.NewOrderService(service.OrderParams{
		DB: sharedPool,
		Catalog: staticCatalog{line: catalogpub.PricedLine{
			ProductName: name, BasePriceAmount: 1000, UnitPriceAmount: 1000,
			Currency: "TRY", TaxRateBPS: 1000,
		}},
		Placer:      placer,
		Orders:      reader,
		GuestOrders: repo.NewGuestOrderRepo(),
		Logger:      zap.NewNop(),
	})

	placed, err := svc.Place(context.Background(), guest, domain.GuestCart{
		Lines: []domain.GuestCartLine{{ProductID: uuid.New(), Quantity: 1}},
	})
	require.NoError(t, err)

	reader.views[placed.OrderID] = pospub.GuestOrderView{
		ID:     placed.OrderID,
		Status: "pending",
		Items: []pospub.GuestOrderItemView{
			{ProductName: name, Quantity: 1, UnitPriceAmount: 1000},
		},
	}
	return placed.OrderID
}

func readerService(reader *mapOrderReader) *service.OrderService {
	return service.NewOrderService(service.OrderParams{
		DB:          sharedPool,
		Catalog:     staticCatalog{},
		Placer:      &linkingPlacer{},
		Orders:      reader,
		GuestOrders: repo.NewGuestOrderRepo(),
		Logger:      zap.NewNop(),
	})
}

// TestGetMine_OtherSessionsOrderAtTheSameTable_IsNotFound: two phones at one
// table are two sessions. Neither may poll the other's order, even though the
// tenant, branch, table and QR code are identical — the session id is the only
// thing separating them.
func TestGetMine_OtherSessionsOrderAtTheSameTable_IsNotFound(t *testing.T) {
	seed := seedQRCode(t, uuid.New())
	first := guestSessionFor(t, seed)
	second := guestSessionFor(t, seed)
	require.NotEqual(t, first.SessionID, second.SessionID, "each scan mints its own session")

	reader := &mapOrderReader{views: map[uuid.UUID]pospub.GuestOrderView{}}
	orderID := placeFor(t, first, reader, "Latte")
	svc := readerService(reader)

	own, err := svc.GetMine(context.Background(), first, orderID)
	require.NoError(t, err)
	assert.Equal(t, orderID, own.OrderID)

	_, err = svc.GetMine(context.Background(), second, orderID)
	assert.ErrorIs(t, err, pub.ErrGuestForbidden,
		"another diner's order id must be indistinguishable from a nonexistent one")
}

// TestGetMine_AnotherTenantsOrder_IsNotFound is the cross-tenant half: even a
// session holding the exact order id of another tenant's order gets nothing,
// because the link lookup runs under that session's own tenant GUC.
func TestGetMine_AnotherTenantsOrder_IsNotFound(t *testing.T) {
	guestA := guestSessionFor(t, seedQRCode(t, uuid.New()))
	guestB := guestSessionFor(t, seedQRCode(t, uuid.New()))
	require.NotEqual(t, guestA.TenantID, guestB.TenantID)

	reader := &mapOrderReader{views: map[uuid.UUID]pospub.GuestOrderView{}}
	orderA := placeFor(t, guestA, reader, "Adana")
	svc := readerService(reader)

	_, err := svc.GetMine(context.Background(), guestB, orderA)
	assert.ErrorIs(t, err, pub.ErrGuestForbidden)
}

// TestListMine_ReturnsOnlyThisSessionsOrders guards the list endpoint the same
// way GetMine is guarded: a table-wide listing would leak what the neighbours
// ordered.
func TestListMine_ReturnsOnlyThisSessionsOrders(t *testing.T) {
	seed := seedQRCode(t, uuid.New())
	mine := guestSessionFor(t, seed)
	theirs := guestSessionFor(t, seed)

	reader := &mapOrderReader{views: map[uuid.UUID]pospub.GuestOrderView{}}
	firstID := placeFor(t, mine, reader, "Çay")
	secondID := placeFor(t, mine, reader, "Kahve")
	otherID := placeFor(t, theirs, reader, "Ayran")
	svc := readerService(reader)

	orders, err := svc.ListMine(context.Background(), mine)
	require.NoError(t, err)

	ids := make([]uuid.UUID, 0, len(orders))
	for _, o := range orders {
		ids = append(ids, o.OrderID)
	}
	assert.ElementsMatch(t, []uuid.UUID{firstID, secondID}, ids)
	assert.NotContains(t, ids, otherID)

	othersOrders, err := svc.ListMine(context.Background(), theirs)
	require.NoError(t, err)
	require.Len(t, othersOrders, 1)
	assert.Equal(t, otherID, othersOrders[0].OrderID)
}

// TestListMine_LinkWithoutAnOrder_IsSkippedNotFatal: a binding row whose order
// vanished is corruption, but the diner's other orders must still render —
// and the surviving order must still be theirs.
func TestListMine_LinkWithoutAnOrder_IsSkippedNotFatal(t *testing.T) {
	guest := guestSessionFor(t, seedQRCode(t, uuid.New()))

	reader := &mapOrderReader{views: map[uuid.UUID]pospub.GuestOrderView{}}
	liveID := placeFor(t, guest, reader, "Lahmacun")
	vanishedID := placeFor(t, guest, reader, "Şalgam")
	delete(reader.views, vanishedID)

	orders, err := readerService(reader).ListMine(context.Background(), guest)
	require.NoError(t, err)
	require.Len(t, orders, 1)
	assert.Equal(t, liveID, orders[0].OrderID)
}
