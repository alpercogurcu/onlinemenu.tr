// Pure rules behind the payment screen (docs/pos-ux-spec.md §3b): which items a
// check consists of, which of them are already paid, how much the next payment
// is for, and how much change a cash payment owes. Kept out of the component so
// the arithmetic — the part that moves money — is testable without a DOM.
//
// All amounts are integers in kuruş.

import { parseMoneyInputToKurus } from './format'
import { clampToRemaining, splitSuggestion } from './payment'

export type PayMethod = 'cash' | 'card'

/** One order item of the check, as the payment screen lists it. */
export type PayableItem = {
  id: string
  productId: string
  name: string
  note: string
  quantity: number
  /** Option-inclusive unit price (see options.ts's unitPriceWith). */
  unitPrice: number
  /** Guest (kuver) number; 0 = unassigned / shared. */
  seat: number
}

/** Narrow shape this module needs from an order — declared locally rather than
 * imported from a generated client binding; see cart.ts's ProductSource doc
 * comment for the same rationale. */
export type OrderSource = {
  items: {
    id: string
    product_id: string
    product_name: string
    quantity: number
    unit_price_amount: number
    note: string
    /** Optional so an order decoded by an older binding reads as unassigned. */
    seat_no?: number
  }[]
}

export function itemTotal(item: PayableItem): number {
  return item.quantity * item.unitPrice
}

/**
 * Every item of every order on the check — the same set the backend sums for
 * the check total (pos/repo.CheckRepo.GetTotal counts orders of any status),
 * so the screen's "kalan" and the items' sum agree.
 */
export function payableItems(orders: readonly OrderSource[]): PayableItem[] {
  return orders.flatMap((order) =>
    order.items.map((it) => ({
      id: it.id,
      productId: it.product_id,
      name: it.product_name,
      note: it.note,
      quantity: it.quantity,
      unitPrice: it.unit_price_amount,
      seat: it.seat_no && it.seat_no > 0 ? it.seat_no : 0,
    })),
  )
}

/** Units of one order item a payment covers. */
export type ItemQty = { id: string; qty: number }

/** Units selected for the next payment, per order-item id. Absent = none. */
export type PaySelection = ReadonlyMap<string, number>

/** Units already paid by an item payment, per order-item id. */
export type PaidQty = ReadonlyMap<string, number>

/**
 * Narrow shape paidQtyBy needs from a tracked payment. Declared locally so
 * this package does not depend on any one app's fuller fiscal-tracking model
 * (e.g. pos-desktop's lib/fiscalStatus.ts, which additionally models the
 * async fiscal-registration lifecycle — pending/completed/failed/voided/
 * unknown — and branch-wide visibility across stations). A caller's richer
 * tracked-payment type is assignable here as-is.
 */
export type PaymentStatusSource = {
  status: string
  items?: readonly ItemQty[]
}

/**
 * Units covered by item payments made from this station. Only payments that
 * still hold or have settled money count: a failed or voided one releases its
 * units, exactly as it releases its amount (see the caller's fiscal-status
 * module).
 *
 * This lives in memory only. After an app restart every item reads as unpaid
 * again while the money balance stays right (it is derived from the server) —
 * the accepted residual risk of docs/pos-ux-spec.md §3b; server-side item
 * allocations are the Faz-2 answer (docs/plans/2026-10-02-kasa-rapor-programi.md G.1).
 */
export function paidQtyBy(tracked: readonly PaymentStatusSource[]): Map<string, number> {
  const paid = new Map<string, number>()
  for (const payment of tracked) {
    if (payment.status === 'failed' || payment.status === 'voided') continue
    for (const { id, qty } of payment.items ?? []) {
      if (qty > 0) paid.set(id, (paid.get(id) ?? 0) + qty)
    }
  }
  return paid
}

export function paidUnits(item: PayableItem, paid: PaidQty): number {
  return Math.min(item.quantity, paid.get(item.id) ?? 0)
}

/** Units of the item still open for an item payment. */
export function unpaidUnits(item: PayableItem, paid: PaidQty): number {
  return Math.max(0, item.quantity - (paid.get(item.id) ?? 0))
}

/** Selected units of the item, never more than are still unpaid — a stale
 * entry (a unit a just-registered payment took, an order refetch) must not be
 * charged twice. */
export function selectedUnits(item: PayableItem, paid: PaidQty, selection: PaySelection): number {
  return Math.max(0, Math.min(selection.get(item.id) ?? 0, unpaidUnits(item, paid)))
}

/** Every item reduced to its unpaid units; fully paid ones dropped. */
export function unpaidItems(items: readonly PayableItem[], paid: PaidQty): PayableItem[] {
  return withQuantities(items, (item) => unpaidUnits(item, paid))
}

/** The selection as items carrying only the selected units — what an item
 * payment covers and the fiscal lines are built from. */
export function selectedItems(items: readonly PayableItem[], paid: PaidQty, selection: PaySelection): PayableItem[] {
  return withQuantities(items, (item) => selectedUnits(item, paid, selection))
}

function withQuantities(items: readonly PayableItem[], quantityOf: (item: PayableItem) => number): PayableItem[] {
  return items.flatMap((item) => {
    const quantity = quantityOf(item)
    return quantity > 0 ? [{ ...item, quantity }] : []
  })
}

export function selectionTotal(items: readonly PayableItem[], paid: PaidQty, selection: PaySelection): number {
  return selectedItems(items, paid, selection).reduce((sum, item) => sum + itemTotal(item), 0)
}

/** What the request records as paid by an item payment. */
export function selectionAllocations(items: readonly PayableItem[], paid: PaidQty, selection: PaySelection): ItemQty[] {
  return selectedItems(items, paid, selection).map((item) => ({ id: item.id, qty: item.quantity }))
}

export function selectedUnitCount(items: readonly PayableItem[], paid: PaidQty, selection: PaySelection): number {
  return selectedItems(items, paid, selection).reduce((sum, item) => sum + item.quantity, 0)
}

/** The selection with one item set to `qty` units (0 removes it). */
export function withSelectedQty(selection: PaySelection, id: string, qty: number): PaySelection {
  const next = new Map(selection)
  if (qty > 0) next.set(id, qty)
  else next.delete(id)
  return next
}

/** A tap on a row adds one unit; past the last unpaid one it wraps to none
 * (1/2 → 2/2 → 0). A single-unit row therefore just toggles. */
export function tapUnits(current: number, available: number): number {
  if (available <= 0) return 0
  return current >= available ? 0 : current + 1
}

/** The row stepper: ±1 within 0..available. */
export function stepUnits(current: number, available: number, delta: number): number {
  return Math.max(0, Math.min(available, current + delta))
}

export type DueMode = 'full' | 'split' | 'items' | 'custom'

/**
 * The items one payment's fiscal lines are built from. An item payment covers
 * exactly the selected units, so its total equals the amount and the lines go
 * out as they are; an amount-based payment (full/split/custom) covers the
 * still-unpaid units — or every item when this station tracked none — and
 * paymentLines.ts shares the amount across them.
 */
export function coveredItems(
  mode: DueMode,
  items: readonly PayableItem[],
  paid: PaidQty,
  selection: PaySelection,
): PayableItem[] {
  if (mode === 'items') return selectedItems(items, paid, selection)
  const unpaid = unpaidItems(items, paid)
  return unpaid.length > 0 ? unpaid : [...items]
}

export type DueInput = {
  mode: DueMode
  /** What the customer still owes and the cashier may still collect. */
  remaining: number
  splitParts: number
  selectedTotal: number
  customDue: number
}

/**
 * What the next payment settles. Never more than `remaining` (see payment.ts's
 * clampToRemaining). The backend now refuses an amount above what the check
 * still owes (409 payment_exceeds_due), but the clamp keeps a mistyped amount
 * from reaching the server at all; the server guard is what covers a stale
 * `remaining` when another station paid first.
 */
export function dueFor(input: DueInput): number {
  switch (input.mode) {
    case 'full':
      return input.remaining
    case 'split':
      return clampToRemaining(splitSuggestion(input.remaining, input.splitParts), input.remaining)
    case 'items':
      return clampToRemaining(input.selectedTotal, input.remaining)
    case 'custom':
      return clampToRemaining(input.customDue, input.remaining)
  }
}

/** Cash the customer handed over. A blank field means exact cash for what is due — the common case. */
export function cashReceived(receivedInput: string, due: number): number {
  return receivedInput.trim() === '' ? due : parseMoneyInputToKurus(receivedInput)
}

/** Change owed back. Cards never give change; short cash gives none (the button stays disabled). */
export function cashChange(method: PayMethod, received: number, due: number): number {
  if (method !== 'cash') return 0
  return Math.max(0, received - due)
}
