import { describe, expect, it } from 'vitest'
import {
  cashChange,
  cashReceived,
  coveredItems,
  dueFor,
  paidQtyBy,
  payableItems,
  selectedItems,
  selectedUnitCount,
  selectionAllocations,
  selectionTotal,
  stepUnits,
  tapUnits,
  unpaidItems,
  unpaidUnits,
  withSelectedQty,
  type ItemQty,
  type PayableItem,
  type PaymentStatusSource,
} from './paymentPlan'

function item(id: string, quantity: number, unitPrice: number, seat = 0): PayableItem {
  return { id, productId: `p-${id}`, name: `Ürün ${id}`, note: '', quantity, unitPrice, seat }
}

type Status = 'pending' | 'completed' | 'failed' | 'voided' | 'unknown'

function tracked(status: Status, items?: ItemQty[]): PaymentStatusSource {
  return { status, items }
}

const sel = (entries: [string, number][]) => new Map(entries)
const none = new Map<string, number>()

describe('payableItems', () => {
  it('flattens every order item — the same set the backend sums for the check total', () => {
    const items = payableItems([
      { items: [{ id: 'i1', product_id: 'p1', product_name: 'Çay', quantity: 2, unit_price_amount: 1500, note: 'Şekersiz' }] },
      { items: [{ id: 'i2', product_id: 'p2', product_name: 'Ayran', quantity: 1, unit_price_amount: 2500, note: '' }] },
    ])
    expect(items).toEqual([
      { id: 'i1', productId: 'p1', name: 'Çay', note: 'Şekersiz', quantity: 2, unitPrice: 1500, seat: 0 },
      { id: 'i2', productId: 'p2', name: 'Ayran', note: '', quantity: 1, unitPrice: 2500, seat: 0 },
    ])
  })

  it('carries the guest seat, reading a missing or zero seat_no as shared', () => {
    const items = payableItems([
      {
        items: [
          { id: 'a', product_id: 'p', product_name: 'X', quantity: 1, unit_price_amount: 100, note: '', seat_no: 2 },
          { id: 'b', product_id: 'p', product_name: 'X', quantity: 1, unit_price_amount: 100, note: '', seat_no: 0 },
          { id: 'c', product_id: 'p', product_name: 'X', quantity: 1, unit_price_amount: 100, note: '' },
        ],
      },
    ])
    expect(items.map((i) => i.seat)).toEqual([2, 0, 0])
  })
})

describe('paidQtyBy', () => {
  it('sums the units of payments that hold or settled money, across payments', () => {
    const paid = paidQtyBy([
      tracked('pending', [{ id: '1', qty: 1 }]),
      tracked('completed', [{ id: '1', qty: 1 }, { id: '2', qty: 3 }]),
      tracked('unknown', [{ id: '3', qty: 1 }]),
    ])
    expect(Object.fromEntries(paid)).toEqual({ '1': 2, '2': 3, '3': 1 })
  })

  it('releases the units of a failed or voided payment so they can be paid again', () => {
    const paid = paidQtyBy([tracked('failed', [{ id: '1', qty: 1 }]), tracked('voided', [{ id: '2', qty: 2 }])])
    expect(paid.size).toBe(0)
  })

  it('ignores payments that were not item payments', () => {
    expect(paidQtyBy([tracked('completed')]).size).toBe(0)
  })
})

describe('partially paid rows', () => {
  const burger = item('b', 2, 30000)

  it.each([
    { paid: 0, open: 2 },
    { paid: 1, open: 1 },
    { paid: 2, open: 0 },
    { paid: 5, open: 0 },
  ])('$paid of 2 paid leaves $open open', ({ paid, open }) => {
    expect(unpaidUnits(burger, sel([['b', paid]]))).toBe(open)
  })

  it('amount-based coverage keeps only the unpaid units and drops fully paid rows', () => {
    const items = [burger, item('c', 1, 1500)]
    expect(unpaidItems(items, sel([['b', 1], ['c', 1]]))).toEqual([{ ...burger, quantity: 1 }])
  })

  it('a selection cannot reach a unit already paid — a stale entry is clamped', () => {
    const paid = sel([['b', 1]])
    expect(selectedItems([burger], paid, sel([['b', 2]]))).toEqual([{ ...burger, quantity: 1 }])
    expect(selectionTotal([burger], paid, sel([['b', 2]]))).toBe(30000)
    expect(selectionAllocations([burger], paid, sel([['b', 2]]))).toEqual([{ id: 'b', qty: 1 }])
  })
})

describe('unit selection', () => {
  const items = [item('1', 2, 6500), item('2', 1, 1500), item('3', 3, 1500)]

  it.each([
    { selection: [] as [string, number][], total: 0, units: 0 },
    { selection: [['1', 1]] as [string, number][], total: 6500, units: 1 },
    { selection: [['1', 2], ['3', 1]] as [string, number][], total: 13000 + 1500, units: 3 },
    { selection: [['1', 2], ['2', 1], ['3', 3]] as [string, number][], total: 13000 + 1500 + 4500, units: 6 },
    { selection: [['ghost', 4]] as [string, number][], total: 0, units: 0 },
  ])('selected units × unit price: $selection → $total', ({ selection, total, units }) => {
    expect(selectionTotal(items, none, sel(selection))).toBe(total)
    expect(selectedUnitCount(items, none, sel(selection))).toBe(units)
  })

  it.each([
    { current: 0, available: 1, next: 1 },
    { current: 1, available: 1, next: 0 },
    { current: 0, available: 2, next: 1 },
    { current: 1, available: 2, next: 2 },
    { current: 2, available: 2, next: 0 },
    { current: 3, available: 2, next: 0 },
    { current: 0, available: 0, next: 0 },
  ])('a tap cycles units: $current/$available → $next', ({ current, available, next }) => {
    expect(tapUnits(current, available)).toBe(next)
  })

  it.each([
    { current: 0, delta: -1, next: 0 },
    { current: 1, delta: -1, next: 0 },
    { current: 1, delta: 1, next: 2 },
    { current: 3, delta: 1, next: 3 },
  ])('the stepper stays within 0..3: $current $delta → $next', ({ current, delta, next }) => {
    expect(stepUnits(current, 3, delta)).toBe(next)
  })

  it('setting a row to zero removes it and leaves the input untouched', () => {
    const before = sel([['1', 2]])
    const after = withSelectedQty(before, '1', 0)
    expect(after.has('1')).toBe(false)
    expect(before.get('1')).toBe(2)
    expect(withSelectedQty(after, '2', 1).get('2')).toBe(1)
  })
})

describe('coveredItems', () => {
  const items = [item('1', 2, 6500), item('2', 1, 1500)]

  it('an item payment covers exactly the selected units', () => {
    expect(coveredItems('items', items, none, sel([['1', 1]]))).toEqual([{ ...items[0], quantity: 1 }])
  })

  it('an amount payment covers the unpaid units', () => {
    expect(coveredItems('full', items, sel([['1', 1]]), none)).toEqual([{ ...items[0], quantity: 1 }, items[1]])
  })

  it('an amount payment falls back to every item when this station saw all of them paid', () => {
    expect(coveredItems('custom', items, sel([['1', 2], ['2', 1]]), none)).toEqual(items)
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
