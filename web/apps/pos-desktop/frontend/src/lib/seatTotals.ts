// Per-seat (kuver) subtotals for the payment screen's "Kişiler" shortcut.
//
// The waiter UI assigns order items to guests 1,2,3… (seat_no; 0 = unassigned)
// and the payment screen offers one tap per guest that prefills the amount
// field with that guest's outstanding share — an accelerator over the normal
// partial payment, not a new payment kind: the registered payment is a plain
// custom-amount installment and the backend knows nothing about seats.
//
// Pure kuruş arithmetic (quantity × unit_price_amount, the same path
// pos-core's paymentPlan.itemTotal takes), kept out of the component so it is
// testable without a DOM.

/** Narrow shape this module needs from a confirmed order (main.OrderDTO).
 * Declared locally rather than imported from the generated Wails binding —
 * same rationale as pos-core's paymentPlan.OrderSource. seat_no is optional so
 * an order decoded by an older binding (field absent) reads as unassigned. */
export type SeatOrderSource = {
  items: {
    id: string
    quantity: number
    unit_price_amount: number
    seat_no?: number
  }[]
}

export type SeatGroup = {
  /** Guest number; 0 is the shared/unassigned bucket ("Ortak"). */
  seat: number
  /** Everything this guest ordered, in kuruş. */
  total: number
  /** The part not yet covered by an item payment, in kuruş. Custom-amount or
   * split payments do not move this — the accepted looseness of a shortcut. */
  remaining: number
}

/**
 * The check's items grouped per guest, numbered seats ascending and the
 * "Ortak" (seat 0) bucket last — at the till the named guests are the point;
 * the shared pot is the leftover. Returns [] when NO item carries a seat, so a
 * shop that never uses kuver never sees the section at all.
 */
export function seatGroups(orders: readonly SeatOrderSource[], paidItemIds: ReadonlySet<string>): SeatGroup[] {
  const bySeat = new Map<number, SeatGroup>()
  let anySeat = false
  for (const order of orders) {
    for (const item of order.items) {
      const seat = item.seat_no && item.seat_no > 0 ? item.seat_no : 0
      if (seat > 0) anySeat = true
      const group = bySeat.get(seat) ?? { seat, total: 0, remaining: 0 }
      const amount = item.quantity * item.unit_price_amount
      group.total += amount
      if (!paidItemIds.has(item.id)) group.remaining += amount
      bySeat.set(seat, group)
    }
  }
  if (!anySeat) return []
  return [...bySeat.values()].sort((a, b) => {
    if (a.seat === 0) return 1
    if (b.seat === 0) return -1
    return a.seat - b.seat
  })
}

/** Row label: "Kişi 2", or "Ortak" for the unassigned bucket. */
export function seatLabel(seat: number): string {
  return seat > 0 ? `Kişi ${seat}` : 'Ortak'
}

/** Compact badge text for an item row: "K2"; empty for an unassigned item. */
export function seatBadge(seatNo: number | undefined): string {
  return seatNo && seatNo > 0 ? `K${seatNo}` : ''
}
