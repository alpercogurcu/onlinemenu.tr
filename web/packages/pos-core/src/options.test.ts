import { describe, expect, it } from 'vitest'
import {
  composeFreeNote,
  composeOptionNote,
  defaultSelection,
  formatDelta,
  groupHint,
  requiredCount,
  selectedModifiers,
  toggleModifier,
  unitPriceWith,
  unmetGroupIds,
  type ModifierGroupSource,
} from './options'

function group(overrides: Partial<ModifierGroupSource> & Pick<ModifierGroupSource, 'id'>): ModifierGroupSource {
  return {
    name: overrides.id,
    selection_type: 'single',
    min_selections: 0,
    max_selections: 0,
    is_required: false,
    sort_order: 0,
    modifiers: [],
    ...overrides,
  }
}

const spice = group({
  id: 'spice',
  name: 'Acı',
  is_required: true,
  min_selections: 1,
  modifiers: [
    { id: 'mild', name: 'Acısız', price_delta: 0 },
    { id: 'hot', name: 'Acılı', price_delta: 0 },
    { id: 'xhot', name: 'Çok acı', price_delta: 0 },
  ],
})
const extras = group({
  id: 'extras',
  name: 'Ekstra',
  selection_type: 'multiple',
  max_selections: 2,
  modifiers: [
    { id: 'cheese', name: 'Peynir', price_delta: 1500 },
    { id: 'lavash', name: 'Lavaş', price_delta: 500 },
    { id: 'onion', name: 'Soğan', price_delta: 0 },
  ],
})

describe('requiredCount', () => {
  it.each([
    { name: 'optional single', g: group({ id: 'a' }), want: 0 },
    { name: 'required single', g: group({ id: 'a', is_required: true }), want: 1 },
    { name: 'optional multiple with min 0', g: group({ id: 'a', selection_type: 'multiple' }), want: 0 },
    { name: 'required multiple defaults to 1', g: group({ id: 'a', selection_type: 'multiple', is_required: true }), want: 1 },
    { name: 'required multiple honours min 3', g: group({ id: 'a', selection_type: 'multiple', is_required: true, min_selections: 3 }), want: 3 },
    { name: 'a min without the required flag still counts', g: group({ id: 'a', selection_type: 'multiple', min_selections: 2 }), want: 2 },
  ])('$name', ({ g, want }) => {
    expect(requiredCount(g)).toBe(want)
  })
})

describe('defaultSelection', () => {
  it('pre-selects the first option of every required group so the fast path is two taps', () => {
    expect(defaultSelection([spice, extras])).toEqual({ spice: ['mild'], extras: [] })
  })

  it('pre-selects as many options as a required multiple group demands', () => {
    const g = group({ id: 'g', selection_type: 'multiple', min_selections: 2, modifiers: extras.modifiers })
    expect(defaultSelection([g])).toEqual({ g: ['cheese', 'lavash'] })
  })
})

describe('toggleModifier', () => {
  it('switches a single group like a radio', () => {
    expect(toggleModifier(spice, ['mild'], 'hot')).toEqual({ next: ['hot'], blocked: false })
  })

  it('does not let a required single group be emptied', () => {
    expect(toggleModifier(spice, ['hot'], 'hot').next).toEqual(['hot'])
  })

  it('lets an optional single group be cleared', () => {
    const optional = group({ id: 'o', modifiers: spice.modifiers })
    expect(toggleModifier(optional, ['hot'], 'hot').next).toEqual([])
  })

  it('toggles multiple groups on and off', () => {
    const on = toggleModifier(extras, [], 'cheese')
    expect(on.next).toEqual(['cheese'])
    expect(toggleModifier(extras, on.next, 'cheese').next).toEqual([])
  })

  it('refuses a selection past max_selections and says so', () => {
    expect(toggleModifier(extras, ['cheese', 'lavash'], 'onion')).toEqual({ next: ['cheese', 'lavash'], blocked: true })
  })

  it('treats max_selections 0 as unlimited', () => {
    const unlimited = { ...extras, max_selections: 0 }
    expect(toggleModifier(unlimited, ['cheese', 'lavash'], 'onion').next).toEqual(['cheese', 'lavash', 'onion'])
  })
})

describe('unmetGroupIds', () => {
  it('reports required groups with too few options', () => {
    expect(unmetGroupIds([spice, extras], { spice: [], extras: [] })).toEqual(['spice'])
  })

  it('is empty when every requirement is met', () => {
    expect(unmetGroupIds([spice, extras], { spice: ['hot'], extras: [] })).toEqual([])
  })

  it('counts a missing entry as no selection', () => {
    expect(unmetGroupIds([spice], {})).toEqual(['spice'])
  })
})

describe('selectedModifiers', () => {
  it('lists chosen options in display order, not tap order', () => {
    const picked = selectedModifiers([spice, extras], { spice: ['hot'], extras: ['lavash', 'cheese'] })
    expect(picked.map((m) => m.id)).toEqual(['hot', 'cheese', 'lavash'])
    expect(picked[1]).toEqual({ id: 'cheese', groupId: 'extras', name: 'Peynir', priceDelta: 1500 })
  })

  it('ignores ids that are not (or no longer) in the group', () => {
    expect(selectedModifiers([spice], { spice: ['gone'] })).toEqual([])
  })
})

describe('unitPriceWith', () => {
  it('is the base price plus every price delta — the formula the server validates', () => {
    const picked = selectedModifiers([spice, extras], { spice: ['hot'], extras: ['cheese', 'lavash'] })
    expect(unitPriceWith(6000, picked)).toBe(6000 + 1500 + 500)
  })

  it('handles a negative delta', () => {
    expect(unitPriceWith(6000, [{ id: 'x', groupId: 'g', name: 'Küçük', priceDelta: -500 }])).toBe(5500)
  })
})

describe('formatDelta', () => {
  it.each([
    { delta: 1500, want: '+₺15' },
    { delta: 750, want: '+₺7,50' },
    { delta: -500, want: '−₺5' },
    { delta: 0, want: '' },
  ])('$delta -> "$want"', ({ delta, want }) => {
    expect(formatDelta(delta)).toBe(want)
  })
})

describe('composeOptionNote', () => {
  it('joins options and the free note the way the kitchen ticket prints them', () => {
    const picked = selectedModifiers([spice, extras], { spice: ['hot'], extras: ['lavash'] })
    expect(composeOptionNote(picked, 'Soğansız')).toBe('Acılı | Lavaş(+5) | Soğansız')
  })

  it('prints fractional and negative deltas', () => {
    expect(
      composeOptionNote(
        [
          { id: '1', groupId: 'g', name: 'Sos', priceDelta: 750 },
          { id: '2', groupId: 'g', name: 'Küçük', priceDelta: -500 },
        ],
        '',
      ),
    ).toBe('Sos(+7,50) | Küçük(-5)')
  })

  it('is empty for a plain line', () => {
    expect(composeOptionNote([], '')).toBe('')
    expect(composeOptionNote([], '   ')).toBe('')
  })
})

describe('composeFreeNote', () => {
  it('joins chips and typed text', () => {
    expect(composeFreeNote(['Soğansız', 'Az pişmiş'], ' bol sos ')).toBe('Soğansız, Az pişmiş, bol sos')
  })

  it('is empty when nothing was chosen', () => {
    expect(composeFreeNote([], '')).toBe('')
  })
})

describe('groupHint', () => {
  it.each([
    { name: 'required single', g: spice, want: 'zorunlu, tek seçim' },
    { name: 'optional capped multiple', g: extras, want: 'opsiyonel, en fazla 2' },
    { name: 'optional uncapped multiple', g: { ...extras, max_selections: 0 }, want: 'opsiyonel, çoklu seçim' },
    { name: 'required multiple', g: group({ id: 'r', selection_type: 'multiple', is_required: true }), want: 'zorunlu, çoklu seçim' },
  ])('$name', ({ g, want }) => {
    expect(groupHint(g)).toBe(want)
  })
})
