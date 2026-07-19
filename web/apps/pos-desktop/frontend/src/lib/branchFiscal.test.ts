import { describe, expect, it } from 'vitest'
import {
  parseBranchFiscalEvent,
  toServerCompletedMap,
  type BranchFiscalPendingEvent,
} from './branchFiscal'

// Only the pure wire -> domain step is covered here. The hook's resetKey
// behaviour (state dropped to empty during render when session/branch changes)
// needs a React renderer (this app has no @testing-library), and the hook module
// imports the gitignored generated wailsjs runtime — see branchFiscal.ts's header.

function event(over: Partial<BranchFiscalPendingEvent> = {}): BranchFiscalPendingEvent {
  return { branch_id: 'b1', as_of: '2026-07-19T10:00:00Z', ...over }
}

describe('parseBranchFiscalEvent', () => {
  it('returns empty state for a missing or fieldless event', () => {
    expect(parseBranchFiscalEvent(undefined)).toEqual({ pending: [], recentlySettled: [] })
    expect(parseBranchFiscalEvent(null)).toEqual({ pending: [], recentlySettled: [] })
    expect(parseBranchFiscalEvent(event())).toEqual({ pending: [], recentlySettled: [] })
  })

  it('maps snake_case wire fields to the domain shape', () => {
    const got = parseBranchFiscalEvent(
      event({
        pending: [
          {
            payment_id: 'p1',
            check_id: 'chk-1',
            amount_total: 12_500,
            registered_at: '2026-07-19T09:59:56Z',
            age_seconds: 4,
          },
        ],
        recently_settled: [
          {
            payment_id: 'p0',
            check_id: 'chk-0',
            status: 'failed',
            amount_total: 4_200,
            failure_reason: 'ECR timeout after 30s',
            settled_at: '2026-07-19T09:58:00Z',
          },
        ],
      }),
    )

    expect(got.pending).toEqual([
      { paymentId: 'p1', checkId: 'chk-1', amountTotal: 12_500, ageSeconds: 4 },
    ])
    expect(got.recentlySettled).toEqual([
      {
        paymentId: 'p0',
        checkId: 'chk-0',
        status: 'failed',
        amountTotal: 4_200,
        failureReason: 'ECR timeout after 30s',
      },
    ])
  })

  it('drops a settled item with an unrecognized status instead of guessing', () => {
    const got = parseBranchFiscalEvent(
      event({
        recently_settled: [
          { payment_id: 'p1', check_id: 'c', status: 'weird', amount_total: 100, settled_at: '' },
        ],
      }),
    )
    expect(got.recentlySettled).toEqual([])
  })

  it('coerces a non-finite or absent amount to 0 rather than NaN', () => {
    // NaN would propagate through settledTotal/collectableRemaining and blank
    // the entire money column instead of failing visibly.
    const got = parseBranchFiscalEvent(
      event({
        pending: [
          {
            payment_id: 'p1',
            check_id: 'c',
            amount_total: NaN,
            registered_at: '',
            age_seconds: Infinity,
          },
        ],
        recently_settled: [
          // A payload from a backend that predates the amount_total addendum.
          { payment_id: 'p2', check_id: 'c', status: 'completed', settled_at: '' },
        ] as BranchFiscalPendingEvent['recently_settled'],
      }),
    )

    expect(got.pending[0].amountTotal).toBe(0)
    expect(got.pending[0].ageSeconds).toBe(0)
    expect(got.recentlySettled[0].amountTotal).toBe(0)
  })

  it('normalizes an empty failure_reason to undefined', () => {
    const got = parseBranchFiscalEvent(
      event({
        recently_settled: [
          {
            payment_id: 'p1',
            check_id: 'c',
            status: 'failed',
            amount_total: 100,
            failure_reason: '',
            settled_at: '',
          },
        ],
      }),
    )
    expect(got.recentlySettled[0].failureReason).toBeUndefined()
  })
})

describe('toServerCompletedMap', () => {
  it('projects the settlement wire list into an id -> amount map', () => {
    const got = toServerCompletedMap([
      { payment_id: 'pay-1', amount_total: 12500 },
      { payment_id: 'pay-2', amount_total: 4200 },
    ])
    expect(got).toEqual(
      new Map([
        ['pay-1', 12500],
        ['pay-2', 4200],
      ]),
    )
  })

  // An empty settlement is the backend's answer for a check with nothing
  // collected AND for a check in another branch (it refuses to confirm the id
  // exists). Both must read as "no settled money", never as unknown.
  it('returns an empty map for an empty, missing or null list', () => {
    expect(toServerCompletedMap([])).toEqual(new Map())
    expect(toServerCompletedMap(undefined)).toEqual(new Map())
    expect(toServerCompletedMap(null)).toEqual(new Map())
  })

  // A NaN amount would propagate through settledTotal and blank the entire
  // money column instead of failing visibly — the same rule the branch event
  // parser applies.
  it('collapses a missing or garbage amount to 0 rather than NaN', () => {
    const got = toServerCompletedMap([
      { payment_id: 'pay-1' } as never,
      { payment_id: 'pay-2', amount_total: 'x' } as never,
    ])
    expect(got.get('pay-1')).toBe(0)
    expect(got.get('pay-2')).toBe(0)
  })

  // The id is the dedupe key every consumer relies on (settledTotal,
  // remoteCompletedOnly). An entry without one would become a phantom credit
  // under an empty-string key that no other source could ever match.
  it('drops an entry with no payment id', () => {
    expect(toServerCompletedMap([{ payment_id: '', amount_total: 500 }])).toEqual(new Map())
  })
})
