// Pure, framework-free state layer for the kitchen display (KDS) event
// stream. Kept separate from the transport (use-kitchen-stream.ts) and the
// UI (pos/kitchen/page.tsx) so the merge/last-writer-wins logic is testable
// without a browser or a WebSocket.
//
// Wire contract mirrors backend/internal/modules/pos/ws/message.go exactly.
import type { OrderStatus } from "@/types"

export type KitchenEventType = "snapshot" | "order.placed" | "order.status_changed"

export interface KitchenOrderEvent {
  type: KitchenEventType
  order_id: string
  check_id?: string | null
  table_label?: string
  status: OrderStatus
  seq: number
  occurred_at: string
}

export interface KitchenSnapshotMessage {
  type: "snapshot"
  orders: KitchenOrderEvent[]
}

export type KitchenMessage = KitchenSnapshotMessage | KitchenOrderEvent

export function isSnapshotMessage(msg: KitchenMessage): msg is KitchenSnapshotMessage {
  return msg.type === "snapshot"
}

// KitchenOrder is the per-order state the KDS board renders. "isNew" is
// UI-only metadata (not part of the wire contract) that the reducer sets on
// every order created by a live order.placed event (never on snapshot rows,
// which are pre-existing orders as of connect time) so the board can apply a
// transient highlight and clear it after a UI-controlled delay.
export interface KitchenOrder {
  orderId: string
  checkId: string | null
  tableLabel: string
  status: OrderStatus
  seq: number
  occurredAt: string
  isNew: boolean
}

export type KitchenOrderMap = Map<string, KitchenOrder>

// Statuses still relevant to a kitchen board. Once an order leaves this set
// (delivered/rejected/cancelled) it is dropped from the map entirely — the
// KDS is a "what still needs cooking/serving" view, not an order history.
const ACTIVE_STATUSES: ReadonlySet<OrderStatus> = new Set([
  "pending",
  "accepted",
  "preparing",
  "ready",
])

export function isActiveKitchenStatus(status: OrderStatus): boolean {
  return ACTIVE_STATUSES.has(status)
}

function toKitchenOrder(evt: KitchenOrderEvent, isNew: boolean): KitchenOrder {
  return {
    orderId: evt.order_id,
    checkId: evt.check_id ?? null,
    tableLabel: evt.table_label ?? "",
    status: evt.status,
    seq: evt.seq,
    occurredAt: evt.occurred_at,
    isNew,
  }
}

// applyKitchenMessage mutates `map` in place (callers own the copy-on-write
// policy) and returns the id of the order that should receive a "new order"
// highlight, or null if this message did not introduce one.
export function applyKitchenMessage(map: KitchenOrderMap, msg: KitchenMessage): string | null {
  if (isSnapshotMessage(msg)) {
    map.clear()
    for (const evt of msg.orders) {
      if (isActiveKitchenStatus(evt.status)) {
        map.set(evt.order_id, toKitchenOrder(evt, false))
      }
    }
    return null
  }

  const existing = map.get(msg.order_id)
  // seq is the JetStream stream sequence number; a lower-or-equal seq than
  // what we already hold is a redelivered/out-of-order message — ignore it
  // (last-writer-wins by seq, per the WS wire contract).
  if (existing && msg.seq !== 0 && msg.seq <= existing.seq) {
    return null
  }

  if (!isActiveKitchenStatus(msg.status)) {
    map.delete(msg.order_id)
    return null
  }

  const isNewOrder = msg.type === "order.placed" && !existing
  map.set(msg.order_id, toKitchenOrder(msg, isNewOrder))
  return isNewOrder ? msg.order_id : null
}

export function kitchenOrdersByStatus(
  map: KitchenOrderMap,
): Record<Extract<OrderStatus, "pending" | "accepted" | "preparing" | "ready">, KitchenOrder[]> {
  const grouped: Record<string, KitchenOrder[]> = {
    pending: [],
    accepted: [],
    preparing: [],
    ready: [],
  }
  for (const order of map.values()) {
    grouped[order.status]?.push(order)
  }
  for (const list of Object.values(grouped)) {
    list.sort((a, b) => a.occurredAt.localeCompare(b.occurredAt))
  }
  return grouped as ReturnType<typeof kitchenOrdersByStatus>
}
