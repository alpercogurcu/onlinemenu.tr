import { describe, expect, it } from 'vitest'
import { buildPaymentLines, lineTotal, type PaymentLine } from './paymentLines'
import { coveredItems, dueFor, selectionTotal, type PayableItem } from './paymentPlan'

function item(id: string, name: string, quantity: number, unitPrice: number): PayableItem {
  return { id, productId: `p-${id}`, name, note: '', quantity, unitPrice, seat: 0 }
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

describe('buildPaymentLines from the payment screen', () => {
  const items = [lahmacun, cay, ayran] // 13000 + 1500 + 4500 = 19000
  const none = new Map<string, number>()

  function linesFor(
    mode: 'full' | 'items' | 'custom',
    remaining: number,
    selection: Map<string, number>,
    paid: Map<string, number> = none,
    customDue = 0,
  ) {
    const selectedTotal = selectionTotal(items, paid, selection)
    const due = dueFor({ mode, remaining, splitParts: 2, selectedTotal, customDue })
    return { due, lines: buildPaymentLines(coveredItems(mode, items, paid, selection), due) }
  }

  it.each([
    {
      name: 'one of two lahmacun',
      selection: [['1', 1]] as [string, number][],
      expected: [{ product_id: 'p-1', name: 'Lahmacun', unit_price_minor: 6500, quantity_milli: 1000 }],
    },
    {
      name: 'both lahmacun and two of three ayran',
      selection: [['1', 2], ['3', 2]] as [string, number][],
      expected: [
        { product_id: 'p-1', name: 'Lahmacun', unit_price_minor: 6500, quantity_milli: 2000 },
        { product_id: 'p-3', name: 'Ayran', unit_price_minor: 1500, quantity_milli: 2000 },
      ],
    },
  ])('an item selection goes out with real names, units and unit prices: $name', ({ selection, expected }) => {
    const { due, lines } = linesFor('items', 19000, new Map(selection))
    expect(lines).toEqual(expected)
    expect(sum(lines)).toBe(due)
  })

  it('the remaining unit of a partly paid row is charged at its real unit price', () => {
    const { lines } = linesFor('items', 6500 + 1500 + 4500, new Map([['1', 2]]), new Map([['1', 1]]))
    expect(lines).toEqual([{ product_id: 'p-1', name: 'Lahmacun', unit_price_minor: 6500, quantity_milli: 1000 }])
  })

  it('a selection above what the check still owes is capped and only then shared out proportionally', () => {
    // An earlier amount payment this station did not tie to items left 10000 owed.
    const { due, lines } = linesFor('items', 10000, new Map([['1', 2]]))
    expect(due).toBe(10000)
    expect(lines).toEqual([{ product_id: 'p-1', name: 'Lahmacun', unit_price_minor: 10000, quantity_milli: 1000 }])
  })

  it('an amount-based payment is still shared across the unpaid units', () => {
    const { due, lines } = linesFor('custom', 19000, none, new Map([['1', 2]]), 3000)
    expect(due).toBe(3000)
    expect(lines.map((l) => l.product_id)).toEqual(['p-2', 'p-3'])
    expect(lines.every((l) => l.quantity_milli === 1000)).toBe(true)
    expect(sum(lines)).toBe(3000)
  })

  it('"Tümü" after a partial item payment covers exactly the unpaid units, unsplit', () => {
    const { due, lines } = linesFor('full', 6500 + 1500 + 4500, none, new Map([['1', 1]]))
    expect(due).toBe(12500)
    expect(lines).toEqual([
      { product_id: 'p-1', name: 'Lahmacun', unit_price_minor: 6500, quantity_milli: 1000 },
      { product_id: 'p-2', name: 'Çay', unit_price_minor: 1500, quantity_milli: 1000 },
      { product_id: 'p-3', name: 'Ayran', unit_price_minor: 1500, quantity_milli: 3000 },
    ])
  })
})
