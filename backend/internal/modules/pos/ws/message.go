// Package ws implements the kitchen display (KDS) WebSocket fan-out for the
// pos module: a single NATS JetStream consumer per process feeds an in-memory
// hub, which pushes order lifecycle notifications to WebSocket clients
// grouped by tenant+branch "room".
//
// Wire contract (see docs handed to Wave 2 / KDS UI):
//
//	→ client, once per connection, immediately after a successful handshake:
//	  Snapshot{type:"snapshot", orders:[OrderEvent...]}
//	→ client, on every subsequent order lifecycle change:
//	  OrderEvent{type:"order.placed"|"order.status_changed", ...}
//
// OrderEvent never carries item/product detail (ADR-DATA-002: no event
// payload enrichment) — the client fetches full detail via the existing
// REST GET /api/v1/pos/orders/{id} or GET /api/v1/pos/checks/{id}/orders.
package ws

import (
	"time"

	"github.com/google/uuid"
)

// MessageType values sent to kitchen WS clients.
const (
	TypeSnapshot           = "snapshot"
	TypeOrderPlaced        = "order.placed"
	TypeOrderStatusChanged = "order.status_changed"
)

// OrderEvent is one order lifecycle notification, and is also the element
// type of Snapshot.Orders. Fields mirror the pos "orders" table's routing
// columns only — never line items (see package doc).
type OrderEvent struct {
	Type       string     `json:"type"`
	OrderID    uuid.UUID  `json:"order_id"`
	CheckID    *uuid.UUID `json:"check_id,omitempty"`
	TableLabel string     `json:"table_label,omitempty"`
	Status     string     `json:"status"`

	// Seq is the JetStream stream sequence number of the event that produced
	// this message (0 for snapshot rows, which have no single originating
	// event). Clients use it to detect gaps/ordering across reconnects; the
	// snapshot itself is the authoritative recovery mechanism for any gap.
	Seq uint64 `json:"seq"`

	// OccurredAt is the NATS message's server timestamp for live events, or
	// the order's last-updated time for snapshot rows.
	OccurredAt time.Time `json:"occurred_at"`
}

// Snapshot is sent exactly once, immediately after a successful handshake,
// listing every order currently "live" for the kitchen (pending/accepted/
// preparing) in the requested branch. It lets a (re)connecting KDS client
// rebuild state without having missed any NATS-driven events while
// disconnected.
type Snapshot struct {
	Type   string       `json:"type"`
	Orders []OrderEvent `json:"orders"`
}

func newSnapshot(orders []OrderEvent) Snapshot {
	if orders == nil {
		orders = []OrderEvent{}
	}
	return Snapshot{Type: TypeSnapshot, Orders: orders}
}
