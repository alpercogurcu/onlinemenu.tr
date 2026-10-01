import { describe, expect, it } from 'vitest'
import {
  checksById,
  checkTitle,
  elapsedLabel,
  serviceKind,
  splitServiceChecks,
  type CheckLike,
} from './checkDisplay'

function check(overrides: Partial<CheckLike> & { id: string }): CheckLike {
  return {
    table_label: '',
    opened_at: '2026-10-01T10:00:00.000Z',
    ...overrides,
  }
}

describe('serviceKind', () => {
  it('maps takeaway and delivery, everything else is table service', () => {
    expect(serviceKind(check({ id: 'c1', service_type: 'takeaway' }))).toBe('takeaway')
    expect(serviceKind(check({ id: 'c2', service_type: 'delivery' }))).toBe('delivery')
    expect(serviceKind(check({ id: 'c3', service_type: 'dine_in' }))).toBeNull()
    expect(serviceKind(check({ id: 'c4' }))).toBeNull()
    expect(serviceKind(check({ id: 'c5', service_type: '' }))).toBeNull()
  })
})

describe('splitServiceChecks', () => {
  it('buckets by service_type, preserving order within each bucket', () => {
    const checks = [
      check({ id: 'a', service_type: 'dine_in', table_label: 'Masa 1' }),
      check({ id: 'b', service_type: 'takeaway', customer_name: 'Alper Vural' }),
      check({ id: 'c' }), // older response without the field -> table service
      check({ id: 'd', service_type: 'delivery', customer_name: 'berke d' }),
      check({ id: 'e', service_type: 'takeaway', customer_name: 'Ayşe' }),
    ]
    const split = splitServiceChecks(checks)
    expect(split.dineIn.map((c) => c.id)).toEqual(['a', 'c'])
    expect(split.takeaway.map((c) => c.id)).toEqual(['b', 'e'])
    expect(split.delivery.map((c) => c.id)).toEqual(['d'])
  })
})

describe('checkTitle', () => {
  it('names a table check after its table', () => {
    expect(checkTitle(check({ id: 'c', table_label: 'Masa 7' }))).toBe('Masa 7')
    expect(checkTitle(check({ id: 'c', table_label: '' }))).toBe('Masa')
  })

  it('names a masasız servis check after its customer', () => {
    expect(checkTitle(check({ id: 'c', service_type: 'takeaway', customer_name: 'Alper Vural' }))).toBe(
      'GEL AL · Alper Vural',
    )
    expect(checkTitle(check({ id: 'c', service_type: 'delivery', customer_name: 'berke d' }))).toBe('PAKET · berke d')
  })

  it('falls back to the table label when a service check has no customer name', () => {
    expect(checkTitle(check({ id: 'c', service_type: 'takeaway', table_label: 'Paket servis' }))).toBe(
      'GEL AL · Paket servis',
    )
    expect(checkTitle(check({ id: 'c', service_type: 'takeaway', customer_name: '  ', table_label: '' }))).toBe(
      'GEL AL · Müşteri',
    )
  })
})

describe('elapsedLabel', () => {
  const openedAt = '2026-10-01T10:00:00.000Z'
  const at = (minutes: number) => Date.parse(openedAt) + minutes * 60000

  it('formats minutes and hour+minutes', () => {
    expect(elapsedLabel(openedAt, at(0))).toBe('0 dk')
    expect(elapsedLabel(openedAt, at(38))).toBe('38 dk')
    expect(elapsedLabel(openedAt, at(65))).toBe('1 sa 5 dk')
  })

  it('clamps a clock skew to zero and rejects an unparsable date', () => {
    expect(elapsedLabel(openedAt, at(-5))).toBe('0 dk')
    expect(elapsedLabel('not-a-date', at(10))).toBe('')
  })
})

describe('checksById', () => {
  it('indexes checks by id for the masa planı card lookup', () => {
    const a = check({ id: 'a', total: 199500 })
    const map = checksById([a, check({ id: 'b' })])
    expect(map.get('a')).toBe(a)
    expect(map.get('yok')).toBeUndefined()
  })
})
