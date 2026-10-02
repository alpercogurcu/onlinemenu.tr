import { formatMoney, selectionTotal, type PayableItem } from '@onlinemenu/pos-core'
import { describe, expect, it } from 'vitest'
import { seatBadge, seatChip, seatGroups, seatLabel, seatSelected, toggleSeat } from './seatTotals'

const item = (id: string, quantity: number, unitPrice: number, seat = 0): PayableItem => ({
  id,
  productId: `p-${id}`,
  name: id,
  note: '',
  quantity,
  unitPrice,
  seat,
})

const none = new Map<string, number>()
const qty = (entries: [string, number][]) => new Map(entries)

describe('seatGroups', () => {
  it('is empty when no item carries a seat — the section must not render', () => {
    expect(seatGroups([], none)).toEqual([])
    expect(seatGroups([item('a', 2, 1000), item('b', 1, 500, 0)], none)).toEqual([])
  })

  it('groups items per seat and sums quantity × unit price in kuruş', () => {
    const items = [item('a', 2, 2500, 1), item('b', 1, 300, 2), item('c', 1, 1200, 1)]
    expect(seatGroups(items, none)).toEqual([
      { seat: 1, total: 6200, remaining: 6200 },
      { seat: 2, total: 300, remaining: 300 },
    ])
  })

  it('collects unassigned items into the trailing Ortak (seat 0) bucket', () => {
    const items = [item('a', 1, 1000, 2), item('b', 3, 400), item('c', 1, 600, 0)]
    expect(seatGroups(items, none)).toEqual([
      { seat: 2, total: 1000, remaining: 1000 },
      { seat: 0, total: 1800, remaining: 1800 },
    ])
  })

  it('sorts numbered seats ascending even when items arrive out of order', () => {
    const items = [item('a', 1, 100, 3), item('b', 1, 100, 1), item('c', 1, 100, 2)]
    expect(seatGroups(items, none).map((g) => g.seat)).toEqual([1, 2, 3])
  })

  it.each([
    { paid: [['a', 2]] as [string, number][], seat1: 500 },
    { paid: [['a', 1]] as [string, number][], seat1: 1500 },
    { paid: [['a', 1], ['b', 1]] as [string, number][], seat1: 1000 },
  ])('drops paid units from remaining but keeps them in the total: $paid', ({ paid, seat1 }) => {
    const items = [item('a', 2, 1000, 1), item('b', 1, 500, 1), item('c', 1, 700, 2)]
    expect(seatGroups(items, qty(paid))).toEqual([
      { seat: 1, total: 2500, remaining: seat1 },
      { seat: 2, total: 700, remaining: 700 },
    ])
  })

  it('a fully paid seat stays listed with remaining 0', () => {
    const groups = seatGroups([item('a', 1, 900, 1), item('b', 1, 400, 2)], qty([['a', 1]]))
    expect(groups[0]).toEqual({ seat: 1, total: 900, remaining: 0 })
  })
})

describe('seat chip selection', () => {
  const items = [item('a', 2, 1000, 1), item('b', 1, 500, 1), item('c', 3, 700, 2), item('d', 1, 400, 0)]

  it('a tap selects every unpaid unit of the guest; the selection totals to the chip amount', () => {
    const paid = qty([['a', 1]])
    const selection = toggleSeat(items, paid, none, 1)
    expect(Object.fromEntries(selection)).toEqual({ a: 1, b: 1 })
    expect(selectionTotal(items, paid, selection)).toBe(seatGroups(items, paid)[0].remaining)
    expect(seatSelected(items, paid, selection, 1)).toBe(true)
  })

  it('several guests can be selected together and the second tap releases only that guest', () => {
    let selection = toggleSeat(items, none, none, 1)
    selection = toggleSeat(items, none, selection, 0)
    expect(selectionTotal(items, none, selection)).toBe(2000 + 500 + 400)
    selection = toggleSeat(items, none, selection, 1)
    expect(Object.fromEntries(selection)).toEqual({ d: 1 })
    expect(seatSelected(items, none, selection, 1)).toBe(false)
    expect(seatSelected(items, none, selection, 0)).toBe(true)
  })

  it('a guest trimmed on the rail reads as not selected; tapping again fills them back in', () => {
    const trimmed = qty([['c', 2]])
    expect(seatSelected(items, none, trimmed, 2)).toBe(false)
    expect(Object.fromEntries(toggleSeat(items, none, trimmed, 2))).toEqual({ c: 3 })
  })

  it('a fully paid guest is never selected and selects nothing', () => {
    const paid = qty([['d', 1]])
    expect(seatSelected(items, paid, none, 0)).toBe(false)
    expect(toggleSeat(items, paid, none, 0).size).toBe(0)
  })
})

describe('seatChip', () => {
  it('renders an open guest as "N · tutar" and keeps it tappable', () => {
    expect(seatChip({ seat: 2, total: 52000, remaining: 52000 })).toEqual({
      seat: 2,
      settled: false,
      label: `2 · ${formatMoney(52000)}`,
    })
  })

  it('renders a settled guest (kalan 0) as "N · Ödendi" and disables the shortcut', () => {
    expect(seatChip({ seat: 1, total: 47000, remaining: 0 })).toEqual({
      seat: 1,
      settled: true,
      label: '1 · Ödendi',
    })
  })

  it('names the shared bucket "Ortak" in both states', () => {
    expect(seatChip({ seat: 0, total: 16000, remaining: 16000 }).label).toBe(`Ortak · ${formatMoney(16000)}`)
    expect(seatChip({ seat: 0, total: 16000, remaining: 0 }).label).toBe('Ortak · Ödendi')
  })
})

describe('seatLabel / seatBadge', () => {
  it('labels guests and the shared bucket', () => {
    expect(seatLabel(2)).toBe('Kişi 2')
    expect(seatLabel(0)).toBe('Ortak')
  })

  it('renders a compact badge only for an assigned item', () => {
    expect(seatBadge(3)).toBe('K3')
    expect(seatBadge(0)).toBe('')
    expect(seatBadge(undefined)).toBe('')
  })
})
