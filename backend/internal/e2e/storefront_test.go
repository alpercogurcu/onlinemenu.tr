package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	catalogdomain "onlinemenu.tr/internal/modules/catalog/domain"
	catalogpub "onlinemenu.tr/internal/modules/catalog/public"
	catalogrepo "onlinemenu.tr/internal/modules/catalog/repo"
	catalogsvc "onlinemenu.tr/internal/modules/catalog/service"
	paymentdomain "onlinemenu.tr/internal/modules/payment/domain"
	paymentpub "onlinemenu.tr/internal/modules/payment/public"
	paymentrepo "onlinemenu.tr/internal/modules/payment/repo"
	paymentsvc "onlinemenu.tr/internal/modules/payment/service"
	posdomain "onlinemenu.tr/internal/modules/pos/domain"
	pospub "onlinemenu.tr/internal/modules/pos/public"
	posrepo "onlinemenu.tr/internal/modules/pos/repo"
	possvc "onlinemenu.tr/internal/modules/pos/service"
	storefronthttp "onlinemenu.tr/internal/modules/storefront/http"
	storefrontrepo "onlinemenu.tr/internal/modules/storefront/repo"
	storefrontsvc "onlinemenu.tr/internal/modules/storefront/service"
	"onlinemenu.tr/internal/platform/auth"
)

// The QR dine-in flow is the only path in the product that crosses every
// module in one request chain — storefront token → catalog menu → pos order →
// payment → back to a table status. The unit and per-module integration tests
// each see one link of that chain; this file is the only place the whole
// chain runs against one database, over the real public HTTP surface with its
// guard middleware attached.

const guestTokenSecret = "e2e-storefront-guest-secret-32b!"

var (
	qrTenantID = uuid.MustParse("aaaaaaaa-0000-0000-0000-000000000002")
	qrBranchID = uuid.MustParse("bbbbbbbb-0000-0000-0000-000000000002")
	qrStaffID  = uuid.MustParse("cccccccc-0000-0000-0000-000000000002")
)

// qrStaffPrincipal is branch-scoped to qrBranchID, which satisfies
// requireBranch's direct-match arm — no OPA bundle needs planting for the
// staff half of the flow (QR issue, accept, close).
func qrStaffPrincipal() auth.Principal {
	return auth.Principal{
		PersonID: qrStaffID,
		Ctx:      auth.ContextStaff,
		TenantID: qrTenantID,
		BranchID: qrBranchID,
		RoleIDs:  []uuid.UUID{uuid.New()},
	}
}

// guestPosAdapter mirrors pos/module.go's guestOrderAdapter: the storefront
// may only reach pos through the three guest methods, and wiring the service
// directly here would test a coupling production does not have.
type guestPosAdapter struct{ svc *possvc.OrderService }

func (a *guestPosAdapter) PlaceGuestOrder(ctx context.Context, req pospub.GuestOrderRequest, link pospub.GuestOrderLinker) (pospub.GuestOrderResult, error) {
	return a.svc.PlaceGuest(ctx, req, link)
}

func (a *guestPosAdapter) GetGuestOrder(ctx context.Context, tenantID, orderID uuid.UUID) (pospub.GuestOrderView, error) {
	return a.svc.GetGuestOrder(ctx, tenantID, orderID)
}

func (a *guestPosAdapter) GetGuestTable(ctx context.Context, tenantID, tableID uuid.UUID) (pospub.GuestTable, error) {
	return a.svc.GetGuestTable(ctx, tenantID, tableID)
}

var (
	_ pospub.GuestOrderPlacer         = (*guestPosAdapter)(nil)
	_ pospub.GuestOrderReader         = (*guestPosAdapter)(nil)
	_ pospub.GuestTableReader         = (*guestPosAdapter)(nil)
	_ catalogpub.StorefrontMenuReader = (*catalogsvc.StorefrontMenuService)(nil)
)

type storefrontStack struct {
	router   *chi.Mux
	qr       *storefrontsvc.QRService
	posOrder *possvc.OrderService
	check    *possvc.CheckService
	payment  *paymentsvc.PaymentService
}

// newStorefrontStack wires the production composition (minus fx) over the
// shared container: real services on both sides of every module boundary,
// real public router with its rate limit / guest guard / idempotency chain.
func newStorefrontStack(t *testing.T) storefrontStack {
	t.Helper()
	log := zap.NewNop()

	mr := miniredis.RunT(t)
	cache := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = cache.Close() })

	payService := paymentsvc.NewPaymentService(paymentsvc.Params{
		DB:          sharedPool,
		PaymentRepo: paymentrepo.NewPaymentRepo(),
		SessionRepo: paymentrepo.NewCashSessionRepo(),
		Fiscal:      paymentdomain.MockFiscalAdapter{},
		Logger:      log,
	})
	checkService := possvc.NewCheckService(possvc.CheckParams{
		DB:         sharedPool,
		CheckRepo:  posrepo.NewCheckRepo(),
		TableRepo:  posrepo.NewTableRepo(),
		SaleReader: &saleReaderAdapter{svc: payService},
		Logger:     log,
	})
	posOrders := possvc.NewOrderService(possvc.OrderParams{
		DB:        sharedPool,
		OrderRepo: posrepo.NewOrderRepo(),
		CheckRepo: posrepo.NewCheckRepo(),
		TableRepo: posrepo.NewTableRepo(),
		Logger:    log,
	})
	guestPos := &guestPosAdapter{svc: posOrders}

	menuReader := catalogsvc.NewStorefrontMenuService(catalogsvc.StorefrontMenuParams{
		DB:     sharedPool,
		Repo:   catalogrepo.NewStorefrontMenuRepo(),
		Logger: log,
	})

	signer, err := auth.NewGuestTokenSigner([]byte(guestTokenSecret))
	require.NoError(t, err)

	qrService := storefrontsvc.NewQRService(storefrontsvc.QRParams{
		DB: sharedPool, QRRepo: storefrontrepo.NewQRCodeRepo(), Logger: log,
	})
	handler := storefronthttp.NewPublicHandler(storefronthttp.PublicParams{
		Sessions: storefrontsvc.NewSessionService(storefrontsvc.SessionParams{
			DB:     sharedPool,
			QRRepo: storefrontrepo.NewQRCodeRepo(),
			Tables: guestPos,
			Signer: signer,
			Logger: log,
		}),
		Menu: storefrontsvc.NewMenuService(storefrontsvc.MenuParams{Catalog: menuReader, Logger: log}),
		Orders: storefrontsvc.NewOrderService(storefrontsvc.OrderParams{
			DB:          sharedPool,
			Catalog:     menuReader,
			Placer:      guestPos,
			Orders:      guestPos,
			GuestOrders: storefrontrepo.NewGuestOrderRepo(),
			Logger:      log,
		}),
		Signer: signer,
		Cache:  cache,
		Logger: log,
	})

	mux := chi.NewMux()
	handler.RegisterPublicRoutes(mux)

	return storefrontStack{
		router: mux, qr: qrService, posOrder: posOrders,
		check: checkService, payment: payService,
	}
}

type qrFixture struct {
	table     posdomain.Table
	productID uuid.UUID
	price     int64
	rawToken  string
}

// seedQRFixture builds everything a diner needs to exist before scanning: a
// table on qrBranchID, a product on that branch's active menu, an open cash
// session (ADR-DATA-008 refuses cash otherwise) and a freshly issued QR code.
func seedQRFixture(t *testing.T, stack storefrontStack, tableName, productName string, price int64) qrFixture {
	t.Helper()
	ctx := context.Background()
	f := qrFixture{price: price}

	tableRepo := posrepo.NewTableRepo()
	require.NoError(t, sharedPool.WithTenantTx(ctx, qrTenantID, func(tx pgx.Tx) error {
		zone, err := tableRepo.CreateZone(ctx, tx, posdomain.TableZone{
			TenantID: qrTenantID, BranchID: qrBranchID, Name: "QR Salon " + tableName, IsActive: true,
		})
		if err != nil {
			return err
		}
		f.table, err = tableRepo.CreateTable(ctx, tx, posdomain.Table{
			TenantID: qrTenantID, BranchID: qrBranchID, ZoneID: zone.ID,
			Name: tableName, Capacity: 4, IsActive: true,
		})
		return err
	}))

	require.NoError(t, sharedPool.WithTenantTx(ctx, qrTenantID, func(tx pgx.Tx) error {
		product, err := catalogrepo.NewProductRepo().Create(ctx, tx, catalogdomain.Product{
			TenantID: qrTenantID, Name: productName, PriceAmount: price,
			Currency: "TRY", Unit: "adet", TaxRateBPS: 1000, IsActive: true,
		})
		if err != nil {
			return err
		}
		f.productID = product.ID

		menu, err := catalogrepo.NewMenuRepo().Create(ctx, tx, catalogdomain.Menu{
			TenantID: qrTenantID, BranchID: &qrBranchID, Name: "QR Menü " + tableName, IsActive: true,
		})
		if err != nil {
			return err
		}
		return catalogrepo.NewMenuItemRepo().AddItem(ctx, tx, catalogdomain.MenuItem{
			MenuID: menu.ID, ProductID: product.ID, TenantID: qrTenantID, IsActive: true,
		})
	}))

	issued, err := stack.qr.Issue(ctx, qrTenantID, qrStaffPrincipal(), storefrontsvc.IssueRequest{
		BranchID:   qrBranchID,
		TableID:    f.table.ID,
		TableLabel: tableName,
		CreatedBy:  qrStaffID,
	})
	require.NoError(t, err)
	require.NotEmpty(t, issued.RawToken)
	f.rawToken = issued.RawToken
	return f
}

// openQRCashSession guarantees the branch has an open cash session, which
// PaymentService requires before it will take cash (ADR-DATA-008). Several
// tests in this file may call it, and the guard only needs SOME open session
// for the branch, so an already-open session is the desired state, not a
// failure.
func openQRCashSession(ctx context.Context, t *testing.T) {
	t.Helper()
	svc := paymentsvc.NewCashSessionService(paymentsvc.CashSessionParams{
		DB:         sharedPool,
		Sessions:   paymentrepo.NewCashSessionRepo(),
		FiscalRepo: paymentrepo.NewFiscalStatusRepo(),
		Logger:     zap.NewNop(),
	})
	_, err := svc.Open(ctx, qrStaffPrincipal(), paymentsvc.OpenCashSessionRequest{
		BranchID: qrBranchID, OpeningCountedAmount: 0,
	})
	if err != nil && !errors.Is(err, paymentpub.ErrCashSessionAlreadyOpen) {
		require.NoError(t, err)
	}
}

// ---------------------------------------------------------------------------
// Public surface helpers — every diner-side step goes over HTTP so the guard
// chain (rate limit → guest cookie → idempotency) is part of what is tested.
// ---------------------------------------------------------------------------

func startGuestSession(t *testing.T, stack storefrontStack, rawToken string) *http.Cookie {
	t.Helper()

	body, err := json.Marshal(map[string]string{"token": rawToken})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/api/public/v1/sessions", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	stack.router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	// The raw token travelled in the body; the path must stay free of it so
	// tracing and access logs cannot capture a live credential (ADR-ARCH-006 §3).
	assert.NotContains(t, req.URL.Path, rawToken)

	for _, c := range rec.Result().Cookies() {
		if c.Name == "om_guest" {
			assert.True(t, c.HttpOnly, "the guest cookie must be unreachable from JS")
			return c
		}
	}
	t.Fatal("no guest cookie was set")
	return nil
}

func getJSON(t *testing.T, stack storefrontStack, cookie *http.Cookie, path string, out any) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	stack.router.ServeHTTP(rec, req)
	if out != nil && rec.Code == http.StatusOK {
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), out))
	}
	return rec
}

type placedOrderResponse struct {
	OrderID uuid.UUID `json:"order_id"`
	CheckID uuid.UUID `json:"check_id"`
	Status  string    `json:"status"`
	Total   int64     `json:"total"`
}

func postOrder(t *testing.T, stack storefrontStack, cookie *http.Cookie, key, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/public/v1/orders", strings.NewReader(body))
	req.AddCookie(cookie)
	req.Header.Set("Idempotency-Key", key)
	rec := httptest.NewRecorder()
	stack.router.ServeHTTP(rec, req)
	return rec
}

// ---------------------------------------------------------------------------
// The flow
// ---------------------------------------------------------------------------

// TestStorefrontSpine_ScanOrderAcceptPayClose walks one QR order from the
// sticker on the table to the cleaned table, asserting at every step the
// invariants no single-module test can see: the ADR's row shapes (guest check,
// online_qr source), the KDS's outbox event, and the cashier's ability to
// settle an order no member of staff typed in.
func TestStorefrontSpine_ScanOrderAcceptPayClose(t *testing.T) {
	ctx := context.Background()
	stack := newStorefrontStack(t)
	openQRCashSession(ctx, t)
	f := seedQRFixture(t, stack, "QR-1", "Mercimek Çorbası", 12000)

	// 1. Scan: the raw token is exchanged for a guest session cookie.
	cookie := startGuestSession(t, stack, f.rawToken)

	// 2. Menu: the diner sees the branch's menu and nothing internal.
	var menu struct {
		Categories []struct {
			Products []struct {
				ID          uuid.UUID `json:"id"`
				Name        string    `json:"name"`
				PriceAmount int64     `json:"price_amount"`
			} `json:"products"`
		} `json:"categories"`
	}
	menuRec := getJSON(t, stack, cookie, "/api/public/v1/menu", &menu)
	require.Equal(t, http.StatusOK, menuRec.Code, menuRec.Body.String())
	require.NotEmpty(t, menu.Categories)
	require.NotEmpty(t, menu.Categories[0].Products)
	assert.Equal(t, f.productID, menu.Categories[0].Products[0].ID)
	assert.Equal(t, f.price, menu.Categories[0].Products[0].PriceAmount)
	for _, forbidden := range []string{"cost_price", "stock", "supplier", "internal_note"} {
		assert.NotContains(t, menuRec.Body.String(), forbidden,
			"the diner-facing menu must never carry internal fields")
	}

	// 3. Order: the body carries a bogus price, which must not survive.
	key := uuid.NewString()
	body := `{"note":"acele","lines":[{"product_id":"` + f.productID.String() +
		`","quantity":2,"unit_price_amount":1,"note":"az tuzlu"}]}`
	rec := postOrder(t, stack, cookie, key, body)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	var placed placedOrderResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &placed))
	assert.Equal(t, 2*f.price, placed.Total, "the total is priced from the read model, not the body")
	assert.Equal(t, string(posdomain.OrderStatusPending), placed.Status)

	// 3b. A retried submission (flaky mobile connection) must replay, not
	// double-order — ADR-SEC-003 on the one mutating public endpoint.
	replay := postOrder(t, stack, cookie, key, body)
	require.Equal(t, http.StatusCreated, replay.Code)
	assert.Equal(t, "true", replay.Header().Get("Idempotency-Replayed"))
	assert.JSONEq(t, rec.Body.String(), replay.Body.String())

	// 4. The rows the ADR is about (§8): a guest check with no person behind
	// it, and both the order and the check marked online_qr while staying
	// dine_in on the channel axis.
	order, err := stack.posOrder.GetByID(ctx, qrTenantID, placed.OrderID)
	require.NoError(t, err)
	assert.Equal(t, posdomain.SourceOnlineQR, order.Source)
	assert.Equal(t, posdomain.OrderChannelDineIn, order.OrderChannel)
	require.Len(t, order.Items, 1)
	assert.Equal(t, f.price, order.Items[0].UnitPriceAmount, "the client's 1 kuruş must not be persisted")
	assert.Equal(t, "az tuzlu", order.Items[0].Note)

	check, err := stack.check.GetByID(ctx, qrTenantID, placed.CheckID)
	require.NoError(t, err)
	assert.Nil(t, check.OpenedBy, "an anonymous diner has no person row (the sentinel UUID is banned)")
	assert.Equal(t, posdomain.OpenedByKindGuestQR, check.OpenedByKind)
	assert.Equal(t, posdomain.SourceOnlineQR, check.Source)

	// 5. The KDS pipeline: the order reaches the kitchen through the SAME
	// outbox event a POS order does, carrying source so the ticket can be
	// marked as a QR order without a second event type.
	assertOrderPlacedEvent(t, ctx, placed.OrderID)

	// 6. Exactly one order exists on the check despite the retry: the replay
	// above must not have produced a second row anywhere.
	ordersOnCheck, err := stack.posOrder.ListByCheck(ctx, qrTenantID, placed.CheckID)
	require.NoError(t, err)
	assert.Len(t, ordersOnCheck, 1, "the idempotent retry must not have created a second order")

	// 7. Staff accepts the order from the KDS.
	accepted, err := stack.posOrder.Accept(ctx, qrTenantID, qrStaffPrincipal(), placed.OrderID, qrStaffID)
	require.NoError(t, err)
	assert.Equal(t, posdomain.OrderStatusAccepted, accepted.Status)

	// 8. The diner polls their own order and sees the new status. The read is
	// scoped to the session that placed it, over the same guarded surface.
	var status struct {
		OrderID uuid.UUID `json:"order_id"`
		Status  string    `json:"status"`
		Total   int64     `json:"total"`
	}
	statusRec := getJSON(t, stack, cookie, "/api/public/v1/orders/"+placed.OrderID.String(), &status)
	require.Equal(t, http.StatusOK, statusRec.Code, statusRec.Body.String())
	assert.Equal(t, placed.OrderID, status.OrderID)
	assert.Equal(t, string(posdomain.OrderStatusAccepted), status.Status)
	assert.Equal(t, placed.Total, status.Total)

	// 9. The cashier settles the check: cash payment + fiscal registration
	// (FISCAL-001 mock adapter), exactly as for a POS-typed order.
	payment, err := stack.payment.RegisterSale(ctx, paymentsvc.RegisterSaleRequest{
		TenantID:       qrTenantID,
		BranchID:       qrBranchID,
		CheckID:        &placed.CheckID,
		IdempotencyKey: "e2e-qr-pay-" + placed.OrderID.String(),
		Method:         paymentdomain.PaymentMethodCash,
		AmountTotal:    placed.Total,
		Currency:       "TRY",
	})
	require.NoError(t, err)
	drainFiscal(t, stack.payment)

	settled, err := stack.payment.GetByID(ctx, qrTenantID, payment.ID)
	require.NoError(t, err)
	assert.Equal(t, paymentdomain.PaymentStatusCompleted, settled.Status)
	require.NotNil(t, settled.FiscalReceiptID, "a QR sale is fiscalised like any other")

	// 10. Closing the check releases the table for cleaning.
	closed, err := stack.check.Close(ctx, qrTenantID, qrStaffPrincipal(), placed.CheckID, qrStaffID)
	require.NoError(t, err)
	assert.Equal(t, posdomain.CheckStatusClosed, closed.Status)

	var tableStatus string
	require.NoError(t, sharedPool.WithTenantReadTx(ctx, qrTenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT status FROM tables WHERE id = $1`, f.table.ID).Scan(&tableStatus)
	}))
	assert.Equal(t, string(posdomain.TableStatusCleaning), tableStatus)
}

// TestStorefrontSpine_RevokedStickerMintsNoSession closes the loop the admin
// side opens: revocation is what makes a printed, unretrievable token safe to
// leave on a table forever (ADR-ARCH-006 §4).
func TestStorefrontSpine_RevokedStickerMintsNoSession(t *testing.T) {
	ctx := context.Background()
	stack := newStorefrontStack(t)
	f := seedQRFixture(t, stack, "QR-2", "Ayran", 3000)

	cookie := startGuestSession(t, stack, f.rawToken)
	require.NotNil(t, cookie)

	codes, err := stack.qr.List(ctx, qrTenantID, qrStaffPrincipal(), qrBranchID)
	require.NoError(t, err)
	var codeID uuid.UUID
	for _, c := range codes {
		if c.TableID == f.table.ID {
			codeID = c.ID
		}
	}
	require.NotEqual(t, uuid.Nil, codeID)

	_, err = stack.qr.Revoke(ctx, qrTenantID, qrStaffPrincipal(), codeID, qrStaffID)
	require.NoError(t, err)

	body, err := json.Marshal(map[string]string{"token": f.rawToken})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/api/public/v1/sessions", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	stack.router.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusNotFound, rec.Code,
		"a revoked sticker must be indistinguishable from a fabricated token")
}

// assertOrderPlacedEvent checks the one event the KDS consumes.
func assertOrderPlacedEvent(t *testing.T, ctx context.Context, orderID uuid.UUID) {
	t.Helper()

	var payload []byte
	require.NoError(t, sharedPool.WithTenantReadTx(ctx, qrTenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT payload FROM pos_outbox
			WHERE aggregate_id = $1 AND event_type = 'order.placed'
		`, orderID.String()).Scan(&payload)
	}))

	var event struct {
		OrderID      uuid.UUID `json:"order_id"`
		BranchID     uuid.UUID `json:"branch_id"`
		OrderChannel string    `json:"order_channel"`
		Source       string    `json:"source"`
		ItemCount    int       `json:"item_count"`
	}
	require.NoError(t, json.Unmarshal(payload, &event))
	assert.Equal(t, orderID, event.OrderID)
	assert.Equal(t, qrBranchID, event.BranchID)
	assert.Equal(t, string(posdomain.SourceOnlineQR), event.Source,
		"the KDS distinguishes a QR ticket by source, not by a separate event type")
	assert.Equal(t, string(posdomain.OrderChannelDineIn), event.OrderChannel)
	assert.Equal(t, 1, event.ItemCount)
}
