import { describe, expect, it } from 'vitest'
import {
  addNoticeFailure,
  describeNoticeFailure,
  removeNoticeFailure,
  type KitchenNoticeSource,
  type NoticeFailure,
} from './kitchenNotice'

const transfer: KitchenNoticeSource = { kind: 'transfer', from: 'Masa 3', to: 'Masa 7', items: [] }
const move: KitchenNoticeSource = { kind: 'move-items', from: 'Masa 3', to: 'Masa 7', items: [{ name: 'Lahmacun', quantity: 2 }] }

function failure(id: string, notice: KitchenNoticeSource | null = transfer, message = 'kağıt sıkıştı'): NoticeFailure {
  return { id, notice, message }
}

describe('notice failures', () => {
  it('keeps every failure — a second one must not hide the first', () => {
    const list = addNoticeFailure(addNoticeFailure([], failure('a')), failure('b'))
    expect(list.map((f) => f.id)).toEqual(['a', 'b'])
  })

  it('replaces a failure with the same id instead of duplicating it', () => {
    const list = addNoticeFailure([failure('a', transfer, 'ilk')], failure('a', transfer, 'ikinci'))
    expect(list).toHaveLength(1)
    expect(list[0].message).toBe('ikinci')
  })

  it('removes one by id', () => {
    expect(removeNoticeFailure([failure('a'), failure('b')], 'a').map((f) => f.id)).toEqual(['b'])
  })
})

describe('describeNoticeFailure', () => {
  it.each([
    { notice: transfer, what: 'Masa taşındı bilgi fişi' },
    { notice: { ...transfer, kind: 'merge' }, what: 'Birleştirme bilgi fişi' },
    { notice: move, what: 'Kalem taşıma bilgi fişi' },
  ])('names the slip that did not reach the kitchen ($what)', ({ notice, what }) => {
    const text = describeNoticeFailure(failure('x', notice))
    expect(text).toContain(what)
    expect(text).toContain('Masa 3 → Masa 7')
    expect(text).toContain('kağıt sıkıştı')
  })

  it('still says what happened when the notice could not even be prepared', () => {
    const text = describeNoticeFailure(failure('x', null, 'adisyon bilgisi okunamadı'))
    expect(text).toContain('Mutfak bilgi fişi')
    expect(text).toContain('adisyon bilgisi okunamadı')
  })
})
