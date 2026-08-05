package http

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"go.uber.org/fx"
	"go.uber.org/zap"

	pospub "onlinemenu.tr/internal/modules/pos/public"
	"onlinemenu.tr/internal/modules/storefront/domain"
	pub "onlinemenu.tr/internal/modules/storefront/public"
	"onlinemenu.tr/internal/modules/storefront/service"
	"onlinemenu.tr/internal/platform/auth"
	"onlinemenu.tr/internal/platform/httpx"
)

// PublicAPIPrefix is the one path prefix served without the staff auth chain.
// cmd/api's global middleware and its tracing filter both key on this
// constant rather than a copied string literal, so the exemption and the
// guard chain below can never drift apart.
const PublicAPIPrefix = "/api/public/v1"

// Config carries the deployment-specific knobs of the public surface.
// Values come from the composition root (cmd/api), never from os.Getenv here.
type Config struct {
	// CookieSecure marks the guest cookie Secure. It is false only in dev,
	// where the menu app talks plain HTTP to localhost.
	CookieSecure bool
	// IPRateLimit bounds anonymous traffic per source address; GuestRateLimit
	// bounds one established session. Zero values fall back to the defaults
	// below so a partially-configured deployment is still limited.
	IPRateLimit    httpx.RateLimitConfig
	GuestRateLimit httpx.RateLimitConfig
}

// Submission bounds for the one mutating public endpoint. They are generous
// for a real table and hostile to a script: a rate limit caps how OFTEN an
// anonymous caller may submit, only these cap how much work one submission
// can cost (rows inserted, lookups performed, text stored).
const (
	maxOrderBodyBytes = 64 << 10 // 64 KiB
	maxCartLines      = 60
	maxLineQuantity   = 99
	maxLineModifiers  = 20
	maxNoteLength     = 500
)

// Defaults sized for a table of diners browsing a menu: generous enough that
// no real session ever notices, small enough to make scripted enumeration of
// QR tokens or order ids expensive.
var (
	defaultIPRateLimit    = httpx.RateLimitConfig{Limit: 120, Window: time.Minute}
	defaultGuestRateLimit = httpx.RateLimitConfig{Limit: 60, Window: time.Minute}
)

// PublicHandler serves the anonymous QR diner surface.
type PublicHandler struct {
	sessions *service.SessionService
	menu     *service.MenuService
	orders   *service.OrderService
	signer   *auth.GuestTokenSigner
	cfg      Config
	logger   *zap.Logger
}

// PublicParams groups fx-injected dependencies.
type PublicParams struct {
	fx.In

	Sessions *service.SessionService
	Menu     *service.MenuService
	Orders   *service.OrderService
	Signer   *auth.GuestTokenSigner
	Cache    *redis.Client
	Config   Config `optional:"true"`
	Logger   *zap.Logger
}

// PublicHandlerWithCache pairs the handler with the Redis client its rate
// limit and idempotency middleware need (mirrors pos's HandlerWithCache).
type PublicHandlerWithCache struct {
	h     *PublicHandler
	cache *redis.Client
}

func NewPublicHandler(p PublicParams) *PublicHandlerWithCache {
	cfg := p.Config
	if cfg.IPRateLimit.Limit <= 0 || cfg.IPRateLimit.Window <= 0 {
		cfg.IPRateLimit = defaultIPRateLimit
	}
	if cfg.GuestRateLimit.Limit <= 0 || cfg.GuestRateLimit.Window <= 0 {
		cfg.GuestRateLimit = defaultGuestRateLimit
	}
	return &PublicHandlerWithCache{
		h: &PublicHandler{
			sessions: p.Sessions,
			menu:     p.Menu,
			orders:   p.Orders,
			signer:   p.Signer,
			cfg:      cfg,
			logger:   p.Logger,
		},
		cache: p.Cache,
	}
}

// RegisterPublicRoutes mounts the unauthenticated storefront surface.
//
// cmd/api exempts this prefix from the staff auth middleware, so the guard
// chain here IS the security boundary — there is nothing behind it:
//
//   - PublicIPRateLimit on the whole group, including /sessions, which is the
//     only endpoint an attacker can reach without a session and therefore the
//     one a QR-token brute force would hammer;
//   - requireGuestSession on everything else — the exemption of /sessions is
//     enforced by structure (a separate chi.Group), so a new route added to
//     the wrong group is caught by public_guard_smoke_test.go, not by review;
//   - GuestRateLimit inside the guarded group, keyed on the session, so one
//     diner cannot spend a whole restaurant NAT's IP budget;
//   - Idempotency on the single mutating endpoint (ADR-SEC-003), scoped to
//     the guest session rather than to a tenant principal that does not exist
//     here.
func (hwc *PublicHandlerWithCache) RegisterPublicRoutes(r *chi.Mux) {
	h := hwc.h
	r.Route(PublicAPIPrefix, func(r chi.Router) {
		r.Use(httpx.PublicIPRateLimit(hwc.cache, h.cfg.IPRateLimit))

		// Token in the BODY — never in the path (ADR-ARCH-006 §3).
		r.Post("/sessions", h.startSession)

		r.Group(func(r chi.Router) {
			r.Use(h.requireGuestSession)
			r.Use(httpx.GuestRateLimit(hwc.cache, h.cfg.GuestRateLimit))

			r.Get("/menu", h.getMenu)
			r.With(httpx.IdempotencyWithScope(hwc.cache, httpx.GuestSessionScope)).
				Post("/orders", h.placeOrder)
			r.Get("/orders", h.listMyOrders)
			r.Get("/orders/{id}", h.getOrderStatus)
		})
	})
}

// startSession exchanges a scanned QR token for a guest session cookie.
//
// Every failure mode of the exchange — unknown token, revoked token,
// deleted/moved table — answers the same 404. The service distinguishes and
// logs them; the caller must not be able to tell a real retired token from a
// fabricated one, or the endpoint becomes a token oracle.
func (h *PublicHandler) startSession(w http.ResponseWriter, r *http.Request) {
	var req startSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Token == "" {
		writeProblem(w, http.StatusBadRequest, codeInvalidRequest,
			"Geçersiz istek. QR kodu tekrar okutmayı deneyin.")
		return
	}

	resolved, err := h.sessions.ResolveToken(r.Context(), req.Token)
	if err != nil {
		if errors.Is(err, pub.ErrQRNotFound) || errors.Is(err, pub.ErrQRRevoked) {
			writeProblem(w, http.StatusNotFound, codeNotFound,
				"Bu QR kod geçerli değil. Lütfen personelden yardım isteyin.")
			return
		}
		h.internal(w, "start session", err)
		return
	}

	expiresAt := guestSessionExpiry(h.signer, resolved.Token)
	setGuestCookie(w, resolved.Token, expiresAt, h.cfg.CookieSecure)
	writeJSON(w, http.StatusOK, sessionResponse{
		BranchID:   resolved.BranchID,
		TableID:    resolved.TableID,
		TableLabel: resolved.TableLabel,
		ExpiresAt:  expiresAt,
	})
}

// guestSessionExpiry reads the expiry back out of the freshly issued token so
// the cookie's lifetime and the token's cannot drift: the TTL is owned by the
// signer, and re-deriving it here would duplicate that decision.
func guestSessionExpiry(signer *auth.GuestTokenSigner, token string) time.Time {
	session, err := signer.VerifyGuest(token)
	if err != nil {
		// Unreachable: the token was signed microseconds ago by this signer.
		return time.Now()
	}
	return session.ExpiresAt
}

// getMenu returns the branch's menu for the scanned table.
//
// Cache-Control is private: the response is per-branch and travels with a
// session cookie, so a shared cache must never keep it. 30 s is short enough
// that an 86'd product disappears promptly and long enough to absorb the
// reload burst of a table that just sat down.
func (h *PublicHandler) getMenu(w http.ResponseWriter, r *http.Request) {
	guest, ok := guestFromRequest(w, r)
	if !ok {
		return
	}

	categories, err := h.menu.GetMenu(r.Context(), guest)
	if err != nil {
		h.internal(w, "get menu", err)
		return
	}

	w.Header().Set("Cache-Control", "private, max-age=30")
	writeJSON(w, http.StatusOK, toMenuResponse(categories))
}

// placeOrder re-prices the submitted cart and puts it on the table's check.
func (h *PublicHandler) placeOrder(w http.ResponseWriter, r *http.Request) {
	guest, ok := guestFromRequest(w, r)
	if !ok {
		return
	}

	// The body is capped before it is decoded: this endpoint is reachable by
	// anyone with a QR photo, and an unbounded JSON body would be an
	// allocation lever no rate limit can take back.
	var req placeOrderRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxOrderBodyBytes)).Decode(&req); err != nil {
		writeProblem(w, http.StatusBadRequest, codeInvalidRequest, "Geçersiz istek gövdesi.")
		return
	}
	if len(req.Lines) == 0 {
		writeProblem(w, http.StatusUnprocessableEntity, codeValidation, "Sepetiniz boş.")
		return
	}
	if len(req.Lines) > maxCartLines {
		writeProblem(w, http.StatusUnprocessableEntity, codeValidation,
			"Sepetinizde çok fazla kalem var. Lütfen siparişi bölerek gönderin.")
		return
	}
	if len(req.Note) > maxNoteLength {
		writeProblem(w, http.StatusUnprocessableEntity, codeValidation, "Sipariş notu çok uzun.")
		return
	}

	cart := domain.GuestCart{Note: req.Note, Lines: make([]domain.GuestCartLine, len(req.Lines))}
	for i, l := range req.Lines {
		// Every bound here is a row or an allocation the diner would
		// otherwise choose the size of: quantity becomes an order_items
		// value, modifier_ids become lookups, note becomes stored text.
		switch {
		case l.ProductID == uuid.Nil, l.Quantity < 1, l.Quantity > maxLineQuantity:
			writeProblem(w, http.StatusUnprocessableEntity, codeValidation,
				"Sepetteki ürün bilgisi geçersiz.")
			return
		case len(l.ModifierIDs) > maxLineModifiers:
			writeProblem(w, http.StatusUnprocessableEntity, codeValidation,
				"Bir üründe çok fazla seçenek işaretlendi.")
			return
		case len(l.Note) > maxNoteLength:
			writeProblem(w, http.StatusUnprocessableEntity, codeValidation, "Ürün notu çok uzun.")
			return
		}
		cart.Lines[i] = domain.GuestCartLine{
			ProductID:   l.ProductID,
			Quantity:    l.Quantity,
			ModifierIDs: l.ModifierIDs,
			Note:        l.Note,
		}
	}

	placed, err := h.orders.Place(r.Context(), guest, cart)
	if err != nil {
		h.placementError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, placeOrderResponse{
		OrderID: placed.OrderID,
		CheckID: placed.CheckID,
		Status:  placed.Status,
		Total:   placed.Total,
	})
}

func (h *PublicHandler) listMyOrders(w http.ResponseWriter, r *http.Request) {
	guest, ok := guestFromRequest(w, r)
	if !ok {
		return
	}

	orders, err := h.orders.ListMine(r.Context(), guest)
	if err != nil {
		h.internal(w, "list orders", err)
		return
	}

	resp := orderListResponse{Orders: make([]orderStatusResponse, 0, len(orders))}
	for _, o := range orders {
		resp.Orders = append(resp.Orders, toOrderStatusResponse(o))
	}
	writeJSON(w, http.StatusOK, resp)
}

// getOrderStatus answers only for orders THIS session placed. A mismatch is a
// 404, not a 403: a diner must not be able to confirm that someone else's
// order id exists.
func (h *PublicHandler) getOrderStatus(w http.ResponseWriter, r *http.Request) {
	guest, ok := guestFromRequest(w, r)
	if !ok {
		return
	}

	orderID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeProblem(w, http.StatusNotFound, codeNotFound, "Sipariş bulunamadı.")
		return
	}

	order, err := h.orders.GetMine(r.Context(), guest, orderID)
	if err != nil {
		if errors.Is(err, pub.ErrGuestForbidden) || errors.Is(err, pub.ErrNotFound) {
			writeProblem(w, http.StatusNotFound, codeNotFound, "Sipariş bulunamadı.")
			return
		}
		h.internal(w, "get order status", err)
		return
	}
	writeJSON(w, http.StatusOK, toOrderStatusResponse(order))
}

// placementError maps the order path's known conditions to diner-facing
// answers. Each 409 carries a distinct code because the advice differs:
// "wait a few minutes" versus "call staff".
func (h *PublicHandler) placementError(w http.ResponseWriter, err error) {
	var validation *pub.ValidationError
	switch {
	case errors.As(err, &validation):
		writeProblem(w, http.StatusUnprocessableEntity, codeValidation, validation.Msg)
	case errors.Is(err, pospub.ErrTableNotReady):
		writeProblem(w, http.StatusConflict, codeTableNotReady,
			"Masanız hazırlanıyor, birkaç dakika içinde tekrar deneyin.")
	case errors.Is(err, pospub.ErrTableOccupied):
		writeProblem(w, http.StatusConflict, codeTableOccupied,
			"Masanız şu anda sipariş alınabilir durumda değil. Lütfen personele danışın.")
	case errors.Is(err, pospub.ErrTableBranchMismatch), errors.Is(err, pospub.ErrTableNotFound):
		writeProblem(w, http.StatusConflict, codeTableMismatch,
			"Bu QR kod artık bu masaya ait değil. Lütfen personele danışın.")
	default:
		h.internal(w, "place order", err)
	}
}

// internal logs the real error server-side and returns a body that says
// nothing about it.
func (h *PublicHandler) internal(w http.ResponseWriter, op string, err error) {
	h.logger.Error("storefront public handler error", zap.String("op", op), zap.Error(err))
	writeProblem(w, http.StatusInternalServerError, codeInternal,
		"Beklenmeyen bir hata oluştu, lütfen tekrar deneyin.")
}
