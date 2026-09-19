import { describe, expect, it } from 'vitest'
import {
  addKitchenFailure,
  applyKitchenPrintResult,
  type KitchenPrintResultEvent,
  describeKitchenFailure,
  removeKitchenFailure,
  shortOrderId,
  type KitchenPrintFailure,
} from './kitchenPrint'

function failure(over: Partial<KitchenPrintFailure> = {}): KitchenPrintFailure {
  return { orderId: 'a1b2c3d4-1111-2222-3333-444455556666', tableLabel: 'Masa 7', message: 'yazıcı bağlı değil', ...over }
}

describe('shortOrderId', () => {
  it('is the first 8 characters, matching the number the kitchen ticket prints', () => {
    expect(shortOrderId('a1b2c3d4-1111-2222-3333-444455556666')).toBe('a1b2c3d4')
    expect(shortOrderId('abc')).toBe('abc')
    expect(shortOrderId('')).toBe('')
  })
})

describe('addKitchenFailure', () => {
  it('appends a new failure, keeping earlier ones (a second failed order must not hide the first)', () => {
    const first = failure({ orderId: 'o1' })
    const second = failure({ orderId: 'o2' })
    expect(addKitchenFailure(addKitchenFailure([], first), second)).toEqual([first, second])
  })

  it('replaces the entry of the same order instead of duplicating it (failed retry)', () => {
    const first = failure({ orderId: 'o1', message: 'eski' })
    const retry = failure({ orderId: 'o1', message: 'yeni' })
    expect(addKitchenFailure([first], retry)).toEqual([retry])
  })

  it('does not mutate its input', () => {
    const list: readonly KitchenPrintFailure[] = [failure({ orderId: 'o1' })]
    addKitchenFailure(list, failure({ orderId: 'o2' }))
    expect(list).toHaveLength(1)
  })
})

describe('removeKitchenFailure', () => {
  it('drops only the matching order', () => {
    const a = failure({ orderId: 'o1' })
    const b = failure({ orderId: 'o2' })
    expect(removeKitchenFailure([a, b], 'o1')).toEqual([b])
  })

  it('is a no-op for an unknown order', () => {
    const a = failure({ orderId: 'o1' })
    expect(removeKitchenFailure([a], 'zzz')).toEqual([a])
  })
})

describe('describeKitchenFailure', () => {
  it('names the table, the short order number and the cause', () => {
    expect(describeKitchenFailure(failure())).toBe(
      'Mutfak fişi yazdırılamadı (Masa 7 · #a1b2c3d4): yazıcı bağlı değil',
    )
  })

  it('omits the table when the label is unknown', () => {
    expect(describeKitchenFailure(failure({ tableLabel: '' }))).toBe(
      'Mutfak fişi yazdırılamadı (#a1b2c3d4): yazıcı bağlı değil',
    )
  })
})

describe('applyKitchenPrintResult', () => {
  const failed = (over: Partial<KitchenPrintResultEvent> = {}): KitchenPrintResultEvent => ({
    order_id: 'o1',
    table_label: 'Masa 5',
    ok: false,
    error: 'yazıcı bağlı değil',
    ...over,
  })

  it('records a dispatcher-side failure so the banner offers a reprint', () => {
    expect(applyKitchenPrintResult([], failed())).toEqual([
      { orderId: 'o1', tableLabel: 'Masa 5', message: 'yazıcı bağlı değil' },
    ])
  })

  it('clears an earlier failure when a later automatic retry succeeds', () => {
    const start = applyKitchenPrintResult([], failed())
    expect(applyKitchenPrintResult(start, failed({ ok: true, error: undefined }))).toEqual([])
  })

  it('a success for an unrelated order leaves other failures alone', () => {
    const start = applyKitchenPrintResult([], failed({ order_id: 'o1' }))
    expect(applyKitchenPrintResult(start, failed({ order_id: 'o2', ok: true }))).toEqual(start)
  })

  it('a repeated failure of the same order replaces its entry', () => {
    const start = applyKitchenPrintResult([], failed({ error: 'eski' }))
    const next = applyKitchenPrintResult(start, failed({ error: 'yeni' }))
    expect(next).toHaveLength(1)
    expect(next[0].message).toBe('yeni')
  })

  it('shows a fallback message when the failure carries no text', () => {
    const next = applyKitchenPrintResult([], failed({ error: undefined }))
    expect(next[0].message).not.toBe('')
  })
})
