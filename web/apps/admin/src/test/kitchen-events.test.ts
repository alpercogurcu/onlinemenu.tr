import { describe, expect, it } from "vitest"

import {
  applyKitchenMessage,
  kitchenOrdersByStatus,
  type KitchenMessage,
  type KitchenOrderMap,
} from "@/lib/kitchen-events"

function snapshot(orders: KitchenMessage[]): KitchenMessage {
  return { type: "snapshot", orders } as KitchenMessage
}

describe("applyKitchenMessage", () => {
  it("builds initial state from a snapshot, dropping terminal statuses", () => {
    const map: KitchenOrderMap = new Map()
    applyKitchenMessage(
      map,
      snapshot([
        {
          type: "snapshot",
          order_id: "o1",
          table_label: "Masa 1",
          status: "pending",
          seq: 0,
          occurred_at: "2026-07-05T10:00:00Z",
        },
        {
          type: "snapshot",
          order_id: "o2",
          table_label: "Masa 2",
          status: "delivered",
          seq: 0,
          occurred_at: "2026-07-05T10:00:00Z",
        },
      ]) as never,
    )

    expect(map.size).toBe(1)
    expect(map.get("o1")?.tableLabel).toBe("Masa 1")
    expect(map.has("o2")).toBe(false)
  })

  it("flags a live order.placed as new but not snapshot rows", () => {
    const map: KitchenOrderMap = new Map()
    const newId = applyKitchenMessage(map, {
      type: "order.placed",
      order_id: "o1",
      table_label: "Masa 1",
      status: "pending",
      seq: 1,
      occurred_at: "2026-07-05T10:00:00Z",
    })

    expect(newId).toBe("o1")
    expect(map.get("o1")?.isNew).toBe(true)
  })

  it("applies last-writer-wins by seq and ignores stale/out-of-order events", () => {
    const map: KitchenOrderMap = new Map()
    applyKitchenMessage(map, {
      type: "order.placed",
      order_id: "o1",
      table_label: "Masa 1",
      status: "pending",
      seq: 5,
      occurred_at: "2026-07-05T10:00:00Z",
    })

    // Stale redelivery with a lower seq must not overwrite newer state.
    const staleResult = applyKitchenMessage(map, {
      type: "order.status_changed",
      order_id: "o1",
      table_label: "Masa 1",
      status: "accepted",
      seq: 3,
      occurred_at: "2026-07-05T10:00:01Z",
    })

    expect(staleResult).toBeNull()
    expect(map.get("o1")?.status).toBe("pending")

    // Newer seq applies normally.
    applyKitchenMessage(map, {
      type: "order.status_changed",
      order_id: "o1",
      table_label: "Masa 1",
      status: "accepted",
      seq: 6,
      occurred_at: "2026-07-05T10:00:02Z",
    })
    expect(map.get("o1")?.status).toBe("accepted")
  })

  it("removes an order from the board once it reaches a terminal status", () => {
    const map: KitchenOrderMap = new Map()
    applyKitchenMessage(map, {
      type: "order.placed",
      order_id: "o1",
      table_label: "Masa 1",
      status: "ready",
      seq: 1,
      occurred_at: "2026-07-05T10:00:00Z",
    })
    applyKitchenMessage(map, {
      type: "order.status_changed",
      order_id: "o1",
      table_label: "Masa 1",
      status: "delivered",
      seq: 2,
      occurred_at: "2026-07-05T10:00:05Z",
    })

    expect(map.has("o1")).toBe(false)
  })

  it("a fresh snapshot fully replaces prior state (reconnect recovery)", () => {
    const map: KitchenOrderMap = new Map()
    applyKitchenMessage(map, {
      type: "order.placed",
      order_id: "stale",
      table_label: "Masa 9",
      status: "pending",
      seq: 1,
      occurred_at: "2026-07-05T10:00:00Z",
    })

    applyKitchenMessage(
      map,
      snapshot([
        {
          type: "snapshot",
          order_id: "o1",
          table_label: "Masa 1",
          status: "preparing",
          seq: 0,
          occurred_at: "2026-07-05T10:05:00Z",
        },
      ]) as never,
    )

    expect(map.has("stale")).toBe(false)
    expect(map.has("o1")).toBe(true)
  })
})

describe("kitchenOrdersByStatus", () => {
  it("groups orders by status and sorts each column oldest-first", () => {
    const map: KitchenOrderMap = new Map()
    applyKitchenMessage(map, {
      type: "order.placed",
      order_id: "later",
      table_label: "Masa 2",
      status: "pending",
      seq: 1,
      occurred_at: "2026-07-05T10:05:00Z",
    })
    applyKitchenMessage(map, {
      type: "order.placed",
      order_id: "earlier",
      table_label: "Masa 1",
      status: "pending",
      seq: 2,
      occurred_at: "2026-07-05T10:00:00Z",
    })

    const grouped = kitchenOrdersByStatus(map)
    expect(grouped.pending.map((o) => o.orderId)).toEqual(["earlier", "later"])
    expect(grouped.accepted).toEqual([])
  })
})
