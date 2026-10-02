import { describe, expect, it } from 'vitest'
import { ROUNDING_OFF, chargeFor, roundingEnabledFor, roundingOffer, settledAmount, type RoundingPolicy } from './rounding'

const ON: RoundingPolicy = { cashEnabled: true, cardEnabled: true, stepMinor: 500, maxPerCheckMinor: 1000 }

describe('roundingOffer', () => {
  it.each([
    { name: '437,50 → 435,00', due: 43750, remaining: 43750, policy: ON, conceded: 0, want: { rounded: 43500, rounding: 250 } },
    { name: 'step ₺1', due: 43750, remaining: 43750, policy: { ...ON, stepMinor: 100 }, conceded: 0, want: { rounded: 43700, rounding: 50 } },
    { name: 'only cash enabled still offers', due: 43750, remaining: 43750, policy: { ...ON, cardEnabled: false }, conceded: 0, want: { rounded: 43500, rounding: 250 } },
    { name: 'already a multiple', due: 43500, remaining: 43500, policy: ON, conceded: 0, want: null },
    { name: 'rounding off', due: 43750, remaining: 43750, policy: ROUNDING_OFF, conceded: 0, want: null },
    { name: 'partial payment never rounds', due: 20000 + 250, remaining: 43750, policy: ON, conceded: 0, want: null },
    { name: 'ceiling reached by earlier rounding', due: 43750, remaining: 43750, policy: { ...ON, maxPerCheckMinor: 300 }, conceded: 100, want: null },
    { name: 'ceiling exactly met', due: 43750, remaining: 43750, policy: { ...ON, maxPerCheckMinor: 350 }, conceded: 100, want: { rounded: 43500, rounding: 250 } },
    { name: 'below one step rounds nothing to zero', due: 300, remaining: 300, policy: ON, conceded: 0, want: null },
    { name: 'nothing due', due: 0, remaining: 0, policy: ON, conceded: 0, want: null },
    { name: 'invalid step', due: 43750, remaining: 43750, policy: { ...ON, stepMinor: 0 }, conceded: 0, want: null },
  ])('$name', ({ due, remaining, policy, conceded, want }) => {
    expect(roundingOffer({ due, remaining, policy, conceded })).toEqual(want)
  })
})

describe('roundingEnabledFor', () => {
  it('reads the switch of the method', () => {
    const cashOnly = { ...ON, cardEnabled: false }
    expect(roundingEnabledFor(cashOnly, 'cash')).toBe(true)
    expect(roundingEnabledFor(cashOnly, 'card')).toBe(false)
  })
})

describe('settledAmount', () => {
  it('adds the rounding conceded to the money taken', () => {
    expect(settledAmount({ amount_total: 43500, rounding_amount: 250 })).toBe(43750)
  })
  it('treats a missing rounding (older backend) as none', () => {
    expect(settledAmount({ amount_total: 43500 })).toBe(43500)
    expect(settledAmount({ amount_total: 43500, rounding_amount: null })).toBe(43500)
  })
})

describe('chargeFor', () => {
  const offer = { rounded: 43500, rounding: 250 }
  it.each([
    { name: 'no offer charges the full due', method: 'cash' as const, offer: null, policy: ON, want: { amount: 43750, rounding: 0 } },
    { name: 'applied rounding reaches cash', method: 'cash' as const, offer, policy: ON, want: { amount: 43500, rounding: 250 } },
    { name: 'applied rounding reaches card when its switch is on', method: 'card' as const, offer, policy: ON, want: { amount: 43500, rounding: 250 } },
    { name: 'card switch off keeps the full due on card', method: 'card' as const, offer, policy: { ...ON, cardEnabled: false }, want: { amount: 43750, rounding: 0 } },
  ])('$name', ({ method, offer, policy, want }) => {
    const charge = chargeFor(method, 43750, offer, policy)
    expect(charge).toEqual(want)
    expect(charge.amount + charge.rounding).toBe(43750)
  })
})
