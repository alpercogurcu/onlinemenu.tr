package ws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"
	"go.uber.org/fx"
	"go.uber.org/zap"

	"onlinemenu.tr/internal/modules/pos/domain"
	pub "onlinemenu.tr/internal/modules/pos/public"
	"onlinemenu.tr/internal/modules/pos/service"
	"onlinemenu.tr/internal/platform/eventbus"
)

// orderEventSubjectFilter matches every order-lifecycle subject the pos
// outbox dispatcher publishes: "pos.order.<event>.v1" for event in
// {placed, accepted, rejected, status_changed} — see
// platform/outbox/dispatcher.go toSubject. It intentionally does NOT match
// "pos.check.*.v1" subjects.
//
// NOTE: this is a 4-token subject (module.eventType.v1), NOT the 5-token
// "<module>.<eventType>.<v>.<tenant_id>" scheme described in
// docs/architecture.md — the outbox dispatcher does not embed tenant_id in
// the subject today. Routing by tenant/branch is therefore done in-process
// after loading the order (see handleOrderEvent), not via subject filtering.
const orderEventSubjectFilter = "pos.order.*.v1"

// kitchenWSDurable is the single, shared JetStream durable consumer name for
// this fan-out. One consumer per pos deployment group — NOT one per
// WebSocket connection — feeds every connected kitchen display.
//
// Known limitation (flag for Wave 2 / scale-out): if multiple api-pos
// replicas run concurrently, JetStream's pull-based Consume() competes for
// messages across processes bound to the same durable name, so an event
// may be delivered to only one replica — WebSocket clients connected to the
// other replicas would miss it until their next snapshot (on reconnect).
// Fixing this properly needs either per-instance ephemeral consumers with
// DeliverPolicy=DeliverNew (platform/eventbus.Subscribe does not expose
// DeliverPolicy today) or a cross-instance relay (e.g. Redis pub/sub) — both
// out of scope here (task scoped this change to modules/pos/**). Faz 1 runs
// a single cmd/api process, so this does not affect current deployments.
const kitchenWSDurable = "pos-kitchen-ws-fanout"

// Hub fans NATS order-lifecycle events out to kitchen WebSocket connections,
// grouped into rooms by (tenant_id, branch_id). It holds exactly one NATS
// JetStream subscription regardless of how many WebSocket connections are
// registered (requirement: no per-connection NATS subscription).
type Hub struct {
	bus    *eventbus.Bus
	orders *service.OrderService
	checks *service.CheckService
	logger *zap.Logger
	cfg    Config

	mu     sync.RWMutex
	rooms  map[roomKey]map[uuid.UUID]*conn
	timing timing

	// subCancel stops the NATS subscription's per-subscription watcher
	// goroutine on OnStop. Set once, in Register's OnStart hook.
	subCancel context.CancelFunc
}

type roomKey struct {
	tenantID uuid.UUID
	branchID uuid.UUID
}

// Params groups fx-injected dependencies.
type Params struct {
	fx.In

	Bus    *eventbus.Bus
	Orders *service.OrderService
	Checks *service.CheckService
	Logger *zap.Logger
	Config Config
}

// Config carries deployment-specific WebSocket settings. Provided by the
// binary's composition root (cmd/*), never read from the environment here.
type Config struct {
	// AllowedOriginPatterns is passed to websocket.AcceptOptions.
	// Empty means coder/websocket's strict same-origin default, which
	// rejects any browser client served from a different origin (e.g. the
	// admin-hosted KDS page in dev on :3000 talking to the API on :8080).
	AllowedOriginPatterns []string
}

// NewHub constructs the kitchen WS hub. It does not yet subscribe to NATS or
// accept connections — call Register to wire it into the fx lifecycle.
func NewHub(p Params) *Hub {
	return &Hub{
		bus:    p.Bus,
		orders: p.Orders,
		checks: p.Checks,
		logger: p.Logger,
		cfg:    p.Config,
		rooms:  make(map[roomKey]map[uuid.UUID]*conn),
		timing: defaultTiming(),
	}
}

// Register wires the Hub's NATS subscription (OnStart) and connection drain
// (OnStop) into the fx lifecycle.
//
// The subscription is handed a dedicated long-lived context (NOT the fx
// start-up context — that is short-lived and would cancel the subscription
// almost immediately; NOT a request-scoped context either — see
// eventbus.Bus.Subscribe's ctx-lifetime warning, either of those would leak
// or misbehave) that Hub itself owns and cancels on OnStop. This is stricter
// than identity/events.Subscriber's context.Background() (which relies
// solely on Bus.OnStop's stopAllConsumers to detach the NATS consumer, and
// leaves Subscribe's internal per-subscription watcher goroutine — the one
// that calls cc.Stop() when its ctx is done — parked until process exit):
// Hub explicitly cancels its own subscription context so that watcher
// goroutine also exits during graceful shutdown, not just at process death.
func (h *Hub) Register(lc fx.Lifecycle) {
	lc.Append(fx.Hook{
		OnStart: func(_ context.Context) error {
			subCtx, cancel := context.WithCancel(context.Background())
			h.subCancel = cancel
			if err := h.bus.Subscribe(subCtx, orderEventSubjectFilter, kitchenWSDurable, h.handleOrderEvent); err != nil { //nolint:contextcheck // subCtx is Hub's own long-lived subscription context (see comment above), not the short-lived fx OnStart context.
				cancel()
				return fmt.Errorf("pos/ws: subscribe to order events: %w", err)
			}
			h.logger.Info("pos/ws: kitchen hub subscribed", zap.String("subject", orderEventSubjectFilter))
			return nil
		},
		OnStop: func(_ context.Context) error {
			if h.subCancel != nil {
				h.subCancel()
			}
			h.drain()
			return nil
		},
	})
}

// drain closes every currently registered connection so shutdown does not
// leave dangling WebSocket goroutines or half-open sockets behind. Clients
// are expected to reconnect (to a new instance, in a rolling deploy) and
// recover state via the snapshot message.
func (h *Hub) drain() {
	h.mu.Lock()
	defer h.mu.Unlock()

	for key, conns := range h.rooms {
		for _, c := range conns {
			c.cancel()
		}
		delete(h.rooms, key)
	}
}

func (h *Hub) register(c *conn) {
	h.mu.Lock()
	defer h.mu.Unlock()

	key := roomKey{tenantID: c.tenantID, branchID: c.branchID}
	room := h.rooms[key]
	if room == nil {
		room = make(map[uuid.UUID]*conn)
		h.rooms[key] = room
	}
	room[c.id] = c
}

func (h *Hub) unregister(c *conn) {
	h.mu.Lock()
	defer h.mu.Unlock()

	key := roomKey{tenantID: c.tenantID, branchID: c.branchID}
	room := h.rooms[key]
	if room == nil {
		return
	}
	delete(room, c.id)
	if len(room) == 0 {
		delete(h.rooms, key)
	}
}

// broadcast fans one message out to every connection currently registered
// for tenantID/branchID. Never blocks: a connection whose outbound buffer is
// full is dropped (see conn.enqueue).
func (h *Hub) broadcast(tenantID, branchID uuid.UUID, evt OrderEvent) {
	data, err := json.Marshal(evt)
	if err != nil {
		h.logger.Error("pos/ws: marshal order event", zap.Error(err))
		return
	}

	key := roomKey{tenantID: tenantID, branchID: branchID}
	h.mu.RLock()
	defer h.mu.RUnlock()

	for _, c := range h.rooms[key] {
		c.enqueue(data)
	}
}

// buildSnapshot loads the branch's currently-live orders and their table
// labels (best-effort — see snapshotTableLabels) for the snapshot message
// sent immediately after a successful handshake.
//
// Table labels are resolved with ONE batched lookup for the whole snapshot,
// not one per order: a busy branch can have hundreds of live orders, and the
// per-order CheckService.GetByID this used to call cost a full
// BEGIN/SET LOCAL/SELECT/COMMIT round-trip each (measured ~22ms/order on a
// dev branch with 738 live orders — a 16s first paint on the KDS).
func (h *Hub) buildSnapshot(ctx context.Context, tenantID, branchID uuid.UUID) (Snapshot, error) {
	orders, err := h.orders.ListActiveByBranch(ctx, tenantID, branchID)
	if err != nil {
		return Snapshot{}, fmt.Errorf("pos/ws: list active orders: %w", err)
	}

	labels := h.snapshotTableLabels(ctx, tenantID, orders)

	events := make([]OrderEvent, 0, len(orders))
	for _, o := range orders {
		label := ""
		if o.CheckID != nil {
			label = labels[*o.CheckID]
		}
		events = append(events, OrderEvent{
			Type:       eventTypeForStatus(o.Status),
			OrderID:    o.ID,
			CheckID:    o.CheckID,
			TableLabel: label,
			Source:     string(o.Source),
			Status:     string(o.Status),
			Seq:        0,
			OccurredAt: o.UpdatedAt,
		})
	}
	return newSnapshot(events), nil
}

// snapshotTableLabels resolves the table label of every dine-in check
// referenced by orders, in a single batched query. Takeaway/delivery orders
// carry no check_id and are skipped; ids are de-duplicated because several
// orders (rounds) commonly share one check.
//
// Best-effort, exactly like the per-order lookup it replaces: a read failure
// is logged and yields an empty map, so the snapshot still goes out with
// every row missing only its table label rather than the client getting no
// snapshot at all.
func (h *Hub) snapshotTableLabels(ctx context.Context, tenantID uuid.UUID, orders []domain.Order) map[uuid.UUID]string {
	seen := make(map[uuid.UUID]struct{}, len(orders))
	ids := make([]uuid.UUID, 0, len(orders))
	for _, o := range orders {
		if o.CheckID == nil {
			continue
		}
		if _, dup := seen[*o.CheckID]; dup {
			continue
		}
		seen[*o.CheckID] = struct{}{}
		ids = append(ids, *o.CheckID)
	}
	if len(ids) == 0 {
		return map[uuid.UUID]string{}
	}

	labels, err := h.checks.TableLabelsByIDs(ctx, tenantID, ids)
	if err != nil {
		h.logger.Warn("pos/ws: resolve snapshot table_labels failed",
			zap.Int("check_count", len(ids)), zap.Error(err))
		return map[uuid.UUID]string{}
	}
	return labels
}

// tableLabel best-effort resolves a check's table label for dine-in orders.
// Takeaway/delivery orders have no check_id and get no table label. A
// transient read failure here must not fail the whole snapshot/broadcast —
// it only means one row is missing its table label, which the client can
// still show grouped by order/check id.
func (h *Hub) tableLabel(ctx context.Context, tenantID uuid.UUID, checkID *uuid.UUID) string {
	if checkID == nil {
		return ""
	}
	c, err := h.checks.GetByID(ctx, tenantID, *checkID)
	if err != nil {
		if !errors.Is(err, pub.ErrNotFound) {
			h.logger.Warn("pos/ws: resolve table_label failed",
				zap.String("check_id", checkID.String()), zap.Error(err))
		}
		return ""
	}
	return c.TableLabel
}

// handleOrderEvent is the eventbus.HandlerFunc for orderEventSubjectFilter.
// It reloads the order from the database (the outbox payload for
// accept/reject/status_changed carries only order_id + tenant_id — see
// order_service.go — not branch_id/status/table_label) so a single read path
// gives routing (branch_id), current status, and table_label uniformly for
// every order event type. tenant_id comes from the (immutable, trusted —
// pos's own outbox) event payload, so no principal/authz is needed for this
// internal read.
//
// Returning an error NAKs the message for redelivery (eventbus.go); this is
// only appropriate for transient failures (DB unreachable). A message whose
// payload cannot be parsed, or whose order_id no longer resolves to a row,
// is acked (nil) and logged instead — retrying either forever would starve
// redelivery of every subsequent event behind it.
func (h *Hub) handleOrderEvent(ctx context.Context, msg jetstream.Msg) error {
	var payload struct {
		TenantID string `json:"tenant_id"`
		OrderID  string `json:"order_id"`
	}
	if err := json.Unmarshal(msg.Data(), &payload); err != nil {
		h.logger.Error("pos/ws: unmarshal order event payload", zap.String("subject", msg.Subject()), zap.Error(err))
		return nil
	}

	tenantID, err := uuid.Parse(payload.TenantID)
	if err != nil {
		h.logger.Error("pos/ws: parse tenant_id", zap.String("subject", msg.Subject()), zap.Error(err))
		return nil
	}
	orderID, err := uuid.Parse(payload.OrderID)
	if err != nil {
		h.logger.Error("pos/ws: parse order_id", zap.String("subject", msg.Subject()), zap.Error(err))
		return nil
	}

	order, err := h.orders.GetByID(ctx, tenantID, orderID)
	if err != nil {
		if errors.Is(err, pub.ErrNotFound) {
			h.logger.Warn("pos/ws: order not found for kitchen event, dropping",
				zap.String("order_id", orderID.String()), zap.String("subject", msg.Subject()))
			return nil
		}
		return fmt.Errorf("pos/ws: load order %s: %w", orderID, err)
	}

	meta, err := msg.Metadata()
	if err != nil {
		return fmt.Errorf("pos/ws: read message metadata: %w", err)
	}

	evt := OrderEvent{
		Type:       eventTypeForSubject(msg.Subject()),
		OrderID:    order.ID,
		CheckID:    order.CheckID,
		TableLabel: h.tableLabel(ctx, tenantID, order.CheckID),
		Source:     string(order.Source),
		Status:     string(order.Status),
		Seq:        meta.Sequence.Stream,
		OccurredAt: meta.Timestamp,
	}
	h.broadcast(tenantID, order.BranchID, evt)
	return nil
}

// eventTypeForSubject maps a NATS subject ("pos.order.placed.v1", ...) to
// the WS wire type: order.placed is its own type, every other order
// transition (accepted/rejected/status_changed) is normalized to
// order.status_changed — the client always re-reads current status from the
// message's Status field, not from the type.
func eventTypeForSubject(subject string) string {
	parts := strings.Split(subject, ".")
	if len(parts) >= 3 && parts[2] == "placed" {
		return TypeOrderPlaced
	}
	return TypeOrderStatusChanged
}

// eventTypeForStatus picks the snapshot row's wire type from the order's
// current status (there is no originating subject for a snapshot row).
func eventTypeForStatus(status domain.OrderStatus) string {
	if status == domain.OrderStatusPending {
		return TypeOrderPlaced
	}
	return TypeOrderStatusChanged
}
