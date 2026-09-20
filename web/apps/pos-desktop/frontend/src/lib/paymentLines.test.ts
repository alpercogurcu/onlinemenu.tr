import { describe, expect, it } from 'vitest'
import { buildPaymentLines, lineTotal, type PaymentLine } from './paymentLines'
import type { PayableItem } from './paymentPlan'

function item(id: string, name: string, quantity: number, unitPrice: number): PayableItem {
  return { id, productId: `p-${id}`, name, note: '', quantity, unitPrice }
}

const lahmacun = item('1', 'Lahmacun', 2, 6500) // 13000
const cay = item('2', 'Çay', 1, 1500) // 1500
const ayran = item('3', 'Ayran', 3, 1500) // 4500

function sum(lines: readonly PaymentLine[]): number {
  return lines.reduce((total, l) => total + lineTotal(l), 0)
}

describe('buildPaymentLines', () => {
  it('sends the items as they are when the payment covers exactly their total', () => {
    const lines = buildPaymentLines([lahmacun, cay], 14500)
    expect(lines).toEqual([
      { product_id: 'p-1', name: 'Lahmacun', unit_price_minor: 6500, quantity_milli: 2000 },
      { product_id: 'p-2', name: 'Çay', unit_price_minor: 1500, quantity_milli: 1000 },
    ])
  })

  it('never lets the lines drift from the payment amount — a vendor rejects a basket whose total differs', () => {
    for (const amount of [1, 7, 999, 7250, 14499, 14501, 20000]) {
      const lines = buildPaymentLines([lahmacun, cay, ayran], amount)
      expect(sum(lines)).toBe(amount)
    }
  })

  it('shares a partial payment across the items in proportion to what they cost', () => {
    const lines = buildPaymentLines([lahmacun, cay], 7250)
    expect(lines.map((l) => l.product_id)).toEqual(['p-1', 'p-2'])
    expect(sum(lines)).toBe(7250)
    expect(lines[0].unit_price_minor).toBeGreaterThan(lines[1].unit_price_minor)
    expect(lines.every((l) => l.quantity_milli === 1000)).toBe(true)
  })

  it('puts the rounding residue on a line instead of losing a kuruş', () => {
    const three = [item('a', 'A', 1, 1000), item('b', 'B', 1, 1000), item('c', 'C', 1, 1000)]
    const lines = buildPaymentLines(three, 1000)
    expect(sum(lines)).toBe(1000)
    expect(lines.map((l) => l.unit_price_minor).sort()).toEqual([333, 333, 334])
  })

  it('drops lines that would be allocated nothing', () => {
    const lines = buildPaymentLines([lahmacun, item('x', 'Şeker', 1, 1)], 1000)
    expect(lines.every((l) => l.unit_price_minor > 0)).toBe(true)
    expect(sum(lines)).toBe(1000)
  })

  it('returns no lines when there is nothing to pay or nothing to allocate to', () => {
    expect(buildPaymentLines([lahmacun], 0)).toEqual([])
    expect(buildPaymentLines([], 5000)).toEqual([])
    expect(buildPaymentLines([item('z', 'Bedava', 1, 0)], 5000)).toEqual([])
  })

  it('does not mutate its input', () => {
    const items = [lahmacun, cay]
    buildPaymentLines(items, 5000)
    expect(items[0].unitPrice).toBe(6500)
  })
})
