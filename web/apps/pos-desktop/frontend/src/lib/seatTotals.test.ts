import { formatMoney } from '@onlinemenu/pos-core'
import { describe, expect, it } from 'vitest'
import { seatBadge, seatChip, seatGroups, seatLabel, type SeatOrderSource } from './seatTotals'

function order(...items: SeatOrderSource['items']): SeatOrderSource {
  return { items }
}

const item = (id: string, quantity: number, unitPrice: number, seatNo?: number) => ({
  id,
  quantity,
  unit_price_amount: unitPrice,
  seat_no: seatNo,
})

const none = new Set<string>()

describe('seatGroups', () => {
  it('is empty when no item carries a seat — the section must not render', () => {
    expect(seatGroups([], none)).toEqual([])
    expect(seatGroups([order(item('a', 2, 1000), item('b', 1, 500, 0))], none)).toEqual([])
  })

  it('groups items per seat across orders and sums quantity × unit price in kuruş', () => {
    const orders = [
      order(item('a', 2, 2500, 1), item('b', 1, 300, 2)),
      order(item('c', 1, 1200, 1)),
    ]
    expect(seatGroups(orders, none)).toEqual([
      { seat: 1, total: 6200, remaining: 6200 },
      { seat: 2, total: 300, remaining: 300 },
    ])
  })

  it('collects unassigned items into the trailing Ortak (seat 0) bucket', () => {
    const orders = [order(item('a', 1, 1000, 2), item('b', 3, 400), item('c', 1, 600, 0))]
    expect(seatGroups(orders, none)).toEqual([
      { seat: 2, total: 1000, remaining: 1000 },
      { seat: 0, total: 1800, remaining: 1800 },
    ])
  })

  it('sorts numbered seats ascending even when orders arrive out of order', () => {
    const orders = [order(item('a', 1, 100, 3), item('b', 1, 100, 1), item('c', 1, 100, 2))]
    expect(seatGroups(orders, none).map((g) => g.seat)).toEqual([1, 2, 3])
  })

  it('drops paid items from remaining but keeps them in the total', () => {
    const orders = [order(item('a', 2, 1000, 1), item('b', 1, 500, 1), item('c', 1, 700, 2))]
    const groups = seatGroups(orders, new Set(['a']))
    expect(groups).toEqual([
      { seat: 1, total: 2500, remaining: 500 },
      { seat: 2, total: 700, remaining: 700 },
    ])
  })

  it('a fully paid seat stays listed with remaining 0', () => {
    const orders = [order(item('a', 1, 900, 1), item('b', 1, 400, 2))]
    const groups = seatGroups(orders, new Set(['a']))
    expect(groups[0]).toEqual({ seat: 1, total: 900, remaining: 0 })
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
