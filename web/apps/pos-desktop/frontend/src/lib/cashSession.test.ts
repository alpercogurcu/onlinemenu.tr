import { describe, expect, it } from 'vitest'
import {
  computeDifference,
  denominationTotal,
  emptyDenominationRows,
  isClosingCountStale,
  isValidMovementAmount,
  isValidMovementReason,
  isValidOpeningAmount,
  nonZeroDenominationRows,
  toClosingSnapshot,
  TURKISH_DENOMINATIONS,
  type ClosingSnapshot,
} from './cashSession'

describe('denominationTotal', () => {
  it('sums denomination x count', () => {
    const total = denominationTotal([
      { denominationMinor: 20000, count: 3 }, // 3x 200 TL
      { denominationMinor: 500, count: 4 }, // 4x 5 TL
    ])
    expect(total).toBe(20000 * 3 + 500 * 4)
  })

  it('is zero for an all-zero breakdown', () => {
    expect(denominationTotal(emptyDenominationRows())).toBe(0)
  })

  it('ignores a negative count rather than subtracting it', () => {
    // Defensive: the UI should never produce a negative count, but the
    // arithmetic must not let a stray negative silently understate the total.
    expect(denominationTotal([{ denominationMinor: 10000, count: -2 }])).toBe(0)
  })

  it('covers every fixed denomination with a positive value', () => {
    for (const d of TURKISH_DENOMINATIONS) {
      expect(d.denominationMinor).toBeGreaterThan(0)
    }
  })
})

describe('nonZeroDenominationRows', () => {
  it('drops zero-count rows', () => {
    const rows = [
      { denominationMinor: 20000, count: 0 },
      { denominationMinor: 500, count: 2 },
    ]
    expect(nonZeroDenominationRows(rows)).toEqual([{ denominationMinor: 500, count: 2 }])
  })
})

describe('computeDifference', () => {
  it('is positive when the counted amount exceeds expected (fazla)', () => {
    expect(computeDifference(76500, 76000)).toBe(500)
  })

  it('is negative when the counted amount falls short (açık)', () => {
    expect(computeDifference(75000, 76000)).toBe(-1000)
  })

  it('is zero when they match exactly', () => {
    expect(computeDifference(76000, 76000)).toBe(0)
  })
})

describe('toClosingSnapshot', () => {
  it('returns null for a session not in closing_control', () => {
    expect(
      toClosingSnapshot({
        id: 's1',
        status: 'opened',
        expected_close: 76000,
        closing_counted_amount: 76500,
        closing_submitted_at: '2026-08-01T20:00:00Z',
      }),
    ).toBeNull()
  })

  it('returns null when closing_counted_amount is absent', () => {
    expect(
      toClosingSnapshot({
        id: 's1',
        status: 'closing_control',
        expected_close: 76000,
      }),
    ).toBeNull()
  })

  it('returns null for a null/undefined session', () => {
    expect(toClosingSnapshot(null)).toBeNull()
    expect(toClosingSnapshot(undefined)).toBeNull()
  })

  it('extracts the snapshot fields for a session in closing_control', () => {
    expect(
      toClosingSnapshot({
        id: 's1',
        status: 'closing_control',
        expected_close: 76000,
        closing_counted_amount: 76500,
        closing_submitted_at: '2026-08-01T20:00:00Z',
      }),
    ).toEqual<ClosingSnapshot>({
      sessionId: 's1',
      expectedClose: 76000,
      closingCountedAmount: 76500,
      closingSubmittedAt: '2026-08-01T20:00:00Z',
    })
  })
})

describe('isClosingCountStale', () => {
  const snapshot: ClosingSnapshot = {
    sessionId: 's1',
    expectedClose: 76000,
    closingCountedAmount: 76500,
    closingSubmittedAt: '2026-08-01T20:00:00Z',
  }

  it('is not stale when the live snapshot is identical', () => {
    expect(isClosingCountStale(snapshot, { ...snapshot })).toBe(false)
  })

  it('is stale when expected_close moved (a pending fiscal submission settled)', () => {
    expect(isClosingCountStale(snapshot, { ...snapshot, expectedClose: 78000 })).toBe(true)
  })

  it('is stale when another station submitted a recount (closing_counted_amount changed) even though expected_close did not move', () => {
    expect(
      isClosingCountStale(snapshot, { ...snapshot, closingCountedAmount: 80000, closingSubmittedAt: '2026-08-01T20:05:00Z' }),
    ).toBe(true)
  })

  it('is stale when the session id no longer matches (this session closed, a new one opened on the branch)', () => {
    expect(isClosingCountStale(snapshot, { ...snapshot, sessionId: 's2' })).toBe(true)
  })

  it('is stale when there is no live snapshot at all (session left closing_control, or branch has no active session)', () => {
    expect(isClosingCountStale(snapshot, null)).toBe(true)
  })
})

describe('movement input validation', () => {
  it('rejects a non-positive amount', () => {
    expect(isValidMovementAmount(0)).toBe(false)
    expect(isValidMovementAmount(-100)).toBe(false)
    expect(isValidMovementAmount(Number.NaN)).toBe(false)
  })

  it('accepts a positive amount', () => {
    expect(isValidMovementAmount(500)).toBe(true)
  })

  it('rejects a blank reason', () => {
    expect(isValidMovementReason('')).toBe(false)
    expect(isValidMovementReason('   ')).toBe(false)
  })

  it('accepts a non-blank reason', () => {
    expect(isValidMovementReason('bozuk para')).toBe(true)
  })
})

describe('isValidOpeningAmount', () => {
  it('accepts zero (a drawer legitimately opened empty)', () => {
    expect(isValidOpeningAmount(0)).toBe(true)
  })

  it('rejects a negative amount', () => {
    expect(isValidOpeningAmount(-100)).toBe(false)
  })
})
