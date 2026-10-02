import { describe, expect, it } from 'vitest'
import { payRowState } from './payRow'

describe('payRowState', () => {
  it.each([
    { quantity: 1, paid: 0, selected: 0, expected: { available: 1, selected: 0, settled: false, partialLabel: '', stepper: false } },
    { quantity: 1, paid: 1, selected: 0, expected: { available: 0, selected: 0, settled: true, partialLabel: '', stepper: false } },
    { quantity: 2, paid: 0, selected: 1, expected: { available: 2, selected: 1, settled: false, partialLabel: '', stepper: true } },
    {
      quantity: 2,
      paid: 1,
      selected: 0,
      expected: { available: 1, selected: 0, settled: false, partialLabel: '1 ödendi, 1 kalan', stepper: false },
    },
    {
      quantity: 4,
      paid: 1,
      selected: 5,
      expected: { available: 3, selected: 3, settled: false, partialLabel: '1 ödendi, 3 kalan', stepper: true },
    },
    { quantity: 2, paid: 3, selected: 1, expected: { available: 0, selected: 0, settled: true, partialLabel: '', stepper: false } },
  ])('$quantity units, $paid paid, $selected picked', ({ quantity, paid, selected, expected }) => {
    expect(payRowState(quantity, paid, selected)).toEqual(expected)
  })
})
