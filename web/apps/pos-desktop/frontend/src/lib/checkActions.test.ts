import { describe, expect, it } from 'vitest'
import { canPickTable, confirmMerge, moveNotice, targetPrompt, type PlanTable } from './checkActions'

const CURRENT = 'check-current'

function table(overrides: Partial<PlanTable>): PlanTable {
  return { name: 'Masa 1', status: 'available', ...overrides }
}

describe('canPickTable — transfer', () => {
  it('accepts only a free table', () => {
    expect(canPickTable('transfer', table({ status: 'available' }), CURRENT)).toBe(true)
    expect(canPickTable('transfer', table({ status: 'occupied', active_check_id: 'other' }), CURRENT)).toBe(false)
    expect(canPickTable('transfer', table({ status: 'reserved' }), CURRENT)).toBe(false)
    expect(canPickTable('transfer', table({ status: 'cleaning' }), CURRENT)).toBe(false)
  })
})

describe('canPickTable — merge', () => {
  it('accepts an occupied table holding another adisyon', () => {
    expect(canPickTable('merge', table({ status: 'occupied', active_check_id: 'other' }), CURRENT)).toBe(true)
  })

  it('refuses the table the adisyon already sits on — merging a check into itself is same_check', () => {
    expect(canPickTable('merge', table({ status: 'occupied', active_check_id: CURRENT }), CURRENT)).toBe(false)
  })

  it('refuses free tables and an occupied table with no known adisyon', () => {
    expect(canPickTable('merge', table({ status: 'available' }), CURRENT)).toBe(false)
    expect(canPickTable('merge', table({ status: 'occupied' }), CURRENT)).toBe(false)
  })
})

describe('canPickTable — move-items', () => {
  it('accepts another adisyon or a free table (a new adisyon is opened for it)', () => {
    expect(canPickTable('move-items', table({ status: 'occupied', active_check_id: 'other' }), CURRENT)).toBe(true)
    expect(canPickTable('move-items', table({ status: 'available' }), CURRENT)).toBe(true)
  })

  it('refuses the current adisyon and cleaning / reserved tables', () => {
    expect(canPickTable('move-items', table({ status: 'occupied', active_check_id: CURRENT }), CURRENT)).toBe(false)
    expect(canPickTable('move-items', table({ status: 'cleaning' }), CURRENT)).toBe(false)
    expect(canPickTable('move-items', table({ status: 'reserved' }), CURRENT)).toBe(false)
  })
})

describe('texts', () => {
  it('names what is being moved in the prompt', () => {
    expect(targetPrompt('transfer', 'Masa 4')).toBe('Hedef masayı seçin — Masa 4 taşınacak')
    expect(targetPrompt('merge', 'Masa 4')).toBe('Hedef masayı seçin — Masa 4 seçilen masanın adisyonuna eklenecek')
    expect(targetPrompt('move-items', 'Masa 4')).toBe('Hedef masayı seçin — seçilen kalemler taşınacak')
  })

  it('spells out who survives a merge before it happens', () => {
    const confirm = confirmMerge('Masa 4', 'Masa 7')
    expect(confirm.title).toBe('Adisyonlar birleştirilsin mi?')
    expect(confirm.message).toContain('Masa 4')
    expect(confirm.message).toContain('Masa 7')
    expect(confirm.message).toMatch(/kapanır|kapatılır/)
  })

  it('reports a move with the right count', () => {
    expect(moveNotice(1, 'Masa 7')).toBe('1 kalem Masa 7 adisyonuna taşındı.')
    expect(moveNotice(3, 'Masa 7')).toBe('3 kalem Masa 7 adisyonuna taşındı.')
  })
})
