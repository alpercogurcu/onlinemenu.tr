// Turns the items a payment covers into the fiscal lines sent with it.
//
// The rule that matters (backend domain.FiscalSale: "TotalMinor must equal
// lines minus discounts; vendors reject mismatches"): the lines of ONE payment
// must add up to that payment's amount, not to the whole check. So a payment
// that covers only part of the items — a split share, a partial amount, or
// items selected for a separate payment — cannot simply send every item.
//
// - Amount equals the items' total: the items go as they are (real quantity
//   and unit price).
// - Anything else: the amount is shared across the items in proportion to what
//   they cost (largest-remainder, so not a kuruş is lost) and each item goes as
//   one unit at its allocated price. The receipt then shows "Lahmacun 1 × ₺45"
//   rather than "2 × ₺65"; the fiscal total is right, which is what the device
//   validates.

import { itemTotal, type PayableItem } from './paymentPlan'

/** Wire line for the Go binding; it adds tax, category and unit from the catalog. */
export type PaymentLine = {
  product_id: string
  name: string
  unit_price_minor: number
  /** Thousandths: 1000 = one unit. */
  quantity_milli: number
}

export function lineTotal(line: PaymentLine): number {
  return Math.round((line.unit_price_minor * line.quantity_milli) / 1000)
}

export function buildPaymentLines(items: readonly PayableItem[], amount: number): PaymentLine[] {
  const total = items.reduce((sum, item) => sum + itemTotal(item), 0)
  if (amount <= 0 || total <= 0) return []

  if (amount === total) {
    return items.map((item) => ({
      product_id: item.productId,
      name: item.name,
      unit_price_minor: item.unitPrice,
      quantity_milli: item.quantity * 1000,
    }))
  }

  // BigInt: amount * lineTotal overflows 2^53 long before a check is unrealistic.
  const shares = items.map((item) => {
    const exact = BigInt(amount) * BigInt(itemTotal(item))
    return { floor: Number(exact / BigInt(total)), rest: Number(exact % BigInt(total)) }
  })
  let leftover = amount - shares.reduce((sum, s) => sum + s.floor, 0)
  const byRest = shares.map((_, i) => i).sort((a, b) => shares[b].rest - shares[a].rest || a - b)
  const allocated = shares.map((s) => s.floor)
  for (const index of byRest) {
    if (leftover <= 0) break
    allocated[index] += 1
    leftover -= 1
  }

  return items
    .map((item, i) => ({
      product_id: item.productId,
      name: item.name,
      unit_price_minor: allocated[i],
      quantity_milli: 1000,
    }))
    .filter((line) => line.unit_price_minor > 0)
}
