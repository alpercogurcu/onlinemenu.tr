import { describe, expect, it } from 'vitest'
import {
  checkIdsAwaitingFiscal,
  closeBlockReason,
  collectableRemaining,
  isFullyPaid,
  remoteCompletedOnly,
  remotePendingOnly,
  reservedTotal,
  settledTotal,
  unreportedRemoteFailures,
  type RemotePendingFiscal,
  type RemoteSettledFiscal,
  type TrackedPayment,
} from './fiscalStatus'

// Money arithmetic across two sources (this station's tracked payments and the
// branch-wide feed) is where a double-collection bug would live, so these
// exercise the merge/dedupe rules rather than the trivial getters.

function tracked(over: Partial<TrackedPayment> & { id: string }): TrackedPayment {
  return {
    checkId: 'chk-1',
    amountTotal: 10_000,
    status: 'pending',
    receivedAmount: 10_000,
    registeredAtMs: 0,
    ...over,
  }
}

function remotePending(over: Partial<RemotePendingFiscal> & { paymentId: string }): RemotePendingFiscal {
  return { checkId: 'chk-1', amountTotal: 10_000, ageSeconds: 5, ...over }
}

function remoteSettled(over: Partial<RemoteSettledFiscal> & { paymentId: string }): RemoteSettledFiscal {
  return { checkId: 'chk-1', status: 'completed', amountTotal: 10_000, ...over }
}

const NO_SERVER_PAYMENTS: ReadonlyMap<string, number> = new Map()

describe('remotePendingOnly', () => {
  it('drops a remote item this station already tracks', () => {
    const got = remotePendingOnly([remotePending({ paymentId: 'p1' })], [tracked({ id: 'p1' })])
    expect(got).toEqual([])
  })

  it('drops it regardless of the tracked status, so a resolved payment is not resurrected', () => {
    for (const status of ['completed', 'failed', 'voided', 'unknown'] as const) {
      const got = remotePendingOnly([remotePending({ paymentId: 'p1' })], [tracked({ id: 'p1', status })])
      expect(got, `status ${status}`).toEqual([])
    }
  })

  it('keeps a payment registered at another station', () => {
    const got = remotePendingOnly([remotePending({ paymentId: 'other' })], [tracked({ id: 'mine' })])
    expect(got.map((r) => r.paymentId)).toEqual(['other'])
  })
})

describe('reservedTotal', () => {
  it("does not double-reserve this station's own pending payment", () => {
    const own = [tracked({ id: 'p1', amountTotal: 10_000 })]
    // The branch feed reports the same payment back to us.
    expect(reservedTotal(own, [remotePending({ paymentId: 'p1', amountTotal: 10_000 })])).toBe(10_000)
  })

  it("adds another station's pending payment to the reservation", () => {
    const own = [tracked({ id: 'mine', amountTotal: 10_000 })]
    expect(reservedTotal(own, [remotePending({ paymentId: 'other', amountTotal: 2_500 })])).toBe(12_500)
  })
})

describe('remoteCompletedOnly', () => {
  it('credits a payment completed at another station', () => {
    const got = remoteCompletedOnly([remoteSettled({ paymentId: 'other' })], [], NO_SERVER_PAYMENTS)
    expect(got.map((r) => r.paymentId)).toEqual(['other'])
  })

  it('never credits failed or voided money — that cash is genuinely collectable again', () => {
    const settled = [
      remoteSettled({ paymentId: 'f', status: 'failed' }),
      remoteSettled({ paymentId: 'v', status: 'voided' }),
    ]
    expect(remoteCompletedOnly(settled, [], NO_SERVER_PAYMENTS)).toEqual([])
  })

  it('skips a payment already counted via tracked or serverCompleted', () => {
    const settled = [remoteSettled({ paymentId: 'p1' })]
    expect(remoteCompletedOnly(settled, [tracked({ id: 'p1', status: 'completed' })], NO_SERVER_PAYMENTS)).toEqual([])
    expect(remoteCompletedOnly(settled, [], new Map([['p1', 10_000]]))).toEqual([])
  })

  it('skips a tracked payment sitting at `unknown`, which already counts as settled', () => {
    // The cashier case: GetPayment 403s, so the payment never leaves `unknown`
    // — and countsAsSettled already credits it. Crediting again would overpay.
    const got = remoteCompletedOnly(
      [remoteSettled({ paymentId: 'p1' })],
      [tracked({ id: 'p1', status: 'unknown' })],
      NO_SERVER_PAYMENTS,
    )
    expect(got).toEqual([])
  })
})

describe('settledTotal with branch-wide settlements', () => {
  it('counts a remote completed payment exactly once', () => {
    const total = settledTotal(NO_SERVER_PAYMENTS, [], [remoteSettled({ paymentId: 'other', amountTotal: 7_500 })])
    expect(total).toBe(7_500)
  })

  it('does not double-count when the same payment is also in serverCompleted', () => {
    const total = settledTotal(
      new Map([['p1', 10_000]]),
      [],
      [remoteSettled({ paymentId: 'p1', amountTotal: 10_000 })],
    )
    expect(total).toBe(10_000)
  })
})

describe('collectableRemaining — the cross-station double-collection guard', () => {
  it('holds back money a colleague has already collected and had registered', () => {
    // ₺100 check, ₺100 taken at another till and its receipt already cut.
    const remaining = collectableRemaining(
      10_000,
      NO_SERVER_PAYMENTS,
      [],
      [],
      [remoteSettled({ paymentId: 'other', amountTotal: 10_000 })],
    )
    expect(remaining).toBe(0)
  })

  it('releases money whose fiscal registration failed', () => {
    const remaining = collectableRemaining(
      10_000,
      NO_SERVER_PAYMENTS,
      [],
      [],
      [remoteSettled({ paymentId: 'other', amountTotal: 10_000, status: 'failed' })],
    )
    expect(remaining).toBe(10_000)
  })

  it("holds back a colleague's still-pending payment too", () => {
    const remaining = collectableRemaining(10_000, NO_SERVER_PAYMENTS, [], [
      remotePending({ paymentId: 'other', amountTotal: 4_000 }),
    ])
    expect(remaining).toBe(6_000)
  })

  it('never goes negative on an overpaid check', () => {
    const remaining = collectableRemaining(
      10_000,
      NO_SERVER_PAYMENTS,
      [],
      [],
      [remoteSettled({ paymentId: 'other', amountTotal: 15_000 })],
    )
    expect(remaining).toBe(0)
  })
})

describe('isFullyPaid', () => {
  // Regression guard: fed from a different settled sum than collectableRemaining,
  // a check paid off at another till shows remaining=0 AND fullyPaid=false —
  // Receipt.tsx then offers neither "Nakit al" nor the close button.
  it('is true for a check paid in full at another station', () => {
    const settled = [remoteSettled({ paymentId: 'other', amountTotal: 10_000 })]
    expect(isFullyPaid(10_000, NO_SERVER_PAYMENTS, [], settled)).toBe(true)
    expect(collectableRemaining(10_000, NO_SERVER_PAYMENTS, [], [], settled)).toBe(0)
  })

  it('stays false while the remote money is only pending', () => {
    expect(isFullyPaid(10_000, NO_SERVER_PAYMENTS, [], [])).toBe(false)
  })
})

describe('closeBlockReason', () => {
  it('returns null when nothing is pending anywhere', () => {
    expect(closeBlockReason([], [])).toBeNull()
  })

  it('names the other station when only a remote payment is pending', () => {
    const reason = closeBlockReason([], [remotePending({ paymentId: 'other' })])
    expect(reason).toBe('1 ödemenin mali kaydı başka bir istasyonda bekleniyor')
  })

  it('counts a payment once when both sources report it', () => {
    const reason = closeBlockReason([tracked({ id: 'p1' })], [remotePending({ paymentId: 'p1' })])
    expect(reason).toBe('1 ödemenin mali kaydı bekleniyor')
  })

  it('breaks down a mixed local/remote block', () => {
    const reason = closeBlockReason([tracked({ id: 'mine' })], [remotePending({ paymentId: 'other' })])
    expect(reason).toBe('2 ödemenin mali kaydı bekleniyor (1 tanesi başka istasyonda)')
  })
})

describe('checkIdsAwaitingFiscal', () => {
  it('unions both sources without duplicating a shared payment', () => {
    const ids = checkIdsAwaitingFiscal(
      [tracked({ id: 'p1', checkId: 'chk-1' })],
      [remotePending({ paymentId: 'p1', checkId: 'chk-1' }), remotePending({ paymentId: 'p2', checkId: 'chk-2' })],
    )
    expect([...ids].sort()).toEqual(['chk-1', 'chk-2'])
  })
})

describe('unreportedRemoteFailures', () => {
  it('surfaces a failure for a payment this station could only resolve to `unknown`', () => {
    // The cashier case — the only channel that ever reports the failure.
    const got = unreportedRemoteFailures(
      [remoteSettled({ paymentId: 'p1', status: 'failed' })],
      [tracked({ id: 'p1', status: 'unknown' })],
    )
    expect(got.map((f) => f.paymentId)).toEqual(['p1'])
  })

  it('suppresses one already shown as failed on the payment row', () => {
    const got = unreportedRemoteFailures(
      [remoteSettled({ paymentId: 'p1', status: 'failed' })],
      [tracked({ id: 'p1', status: 'failed' })],
    )
    expect(got).toEqual([])
  })

  it('ignores completed and voided settlements', () => {
    const got = unreportedRemoteFailures(
      [remoteSettled({ paymentId: 'a' }), remoteSettled({ paymentId: 'b', status: 'voided' })],
      [],
    )
    expect(got).toEqual([])
  })
})
