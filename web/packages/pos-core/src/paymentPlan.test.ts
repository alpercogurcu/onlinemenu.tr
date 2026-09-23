import { describe, expect, it } from 'vitest'
import {
  cashChange,
  cashReceived,
  dueFor,
  itemsPaidBy,
  payableItems,
  selectionTotal,
  unpaidItems,
  type PayableItem,
  type PaymentStatusSource,
} from './paymentPlan'

function item(id: string, quantity: number, unitPrice: number): PayableItem {
  return { id, productId: `p-${id}`, name: `Ürün ${id}`, note: '', quantity, unitPrice }
}

type Status = 'pending' | 'completed' | 'failed' | 'voided' | 'unknown'

function tracked(id: string, status: Status, itemIds?: string[]): PaymentStatusSource {
  return { status, itemIds }
}

describe('payableItems', () => {
  it('flattens every order item — the same set the backend sums for the check total', () => {
    const items = payableItems([
      { items: [{ id: 'i1', product_id: 'p1', product_name: 'Çay', quantity: 2, unit_price_amount: 1500, note: 'Şekersiz' }] },
      { items: [{ id: 'i2', product_id: 'p2', product_name: 'Ayran', quantity: 1, unit_price_amount: 2500, note: '' }] },
    ])
    expect(items).toEqual([
      { id: 'i1', productId: 'p1', name: 'Çay', note: 'Şekersiz', quantity: 2, unitPrice: 1500 },
      { id: 'i2', productId: 'p2', name: 'Ayran', note: '', quantity: 1, unitPrice: 2500 },
    ])
  })
})

describe('itemsPaidBy', () => {
  it('counts items of payments that hold or settled money', () => {
    const paid = itemsPaidBy([tracked('a', 'pending', ['1']), tracked('b', 'completed', ['2']), tracked('c', 'unknown', ['3'])])
    expect([...paid].sort()).toEqual(['1', '2', '3'])
  })

  it('releases the items of a failed or voided payment so they can be paid again', () => {
    const paid = itemsPaidBy([tracked('a', 'failed', ['1']), tracked('b', 'voided', ['2'])])
    expect(paid.size).toBe(0)
  })

  it('ignores payments that were not item payments', () => {
    expect(itemsPaidBy([tracked('a', 'completed')]).size).toBe(0)
  })
})

describe('unpaidItems / selectionTotal', () => {
  const items = [item('1', 2, 6500), item('2', 1, 1500), item('3', 3, 1500)]

  it('drops the items already paid', () => {
    expect(unpaidItems(items, new Set(['2'])).map((i) => i.id)).toEqual(['1', '3'])
  })

  it('totals only the selected items', () => {
    expect(selectionTotal(items, new Set(['1', '3']))).toBe(13000 + 4500)
    expect(selectionTotal(items, new Set())).toBe(0)
  })
})

describe('dueFor', () => {
  const base = { remaining: 24000, splitParts: 3, selectedTotal: 0, customDue: 0 }

  it('full: everything that is left', () => {
    expect(dueFor({ ...base, mode: 'full' })).toBe(24000)
  })

  it('split: an equal share, rounded up, never more than what is left', () => {
    expect(dueFor({ ...base, mode: 'split' })).toBe(8000)
    expect(dueFor({ ...base, mode: 'split', remaining: 1001, splitParts: 3 })).toBe(334)
    expect(dueFor({ ...base, mode: 'split', remaining: 1, splitParts: 3 })).toBe(1)
  })

  it('items: the selected total, capped at what is left after earlier partial payments', () => {
    expect(dueFor({ ...base, mode: 'items', selectedTotal: 9000 })).toBe(9000)
    expect(dueFor({ ...base, mode: 'items', selectedTotal: 30000 })).toBe(24000)
    expect(dueFor({ ...base, mode: 'items', selectedTotal: 0 })).toBe(0)
  })

  it('custom: the typed amount, capped at what is left', () => {
    expect(dueFor({ ...base, mode: 'custom', customDue: 5000 })).toBe(5000)
    expect(dueFor({ ...base, mode: 'custom', customDue: 99999 })).toBe(24000)
  })
})

describe('cash handling', () => {
  it('a blank received field means exact cash for what is due', () => {
    expect(cashReceived('', 8000)).toBe(8000)
    expect(cashReceived('  ', 8000)).toBe(8000)
    expect(cashReceived('100', 8000)).toBe(10000)
  })

  it('change is only ever owed on cash', () => {
    expect(cashChange('cash', 10000, 8000)).toBe(2000)
    expect(cashChange('cash', 8000, 8000)).toBe(0)
    expect(cashChange('cash', 5000, 8000)).toBe(0)
    expect(cashChange('card', 10000, 8000)).toBe(0)
  })
})
