// Per-seat (kuver) subtotals and selection for the payment screen's "Kişiler"
// shortcut.
//
// The waiter UI assigns order items to guests 1,2,3… (seat_no; 0 = unassigned).
// On the payment screen a guest chip selects that guest's unpaid units on the
// receipt rail (docs/plans/2026-10-02-kasa-rapor-programi.md G.1) — an
// accelerator over the ordinary item payment, not a new payment kind: the
// backend knows nothing about seats.
//
// Pure kuruş arithmetic over pos-core's paymentPlan unit helpers, kept out of
// the component so it is testable without a DOM.

import {
  formatMoney,
  itemTotal,
  selectedUnits,
  unpaidUnits,
  withSelectedQty,
  type PaidQty,
  type PayableItem,
  type PaySelection,
} from '@onlinemenu/pos-core'

export type SeatGroup = {
  /** Guest number; 0 is the shared/unassigned bucket ("Ortak"). */
  seat: number
  /** Everything this guest ordered, in kuruş. */
  total: number
  /** The units not yet covered by an item payment, in kuruş. Amount-based
   * (full/split/custom) payments do not move this — the accepted looseness of
   * a shortcut. */
  remaining: number
}

/**
 * The check's items grouped per guest, numbered seats ascending and the
 * "Ortak" (seat 0) bucket last — at the till the named guests are the point;
 * the shared pot is the leftover. Returns [] when NO item carries a seat, so a
 * shop that never uses kuver never sees the section at all.
 */
export function seatGroups(items: readonly PayableItem[], paid: PaidQty): SeatGroup[] {
  const bySeat = new Map<number, SeatGroup>()
  for (const item of items) {
    const group = bySeat.get(item.seat) ?? { seat: item.seat, total: 0, remaining: 0 }
    group.total += itemTotal(item)
    group.remaining += unpaidUnits(item, paid) * item.unitPrice
    bySeat.set(item.seat, group)
  }
  if (![...bySeat.keys()].some((seat) => seat > 0)) return []
  return [...bySeat.values()].sort((a, b) => {
    if (a.seat === 0) return 1
    if (b.seat === 0) return -1
    return a.seat - b.seat
  })
}

/** A guest counts as selected while every one of their unpaid units is — so
 * a cashier who trims a row on the rail sees the chip let go. */
export function seatSelected(items: readonly PayableItem[], paid: PaidQty, selection: PaySelection, seat: number): boolean {
  const open = items.filter((item) => item.seat === seat && unpaidUnits(item, paid) > 0)
  return open.length > 0 && open.every((item) => selectedUnits(item, paid, selection) === unpaidUnits(item, paid))
}

/** Chip tap: selects all of the guest's unpaid units, or releases them when
 * the guest is already selected. Other guests' selections stay — several
 * guests can pay together. */
export function toggleSeat(items: readonly PayableItem[], paid: PaidQty, selection: PaySelection, seat: number): PaySelection {
  const release = seatSelected(items, paid, selection, seat)
  let next = selection
  for (const item of items) {
    if (item.seat !== seat) continue
    next = withSelectedQty(next, item.id, release ? 0 : unpaidUnits(item, paid))
  }
  return next
}

export type SeatChip = {
  seat: number
  /** Chip text as rendered: "2 · 520,00 ₺", "1 · Ödendi", "Ortak · 160,00 ₺". */
  label: string
  /** remaining 0 — every unit of the guest was item-paid; the chip is a badge,
   * not a shortcut, and must not be tappable. */
  settled: boolean
}

/**
 * Presentation of one "Kişiler" chip on the payment screen (approved payment
 * design): a settled guest reads "N · Ödendi", an open one "N · tutar", and
 * the shared bucket keeps its "Ortak" name. Pure so the visible chip text is
 * testable without a DOM.
 */
export function seatChip(group: SeatGroup): SeatChip {
  const name = group.seat > 0 ? String(group.seat) : 'Ortak'
  const settled = group.remaining <= 0
  return {
    seat: group.seat,
    settled,
    label: settled ? `${name} · Ödendi` : `${name} · ${formatMoney(group.remaining)}`,
  }
}

/** Row label: "Kişi 2", or "Ortak" for the unassigned bucket. */
export function seatLabel(seat: number): string {
  return seat > 0 ? `Kişi ${seat}` : 'Ortak'
}

/** Compact badge text for an item row: "K2"; empty for an unassigned item. */
export function seatBadge(seatNo: number | undefined): string {
  return seatNo && seatNo > 0 ? `K${seatNo}` : ''
}
