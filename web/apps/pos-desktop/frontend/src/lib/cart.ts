import type { main } from '../../wailsjs/go/models'

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
}

export function addProductToPending(lines: PendingLine[], product: main.ProductDTO): PendingLine[] {
  const existing = lines.find((l) => l.productId === product.id)
  if (existing) {
    return lines.map((l) => (l.productId === product.id ? { ...l, quantity: l.quantity + 1 } : l))
  }
  return [
    ...lines,
    {
      clientId: `${product.id}-${Date.now()}`,
      productId: product.id,
      productName: product.name,
      productPriceAmount: product.price_amount,
      productCurrency: product.currency,
      taxRateBps: product.tax_rate_bps,
      unit: product.unit,
      quantity: 1,
    },
  ]
}

export function removePendingLine(lines: PendingLine[], clientId: string): PendingLine[] {
  return lines.filter((l) => l.clientId !== clientId)
}

export function pendingLineTotal(line: PendingLine): number {
  return line.productPriceAmount * line.quantity
}

export function pendingTotal(lines: PendingLine[]): number {
  return lines.reduce((sum, l) => sum + pendingLineTotal(l), 0)
}

export function toOrderItemInputs(lines: PendingLine[]): main.OrderItemInputDTO[] {
  return lines.map((l) => ({
    product_id: l.productId,
    product_name: l.productName,
    product_price_amount: l.productPriceAmount,
    product_currency: l.productCurrency,
    tax_rate_bps: l.taxRateBps,
    quantity: l.quantity,
    unit_price_amount: l.productPriceAmount,
    note: '',
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
