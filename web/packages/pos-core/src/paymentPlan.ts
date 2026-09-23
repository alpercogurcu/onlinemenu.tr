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
    })),
  )
}

/**
 * Narrow shape itemsPaidBy needs from a tracked payment. Declared locally so
 * this package does not depend on any one app's fuller fiscal-tracking model
 * (e.g. pos-desktop's lib/fiscalStatus.ts, which additionally models the
 * async fiscal-registration lifecycle — pending/completed/failed/voided/
 * unknown — and branch-wide visibility across stations). A caller's richer
 * tracked-payment type is assignable here as-is.
 */
export type PaymentStatusSource = {
  status: string
  itemIds?: string[]
}

/**
 * Items already covered by an item payment made from this station. Only
 * payments that still hold or have settled money count: a failed or voided one
 * releases its items, exactly as it releases its amount (see the caller's
 * fiscal-status module).
 *
 * This lives in memory only. After an app restart every item reads as unpaid
 * again while the money balance stays right (it is derived from the server) —
 * the accepted residual risk of docs/pos-ux-spec.md §3b; a server-side
 * per-item paid amount is the Faz-2 answer.
 */
export function itemsPaidBy(tracked: readonly PaymentStatusSource[]): Set<string> {
  const paid = new Set<string>()
  for (const payment of tracked) {
    if (payment.status === 'failed' || payment.status === 'voided') continue
    for (const id of payment.itemIds ?? []) paid.add(id)
  }
  return paid
}

export function unpaidItems(items: readonly PayableItem[], paid: ReadonlySet<string>): PayableItem[] {
  return items.filter((item) => !paid.has(item.id))
}

export function selectionTotal(items: readonly PayableItem[], selected: ReadonlySet<string>): number {
  return items.reduce((sum, item) => (selected.has(item.id) ? sum + itemTotal(item) : sum), 0)
}

export type DueMode = 'full' | 'split' | 'items' | 'custom'

export type DueInput = {
  mode: DueMode
  /** What the customer still owes and the cashier may still collect. */
  remaining: number
  splitParts: number
  selectedTotal: number
  customDue: number
}

/**
 * What the next payment settles. Never more than `remaining`: the backend has
 * no overpayment guard (see payment.ts's clampToRemaining), so the clamp is
 * the only thing that stops a mistyped or stale amount from over-collecting.
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
