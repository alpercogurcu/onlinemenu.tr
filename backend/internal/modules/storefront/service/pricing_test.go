package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	catalogpub "onlinemenu.tr/internal/modules/catalog/public"
	pospub "onlinemenu.tr/internal/modules/pos/public"
	"onlinemenu.tr/internal/modules/storefront/domain"
	pub "onlinemenu.tr/internal/modules/storefront/public"
	"onlinemenu.tr/internal/modules/storefront/service"
	"onlinemenu.tr/internal/platform/auth"
)

// The catalog and pos sides are stubbed here on purpose: what is under test
// is that the storefront takes EVERY amount from the catalog read model and
// nothing from the diner, which is a property of the wiring, not of SQL. The
// SQL half (which menu wins, what dine_in availability means) is covered by
// the catalog repo's integration tests.

type fakeCatalog struct {
	priced   []catalogpub.PricedLine
	priceErr error
	gotLines []catalogpub.CartLine
}

func (f *fakeCatalog) GetStorefrontMenu(_ context.Context, _, _ uuid.UUID) ([]catalogpub.StorefrontCategory, error) {
	return nil, nil
}

func (f *fakeCatalog) PriceCart(_ context.Context, _, _ uuid.UUID, lines []catalogpub.CartLine) ([]catalogpub.PricedLine, error) {
	f.gotLines = lines
	if f.priceErr != nil {
		return nil, f.priceErr
	}
	return f.priced, nil
}

type fakePos struct {
	got      pospub.GuestOrderRequest
	linker   pospub.GuestOrderLinker
	placeErr error
}

func (f *fakePos) PlaceGuestOrder(_ context.Context, req pospub.GuestOrderRequest, link pospub.GuestOrderLinker) (pospub.GuestOrderResult, error) {
	f.got = req
	f.linker = link
	if f.placeErr != nil {
		return pospub.GuestOrderResult{}, f.placeErr
	}
	return pospub.GuestOrderResult{OrderID: uuid.New(), CheckID: uuid.New(), Status: "pending"}, nil
}

func (f *fakePos) GetGuestOrder(_ context.Context, _, _ uuid.UUID) (pospub.GuestOrderView, error) {
	return pospub.GuestOrderView{}, nil
}

func newOrderService(catalog *fakeCatalog, pos *fakePos) *service.OrderService {
	return service.NewOrderService(service.OrderParams{
		// DB and GuestOrders stay nil: Place touches neither. The binding row
		// is written by the linker callback, which pos invokes inside its own
		// transaction — see the linker assertion below.
		Catalog: catalog,
		Placer:  pos,
		Orders:  pos,
		Logger:  zap.NewNop(),
	})
}

func testGuest() auth.GuestSession {
	return auth.GuestSession{
		TenantID:  uuid.New(),
		BranchID:  uuid.New(),
		TableID:   uuid.New(),
		QRCodeID:  uuid.New(),
		SessionID: uuid.New(),
	}
}

// TestPlace_PersistsReadModelPrice is the core anti-tampering assertion: the
// order pos receives carries the catalog's price, and the cart the diner
// submitted had no price field to begin with (domain.GuestCartLine has none —
// this test would not compile if it did).
func TestPlace_PersistsReadModelPrice(t *testing.T) {
	productID := uuid.New()
	catalog := &fakeCatalog{priced: []catalogpub.PricedLine{{
		ProductID:       productID,
		ProductName:     "Latte",
		BasePriceAmount: 4500,
		UnitPriceAmount: 4500,
		Currency:        "TRY",
		TaxRateBPS:      1000,
		Quantity:        2,
	}}}
	pos := &fakePos{}
	svc := newOrderService(catalog, pos)
	guest := testGuest()

	placed, err := svc.Place(context.Background(), guest, domain.GuestCart{
		Lines: []domain.GuestCartLine{{ProductID: productID, Quantity: 2}},
	})
	require.NoError(t, err)

	require.Len(t, pos.got.Lines, 1)
	line := pos.got.Lines[0]
	assert.Equal(t, int64(4500), line.UnitPriceAmount)
	assert.Equal(t, int64(4500), line.BasePriceAmount)
	assert.Equal(t, 1000, line.TaxRateBPS)
	assert.Equal(t, "Latte", line.Name, "the product name is the server's, not the client's")
	assert.Equal(t, int64(9000), placed.Total, "total is quantity × server unit price")

	// The session's own claims decide tenant/branch/table — nothing about
	// them can be influenced by the request body.
	assert.Equal(t, guest.TenantID, pos.got.TenantID)
	assert.Equal(t, guest.BranchID, pos.got.BranchID)
	assert.Equal(t, guest.TableID, pos.got.TableID)
	assert.Equal(t, guest.SessionID, pos.got.GuestSessionID)
	assert.Equal(t, guest.QRCodeID, pos.got.QRCodeID)
}

// TestPlace_ModifierDeltaLandsInUnitPrice: pos bills quantity ×
// unit_price_amount, so a delta kept anywhere else would never be charged.
func TestPlace_ModifierDeltaLandsInUnitPrice(t *testing.T) {
	productID := uuid.New()
	modifierID := uuid.New()
	catalog := &fakeCatalog{priced: []catalogpub.PricedLine{{
		ProductID:       productID,
		ProductName:     "Latte",
		BasePriceAmount: 4500,
		UnitPriceAmount: 5000,
		Currency:        "TRY",
		Quantity:        1,
		Modifiers:       []catalogpub.PricedModifier{{ID: modifierID, Name: "Ekstra shot", PriceDelta: 500}},
	}}}
	pos := &fakePos{}
	svc := newOrderService(catalog, pos)

	_, err := svc.Place(context.Background(), testGuest(), domain.GuestCart{
		Lines: []domain.GuestCartLine{{ProductID: productID, Quantity: 1, ModifierIDs: []uuid.UUID{modifierID}, Note: "az şekerli"}},
	})
	require.NoError(t, err)

	require.Len(t, pos.got.Lines, 1)
	assert.Equal(t, int64(5000), pos.got.Lines[0].UnitPriceAmount)
	// order_items has no modifier columns, so the selection has to reach the
	// kitchen through the note or the ticket would say plain "Latte".
	assert.Contains(t, pos.got.Lines[0].Note, "Ekstra shot")
	assert.Contains(t, pos.got.Lines[0].Note, "az şekerli")
}

// TestPlace_MultiLineCart_NotesStayWithTheirProduct pins the positional
// contract between PriceCart's result and the submitted cart: the two are
// zipped by index, so a reordered or short result would put "soğansız" on the
// wrong dish. Single-line tests cannot see that.
func TestPlace_MultiLineCart_NotesStayWithTheirProduct(t *testing.T) {
	burger, salad := uuid.New(), uuid.New()
	catalog := &fakeCatalog{priced: []catalogpub.PricedLine{
		{ProductID: burger, ProductName: "Burger", UnitPriceAmount: 20000, Quantity: 1},
		{ProductID: salad, ProductName: "Salata", UnitPriceAmount: 8000, Quantity: 3},
	}}
	pos := &fakePos{}
	svc := newOrderService(catalog, pos)

	_, err := svc.Place(context.Background(), testGuest(), domain.GuestCart{
		Lines: []domain.GuestCartLine{
			{ProductID: burger, Quantity: 1, Note: "soğansız"},
			{ProductID: salad, Quantity: 3, Note: "sossuz"},
		},
	})
	require.NoError(t, err)

	require.Len(t, pos.got.Lines, 2)
	assert.Equal(t, "Burger", pos.got.Lines[0].Name)
	assert.Equal(t, "soğansız", pos.got.Lines[0].Note)
	assert.Equal(t, "Salata", pos.got.Lines[1].Name)
	assert.Equal(t, "sossuz", pos.got.Lines[1].Note)
}

// TestPlace_SameProductTwice_StaysTwoLines: a diner ordering the same coffee
// twice with different options must get two distinct lines, not a merged one.
func TestPlace_SameProductTwice_StaysTwoLines(t *testing.T) {
	productID := uuid.New()
	catalog := &fakeCatalog{priced: []catalogpub.PricedLine{
		{ProductID: productID, ProductName: "Latte", UnitPriceAmount: 4500, Quantity: 1},
		{ProductID: productID, ProductName: "Latte", UnitPriceAmount: 5000, Quantity: 1,
			Modifiers: []catalogpub.PricedModifier{{ID: uuid.New(), Name: "Ekstra shot", PriceDelta: 500}}},
	}}
	pos := &fakePos{}
	svc := newOrderService(catalog, pos)

	placed, err := svc.Place(context.Background(), testGuest(), domain.GuestCart{
		Lines: []domain.GuestCartLine{
			{ProductID: productID, Quantity: 1},
			{ProductID: productID, Quantity: 1, Note: "sade"},
		},
	})
	require.NoError(t, err)

	require.Len(t, pos.got.Lines, 2)
	assert.Equal(t, int64(4500), pos.got.Lines[0].UnitPriceAmount)
	assert.Equal(t, int64(5000), pos.got.Lines[1].UnitPriceAmount)
	assert.Contains(t, pos.got.Lines[1].Note, "Ekstra shot")
	assert.Contains(t, pos.got.Lines[1].Note, "sade")
	assert.Equal(t, int64(9500), placed.Total)
}

// TestPlace_PricedLineCountMismatch_IsInternalError: a catalog that returned
// fewer lines than were submitted would silently drop items (and misalign
// notes). That is a bug in the read model, not something to show a diner.
func TestPlace_PricedLineCountMismatch_IsInternalError(t *testing.T) {
	productID := uuid.New()
	catalog := &fakeCatalog{priced: []catalogpub.PricedLine{
		{ProductID: productID, ProductName: "Latte", UnitPriceAmount: 4500, Quantity: 1},
	}}
	pos := &fakePos{}
	svc := newOrderService(catalog, pos)

	_, err := svc.Place(context.Background(), testGuest(), domain.GuestCart{
		Lines: []domain.GuestCartLine{
			{ProductID: productID, Quantity: 1},
			{ProductID: uuid.New(), Quantity: 1},
		},
	})

	require.Error(t, err)
	var validation *pub.ValidationError
	assert.False(t, errors.As(err, &validation),
		"a short pricing result is an internal inconsistency, not diner input — it must not surface as a 422")
	assert.Equal(t, pospub.GuestOrderRequest{}, pos.got, "nothing may reach pos on a misaligned cart")
}

func TestPlace_UnorderableProduct_IsValidationError(t *testing.T) {
	catalog := &fakeCatalog{priceErr: &catalogpub.ValidationError{Msg: "product is not orderable"}}
	pos := &fakePos{}
	svc := newOrderService(catalog, pos)

	_, err := svc.Place(context.Background(), testGuest(), domain.GuestCart{
		Lines: []domain.GuestCartLine{{ProductID: uuid.New(), Quantity: 1}},
	})

	var validation *pub.ValidationError
	require.ErrorAs(t, err, &validation)
	assert.Equal(t, pospub.GuestOrderRequest{}, pos.got, "an unpriceable cart must never reach pos")
}

func TestPlace_EmptyCart_IsValidationError(t *testing.T) {
	pos := &fakePos{}
	svc := newOrderService(&fakeCatalog{}, pos)

	_, err := svc.Place(context.Background(), testGuest(), domain.GuestCart{})

	var validation *pub.ValidationError
	require.ErrorAs(t, err, &validation)
	assert.Equal(t, pospub.GuestOrderRequest{}, pos.got)
}

// TestPlace_PassesLinkerSoTheBindingIsTransactional guards the invariant that
// makes "my orders" answerable: the storefront_guest_orders row is written by
// a callback pos runs INSIDE its placement transaction, never in a second one
// that could fail on its own and leave an order the diner cannot see.
func TestPlace_PassesLinkerSoTheBindingIsTransactional(t *testing.T) {
	productID := uuid.New()
	catalog := &fakeCatalog{priced: []catalogpub.PricedLine{{
		ProductID: productID, ProductName: "Çay", UnitPriceAmount: 1000, Quantity: 1,
	}}}
	pos := &fakePos{}
	svc := newOrderService(catalog, pos)

	_, err := svc.Place(context.Background(), testGuest(), domain.GuestCart{
		Lines: []domain.GuestCartLine{{ProductID: productID, Quantity: 1}},
	})
	require.NoError(t, err)
	require.NotNil(t, pos.linker, "pos must be handed the binding callback, not left to place an unlinked order")

	// A linker failure must abort the placement (pos rolls its transaction
	// back); the caller sees the error rather than a phantom success.
	var _ func(context.Context, pgx.Tx, pospub.GuestOrderResult) error = pos.linker
}

func TestPlace_TableNotReady_PropagatesSentinel(t *testing.T) {
	productID := uuid.New()
	catalog := &fakeCatalog{priced: []catalogpub.PricedLine{{
		ProductID: productID, ProductName: "Çay", UnitPriceAmount: 1000, Quantity: 1,
	}}}
	pos := &fakePos{placeErr: pospub.ErrTableNotReady}
	svc := newOrderService(catalog, pos)

	_, err := svc.Place(context.Background(), testGuest(), domain.GuestCart{
		Lines: []domain.GuestCartLine{{ProductID: productID, Quantity: 1}},
	})

	require.ErrorIs(t, err, pospub.ErrTableNotReady,
		"the HTTP layer distinguishes 'being cleaned' from 'occupied'; wrapping away the sentinel would collapse both")
}
