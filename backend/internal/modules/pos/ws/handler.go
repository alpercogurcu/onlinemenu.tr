package ws

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"onlinemenu.tr/internal/modules/pos/service"
	"onlinemenu.tr/internal/platform/auth"
)

// KitchenWSPath is the kitchen display WebSocket endpoint. Mounted under the
// same chi.Mux and same global auth.Middleware as every other pos route —
// the WebSocket handshake is a plain GET request and carries the same
// Authorization: Bearer <token> header as any REST call (no query-param
// token: query strings end up in proxy/access logs).
const KitchenWSPath = "/api/v1/pos/ws/kitchen"

// RegisterRoutes mounts the kitchen WebSocket endpoint, gated by the same
// OPA permission ("pos.order.read") every other order-reading route uses
// (ADR-AUTH-001 layer 2). Branch-level authorization (layer 3) is checked
// per-connection in ServeKitchenWS, once the branch_id query parameter is
// known.
func (h *Hub) RegisterRoutes(r *chi.Mux, engine *auth.Engine) {
	r.With(auth.RequirePermission(engine, "pos.order.read")).Get(KitchenWSPath, h.ServeKitchenWS)
}

// ServeKitchenWS upgrades an authorized request to a WebSocket connection,
// registers it in the tenant+branch room, sends the initial snapshot, and
// then serves the connection until it disconnects, times out on heartbeat,
// or is dropped for backpressure.
func (h *Hub) ServeKitchenWS(w http.ResponseWriter, r *http.Request) {
	principal, err := auth.FromContext(r.Context())
	if err != nil || principal.TenantID == uuid.Nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	branchID, err := uuid.Parse(r.URL.Query().Get("branch_id"))
	if err != nil || branchID == uuid.Nil {
		http.Error(w, "branch_id is required", http.StatusUnprocessableEntity)
		return
	}

	if err := service.RequireBranchAccess(r.Context(), principal, branchID); err != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	// Origin policy: empty AllowedOriginPatterns keeps coder/websocket's
	// strict same-origin default (non-browser clients send no Origin header
	// and pass either way). Browser-hosted KDS pages on a different origin
	// (dev: admin on :3000 vs API on :8080) require the deployment to set
	// POS_WS_ALLOWED_ORIGINS — wired in cmd/* via ws.Config, never read from
	// the environment here.
	var acceptOpts *websocket.AcceptOptions
	if len(h.cfg.AllowedOriginPatterns) > 0 {
		acceptOpts = &websocket.AcceptOptions{OriginPatterns: h.cfg.AllowedOriginPatterns}
	}
	wsConn, err := websocket.Accept(w, r, acceptOpts)
	if err != nil {
		h.logger.Warn("pos/ws: accept failed", zap.Error(err))
		return
	}

	// The connection's lifetime is independent of this handler goroutine's
	// caller (the HTTP server) beyond the initial request — it is exactly
	// the WebSocket connection's own lifetime, so a dedicated
	// context.WithCancel (not eventbus's request-scoped-ctx pitfall, which
	// is about a *subscription* outliving a single request) is correct here:
	// this handler blocks for the connection's whole lifetime.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// CloseRead spawns the internal read loop that discards any (unexpected)
	// client data frames and answers control frames (ping/pong/close)
	// automatically — required for Ping (heartbeatPump) to observe pongs,
	// and for a client-initiated close to be detected.
	readDone := wsConn.CloseRead(ctx)

	// The snapshot is written directly to the socket, sequentially, BEFORE
	// this connection is registered in its room and BEFORE writePump starts.
	// Registering first would let a concurrent Hub.broadcast enqueue into
	// c.send while buildSnapshot's DB round-trips are still in flight; under
	// load that queue could fill past sendBufferSize before writePump (the
	// only drainer) ever starts, and the subsequent snapshot send — a raw
	// channel send, not a select — would then block the connection
	// goroutine forever (requirement #4 explicitly forbids ever blocking).
	// Writing directly here also guarantees the snapshot is the first frame
	// on the wire. The tradeoff: an event committed between buildSnapshot's
	// read and register is not broadcast to this connection — negligible
	// and self-correcting (the next status change re-broadcasts), and
	// strictly better than the alternative (register first), where a live
	// event could race ahead of the full-state snapshot on the wire.
	snapshot, err := h.buildSnapshot(ctx, principal.TenantID, branchID) //nolint:contextcheck // ctx is the connection's own lifetime (see comment above), not r.Context() — the connection outlives this request.
	if err != nil {
		h.logger.Error("pos/ws: build snapshot failed", zap.Error(err))
		wsConn.Close(websocket.StatusInternalError, "snapshot failed")
		return
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		h.logger.Error("pos/ws: marshal snapshot failed", zap.Error(err))
		wsConn.Close(websocket.StatusInternalError, "snapshot failed")
		return
	}
	wctx, wcancel := context.WithTimeout(ctx, h.timing.writeTimeout)
	err = wsConn.Write(wctx, websocket.MessageText, data) //nolint:contextcheck // wctx derives from the connection's own ctx (not r.Context()), consistent with the rest of the connection's lifetime.
	wcancel()
	if err != nil {
		h.logger.Warn("pos/ws: snapshot write failed", zap.Error(err))
		return
	}

	c := newConn(principal.TenantID, branchID, wsConn, cancel, h.logger, h.timing)
	h.register(c)
	defer h.unregister(c)

	go c.writePump(ctx)     //nolint:contextcheck // ctx is the connection's own lifetime; the pump must keep running past this request handler's return.
	go c.heartbeatPump(ctx) //nolint:contextcheck // same connection-lifetime ctx as writePump above.

	<-readDone.Done()
	cancel()
	wsConn.Close(websocket.StatusNormalClosure, "")
}
