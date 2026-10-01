import { describe, expect, it } from 'vitest'
import {
  MAX_LINE_QUANTITY,
  addProductToPending,
  changePendingQuantity,
  optionsHash,
  pendingLineTotal,
  pendingTotal,
  pendingUnitPrice,
  toOrderItemInputs,
  updatePendingNote,
  type PendingLine,
  type ProductSource,
} from './cart'
import type { SelectedModifier } from './options'

const HOT: SelectedModifier = { id: 'hot', groupId: 'spice', name: 'Acılı', priceDelta: 0 }
const MILD: SelectedModifier = { id: 'mild', groupId: 'spice', name: 'Acısız', priceDelta: 0 }
const LAVASH: SelectedModifier = { id: 'lavash', groupId: 'extras', name: 'Lavaş', priceDelta: 500 }
const CHEESE: SelectedModifier = { id: 'cheese', groupId: 'extras', name: 'Peynir', priceDelta: 1500 }

const lahmacun: ProductSource = {
  id: 'p-lahmacun',
  name: 'Lahmacun',
  price_amount: 6000,
  currency: 'TRY',
  tax_rate_bps: 1000,
  unit: 'adet',
}
const tea: ProductSource = { ...lahmacun, id: 'p-tea', name: 'Çay', price_amount: 1500 }

function line(overrides: Partial<PendingLine> = {}): PendingLine {
  return {
    clientId: 'a',
    productId: 'p1',
    productName: 'Çay',
    productPriceAmount: 1500,
    productCurrency: 'TRY',
    taxRateBps: 1000,
    unit: 'adet',
    quantity: 2,
    modifiers: [],
    note: '',
    optionsHash: optionsHash([], ''),
    optionsUnavailable: false,
    ...overrides,
  }
}

describe('changePendingQuantity', () => {
  it('increments the addressed line only', () => {
    const lines = [line({ clientId: 'a' }), line({ clientId: 'b', quantity: 1 })]
    const next = changePendingQuantity(lines, 'a', 1)
    expect(next.map((l) => l.quantity)).toEqual([3, 1])
  })

  it('decrements down to 1 and never removes the line — the × button is the deliberate delete', () => {
    const lines = [line({ quantity: 2 })]
    const once = changePendingQuantity(lines, 'a', -1)
    expect(once[0].quantity).toBe(1)
    const twice = changePendingQuantity(once, 'a', -1)
    expect(twice).toHaveLength(1)
    expect(twice[0].quantity).toBe(1)
  })

  it('caps the quantity so a stuck finger cannot order thousands', () => {
    const next = changePendingQuantity([line({ quantity: MAX_LINE_QUANTITY })], 'a', 1)
    expect(next[0].quantity).toBe(MAX_LINE_QUANTITY)
  })

  it('leaves the list untouched for an unknown line', () => {
    const lines = [line()]
    expect(changePendingQuantity(lines, 'zzz', 1)).toEqual(lines)
  })

  it('does not mutate its input', () => {
    const lines = [line()]
    changePendingQuantity(lines, 'a', 1)
    expect(lines[0].quantity).toBe(2)
  })

  it('keeps the options when the quantity changes ("aynısından bir tane daha")', () => {
    const [start] = addProductToPending([], lahmacun, { modifiers: [HOT, LAVASH], note: '' })
    const [next] = changePendingQuantity([start], start.clientId, 1)
    expect(next.quantity).toBe(2)
    expect(next.modifiers).toEqual([HOT, LAVASH])
  })
})

describe('optionsHash', () => {
  it('is independent of the order the options were chosen in', () => {
    expect(optionsHash([HOT, LAVASH], '')).toBe(optionsHash([LAVASH, HOT], ''))
  })

  it('differs for different options', () => {
    expect(optionsHash([HOT], '')).not.toBe(optionsHash([MILD], ''))
    expect(optionsHash([HOT], '')).not.toBe(optionsHash([], ''))
  })

  it('differs for a different note, ignoring surrounding whitespace', () => {
    expect(optionsHash([HOT], 'Soğansız')).not.toBe(optionsHash([HOT], ''))
    expect(optionsHash([HOT], ' Soğansız ')).toBe(optionsHash([HOT], 'Soğansız'))
  })
})

describe('addProductToPending', () => {
  it('adds a plain product with one tap and no options', () => {
    const lines = addProductToPending([], tea)
    expect(lines).toHaveLength(1)
    expect(lines[0]).toMatchObject({ productId: 'p-tea', quantity: 1, modifiers: [], note: '', optionsUnavailable: false })
  })

  it('raises the quantity when the same plain product is tapped again', () => {
    const once = addProductToPending([], tea)
    const twice = addProductToPending(once, tea)
    expect(twice).toHaveLength(1)
    expect(twice[0].quantity).toBe(2)
  })

  it('keeps the same product with different options as separate lines', () => {
    const lines = addProductToPending(addProductToPending([], lahmacun, { modifiers: [HOT] }), lahmacun, { modifiers: [MILD] })
    expect(lines).toHaveLength(2)
    expect(lines.map((l) => l.modifiers[0].id)).toEqual(['hot', 'mild'])
  })

  it('keeps an option-less line apart from an optioned line of the same product', () => {
    const lines = addProductToPending(addProductToPending([], lahmacun), lahmacun, { modifiers: [HOT] })
    expect(lines).toHaveLength(2)
  })

  it('merges the same product with the same options and adds the picked quantity', () => {
    const first = addProductToPending([], lahmacun, { modifiers: [HOT, LAVASH], quantity: 2 })
    const second = addProductToPending(first, lahmacun, { modifiers: [LAVASH, HOT], quantity: 3 })
    expect(second).toHaveLength(1)
    expect(second[0].quantity).toBe(5)
  })

  it('separates lines that differ only by note', () => {
    const lines = addProductToPending(addProductToPending([], lahmacun, { note: 'Soğansız' }), lahmacun, { note: '' })
    expect(lines).toHaveLength(2)
  })

  it('caps a merged quantity', () => {
    const first = addProductToPending([], tea, { quantity: MAX_LINE_QUANTITY })
    expect(addProductToPending(first, tea)[0].quantity).toBe(MAX_LINE_QUANTITY)
  })

  it('gives lines added in the same millisecond distinct client ids', () => {
    const lines = addProductToPending(addProductToPending([], lahmacun, { modifiers: [HOT] }), lahmacun, { modifiers: [MILD] })
    expect(new Set(lines.map((l) => l.clientId)).size).toBe(2)
  })

  it('carries the "options could not be fetched" flag onto the line', () => {
    const [flagged] = addProductToPending([], { ...lahmacun, options_unavailable: true })
    expect(flagged.optionsUnavailable).toBe(true)
  })
})

describe('updatePendingNote', () => {
  const cases: {
    name: string
    lines: PendingLine[]
    clientId: string
    note: string
    want: { count: number; notes?: string[]; quantities?: number[]; same?: boolean }
  }[] = [
    {
      name: 'writes a note onto an option-less line and refreshes its hash',
      lines: [line({ clientId: 'a' })],
      clientId: 'a',
      note: 'Buzsuz',
      want: { count: 1, notes: ['Buzsuz'] },
    },
    {
      name: 'trims surrounding whitespace',
      lines: [line({ clientId: 'a' })],
      clientId: 'a',
      note: '  az şekerli  ',
      want: { count: 1, notes: ['az şekerli'] },
    },
    {
      name: 'clearing the note restores the plain identity',
      lines: [line({ clientId: 'a', note: 'Buzsuz', optionsHash: optionsHash([], 'Buzsuz') })],
      clientId: 'a',
      note: '',
      want: { count: 1, notes: [''] },
    },
    {
      name: 'unknown clientId returns the input unchanged',
      lines: [line({ clientId: 'a' })],
      clientId: 'missing',
      note: 'Buzsuz',
      want: { count: 1, same: true },
    },
    {
      name: 'a whitespace-only edit of an empty note is a no-op',
      lines: [line({ clientId: 'a' })],
      clientId: 'a',
      note: '   ',
      want: { count: 1, same: true },
    },
    {
      name: 'merges into the twin line when the edit makes the options identical',
      lines: [
        line({ clientId: 'a', quantity: 2, note: 'Buzsuz', optionsHash: optionsHash([], 'Buzsuz') }),
        line({ clientId: 'b', quantity: 3 }),
      ],
      clientId: 'a',
      note: '',
      want: { count: 1, quantities: [5], notes: [''] },
    },
    {
      name: 'a merged quantity is capped at MAX_LINE_QUANTITY',
      lines: [
        line({ clientId: 'a', quantity: 10, note: 'Buzsuz', optionsHash: optionsHash([], 'Buzsuz') }),
        line({ clientId: 'b', quantity: MAX_LINE_QUANTITY }),
      ],
      clientId: 'a',
      note: '',
      want: { count: 1, quantities: [MAX_LINE_QUANTITY] },
    },
    {
      name: 'does not merge across different products',
      lines: [
        line({ clientId: 'a', productId: 'p1', note: 'Buzsuz', optionsHash: optionsHash([], 'Buzsuz') }),
        line({ clientId: 'b', productId: 'p2' }),
      ],
      clientId: 'a',
      note: '',
      want: { count: 2, notes: ['', ''] },
    },
  ]

  it.each(cases)('$name', ({ lines, clientId, note, want }) => {
    const next = updatePendingNote(lines, clientId, note)
    expect(next).toHaveLength(want.count)
    if (want.same) expect(next).toBe(lines)
    if (want.notes) expect(next.map((l) => l.note)).toEqual(want.notes)
    if (want.quantities) expect(next.map((l) => l.quantity)).toEqual(want.quantities)
    for (const l of next) expect(l.optionsHash).toBe(optionsHash(l.modifiers, l.note))
  })

  it('keeps an edited line separate from an optioned line of the same product', () => {
    const lines = [
      line({ clientId: 'a' }),
      line({ clientId: 'b', modifiers: [HOT], optionsHash: optionsHash([HOT], '') }),
    ]
    const next = updatePendingNote(lines, 'a', 'Soğansız')
    expect(next).toHaveLength(2)
    expect(next[0].note).toBe('Soğansız')
    expect(next[1].modifiers).toEqual([HOT])
  })
})

describe('pricing', () => {
  it('prices a line at base plus option deltas, times quantity', () => {
    const [l] = addProductToPending([], lahmacun, { modifiers: [HOT, LAVASH, CHEESE], quantity: 2 })
    expect(pendingUnitPrice(l)).toBe(6000 + 500 + 1500)
    expect(pendingLineTotal(l)).toBe((6000 + 500 + 1500) * 2)
  })

  it('sums every line into the pending total', () => {
    const lines = addProductToPending(addProductToPending([], lahmacun, { modifiers: [LAVASH] }), tea, { quantity: 2 })
    expect(pendingTotal(lines)).toBe(6500 + 3000)
  })
})

describe('toOrderItemInputs', () => {
  it('sends the option-inclusive unit price, the base price, the chosen modifier ids and a readable note', () => {
    const lines = addProductToPending([], lahmacun, { modifiers: [HOT, LAVASH], note: 'Soğansız', quantity: 2 })
    expect(toOrderItemInputs(lines)).toEqual([
      {
        product_id: 'p-lahmacun',
        product_name: 'Lahmacun',
        product_price_amount: 6000,
        product_currency: 'TRY',
        tax_rate_bps: 1000,
        quantity: 2,
        unit_price_amount: 6500,
        note: 'Acılı | Lavaş(+5) | Soğansız',
        modifier_ids: ['hot', 'lavash'],
      },
    ])
  })

  it('sends a plain line with an empty note and no modifiers', () => {
    const [input] = toOrderItemInputs(addProductToPending([], tea))
    expect(input.unit_price_amount).toBe(1500)
    expect(input.note).toBe('')
    expect(input.modifier_ids).toEqual([])
  })

  it('tells the kitchen when the options could not be fetched, so the order is never silently option-less', () => {
    const [input] = toOrderItemInputs(addProductToPending([], { ...lahmacun, options_unavailable: true }))
    expect(input.note).toContain('Seçenek alınamadı')
    expect(input.modifier_ids).toEqual([])
    expect(input.unit_price_amount).toBe(6000)
  })
})
