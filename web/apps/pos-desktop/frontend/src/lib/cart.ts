import type { main } from '../../wailsjs/go/models'
import { composeOptionNote, unitPriceWith, type SelectedModifier } from './options'

/** Narrow shape this module needs from a ProductDTO — declared locally (not imported from the generated, gitignored wailsjs models) so it is testable without the bindings; see lib/branchFiscal.ts. */
export type ProductSource = {
  id: string
  name: string
  price_amount: number
  currency: string
  tax_rate_bps: number
  unit: string
  /** The option lookup failed; the product is added without options and the line warns the cashier. */
  options_unavailable?: boolean
}

/** One not-yet-sent product line the cashier has tapped into the current round. */
export type PendingLine = {
  clientId: string
  productId: string
  productName: string
  productPriceAmount: number
  productCurrency: string
  taxRateBps: number
  unit: string
  quantity: number
  modifiers: SelectedModifier[]
  /** Free/ready-made kitchen note ("Soğansız"), separate from the option names. */
  note: string
  /** Merge key: two taps of the same product only share a line when this matches. */
  optionsHash: string
  optionsUnavailable: boolean
}

export type LineOptions = {
  modifiers?: SelectedModifier[]
  note?: string
  quantity?: number
}

export const MAX_LINE_QUANTITY = 99

const OPTIONS_UNAVAILABLE_NOTE = 'Seçenek alınamadı'

/**
 * Identity of a line's options. Sorted so the order the options were tapped in
 * does not matter; the note is part of it because the kitchen ticket differs.
 */
export function optionsHash(modifiers: readonly SelectedModifier[], note: string): string {
  return `${modifiers.map((m) => m.id).sort().join(',')}#${note.trim()}`
}

let lineSeq = 0

/**
 * Adds a product to the pending round. Same product + same options merges into
 * one line (quantity goes up); the same product with different options is a
 * separate line, because the kitchen and the price differ.
 */
export function addProductToPending(lines: PendingLine[], product: ProductSource, options: LineOptions = {}): PendingLine[] {
  const modifiers = options.modifiers ?? []
  const note = (options.note ?? '').trim()
  const quantity = options.quantity ?? 1
  const hash = optionsHash(modifiers, note)

  const existing = lines.find((l) => l.productId === product.id && l.optionsHash === hash)
  if (existing) {
    return lines.map((l) =>
      l === existing ? { ...l, quantity: Math.min(MAX_LINE_QUANTITY, l.quantity + quantity) } : l,
    )
  }
  lineSeq += 1
  return [
    ...lines,
    {
      clientId: `${product.id}-${Date.now()}-${lineSeq}`,
      productId: product.id,
      productName: product.name,
      productPriceAmount: product.price_amount,
      productCurrency: product.currency,
      taxRateBps: product.tax_rate_bps,
      unit: product.unit,
      quantity: Math.min(MAX_LINE_QUANTITY, quantity),
      modifiers,
      note,
      optionsHash: hash,
      optionsUnavailable: product.options_unavailable ?? false,
    },
  ]
}

/**
 * Stepper behaviour for a pending line: clamps to 1..MAX_LINE_QUANTITY and
 * never removes the line — removal is the separate ×, so a stray "−" tap on a
 * quantity of 1 cannot silently delete an order line.
 */
export function changePendingQuantity(lines: PendingLine[], clientId: string, delta: number): PendingLine[] {
  return lines.map((l) =>
    l.clientId === clientId
      ? { ...l, quantity: Math.min(MAX_LINE_QUANTITY, Math.max(1, l.quantity + delta)) }
      : l,
  )
}

export function removePendingLine(lines: PendingLine[], clientId: string): PendingLine[] {
  return lines.filter((l) => l.clientId !== clientId)
}

/** Base price plus every chosen option's price delta — the formula the server validates. */
export function pendingUnitPrice(line: PendingLine): number {
  return unitPriceWith(line.productPriceAmount, line.modifiers)
}

export function pendingLineTotal(line: PendingLine): number {
  return pendingUnitPrice(line) * line.quantity
}

export function pendingTotal(lines: PendingLine[]): number {
  return lines.reduce((sum, l) => sum + pendingLineTotal(l), 0)
}

function orderNoteFor(line: PendingLine): string {
  const note = composeOptionNote(line.modifiers, line.note)
  if (!line.optionsUnavailable) return note
  return note ? `${note} | ${OPTIONS_UNAVAILABLE_NOTE}` : OPTIONS_UNAVAILABLE_NOTE
}

export function toOrderItemInputs(lines: PendingLine[]): main.OrderItemInputDTO[] {
  return lines.map((l) => ({
    product_id: l.productId,
    product_name: l.productName,
    product_price_amount: l.productPriceAmount,
    product_currency: l.productCurrency,
    tax_rate_bps: l.taxRateBps,
    quantity: l.quantity,
    unit_price_amount: pendingUnitPrice(l),
    note: orderNoteFor(l),
    modifier_ids: l.modifiers.map((m) => m.id),
  }))
}

/**
 * Mirrors the backend's check-total computation
 * (pos/repo.CheckRepo.GetTotal: SUM(quantity * unit_price_amount) across
 * EVERY order on the check, regardless of that order's status — pending,
 * rejected or cancelled orders' items still count). There is no
 * server-side "check total" endpoint, so this must stay in lockstep with
 * that query or CloseCheck/RegisterCashPayment amounts will not match what
 * the backend expects.
 */
export function confirmedOrdersTotal(orders: main.OrderDTO[]): number {
  let total = 0
  for (const order of orders) {
    for (const item of order.items) {
      total += item.quantity * item.unit_price_amount
    }
  }
  return total
}
