// Asynchronous fiscal registration (ADR-FISCAL-002) — the money arithmetic.
//
// POST /api/v1/payments no longer completes a sale synchronously: it returns a
// payment with status "pending" and the ÖKC receipt is cut out-of-band. The
// payment then settles into one of:
//
//   completed → fiş kesildi, money counts as paid
//   failed    → fiş kesilemedi, money does NOT count (cashier must retry)
//   voided    → fiş iptal edildi, money does NOT count
//
// This split is why a "paidTotal" number is no longer enough. Two separate
// buckets are needed, and conflating them is the bug this module exists to
// prevent:
//
//   settled  — completed payments. ONLY these may enable "adisyonu kapat",
//              because the backend's own guard (pos/service.CheckService.Close
//              -> TotalPaidForCheck) counts completed payments only. Crediting
//              a pending payment here makes the UI offer a close that the
//              backend then rejects.
//   reserved — pending payments. These must reduce the REMAINING balance (so a
//              cashier cannot collect the same ₺100 twice while the first
//              receipt is still printing) without enabling the close.
//
// All amounts are integers in kuruş (see format.ts).

import { remainingBalance } from './payment'

/**
 * Lifecycle of a payment as this client models it. The first four mirror
 * backend `domain.PaymentStatus` verbatim. `unknown` is client-only — see
 * PERMISSION GAP below.
 */
export type FiscalStatus = 'pending' | 'completed' | 'failed' | 'voided' | 'unknown'

/**
 * PERMISSION GAP (`unknown`) — GET /api/v1/payments/{id} requires
 * "payment.payment.read", which authz.rego grants to shift_manager/manager
 * only. A plain "cashier" session (the role this POS supports) gets 403 and can
 * never observe its own payment leaving "pending".
 *
 * Treating that as a permanent `pending` would make every adisyon permanently
 * unclosable — strictly worse than the pre-async behavior. So a 403 collapses
 * the payment to `unknown`, which:
 *   - counts as SETTLED (optimistic, exactly the pre-async client behavior), and
 *   - does NOT block the close,
 * leaving the backend's own paid-in-full guard as the real gate: CloseCheck
 * returns ErrInsufficientPayment until the fiscal record actually lands, which
 * the existing error banner already surfaces.
 *
 * The badge still tells the cashier the status is unreadable rather than
 * silently lying that the receipt was cut.
 */
export type TrackedPayment = {
  id: string
  checkId: string
  /** Registered amount in kuruş — what POST /payments accepted. */
  amountTotal: number
  status: FiscalStatus
  /** Raw cash the customer physically handed over for THIS installment (may
   * exceed amountTotal when change is due). Needed for the printed receipt's
   * "ALINAN"/para üstü line — see receivedTotalForPrint. */
  receivedAmount: number
  /** Epoch ms at which this payment was registered. Drives the anti-flicker
   * delay in requirement 6 (see shouldRenderPendingBadge). */
  registeredAtMs: number
  /** Turkish, already passed through describeError — only set for `failed`. */
  failureReason?: string
}

/**
 * BRANCH-WIDE VISIBILITY — one payment somewhere in this branch still awaiting
 * its fiscal record, as reported by the Go poller's `fiscal:branch-pending`
 * event (GET /api/v1/payments/fiscal-pending). Unlike TrackedPayment this is
 * NOT necessarily this station's own payment: the whole point is to see the
 * receipt being cut at the till next to you.
 *
 * DEDUPE RULE, applied everywhere these two sources meet: the branch snapshot
 * ALSO contains this station's own pending payments, so a payment id present
 * in `tracked` must be ignored from the remote list. `tracked` wins because it
 * is strictly richer (it knows receivedAmount and registeredAtMs, which the
 * branch endpoint does not carry) — see remotePendingOnly, the single
 * chokepoint every merged computation below goes through.
 */
export type RemotePendingFiscal = {
  paymentId: string
  checkId: string
  /** Registered amount in kuruş. */
  amountTotal: number
  /** Server-computed age, so the station never has to trust its own clock. */
  ageSeconds: number
}

/** A payment that recently LEFT the branch's pending set. `failureReason` is
 * raw technical text from the fiscal device — diagnostic detail only, never
 * shown to a cashier as the primary message (see describeRemoteFailure). */
export type RemoteSettledFiscal = {
  paymentId: string
  checkId: string
  status: 'completed' | 'failed' | 'voided'
  /** Registered amount in kuruş. Only meaningful for `completed` — see
   * remoteCompletedOnly on why failed/voided amounts are never credited. */
  amountTotal: number
  failureReason?: string
}

const TERMINAL: ReadonlySet<FiscalStatus> = new Set<FiscalStatus>([
  'completed',
  'failed',
  'voided',
  'unknown',
])

/** A terminal payment is never polled again. `unknown` is terminal because the
 * 403 that produced it is a role property, not a transient failure — retrying
 * it every 2s for the life of the check would be a guaranteed-useless request
 * loop against the authz engine. */
export function isTerminal(status: FiscalStatus): boolean {
  return TERMINAL.has(status)
}

/** Statuses whose money counts toward "this check has been paid". `unknown` is
 * included optimistically — see TrackedPayment's doc comment. */
function countsAsSettled(status: FiscalStatus): boolean {
  return status === 'completed' || status === 'unknown'
}

/**
 * Money that has actually settled: server-recorded completed payments (keyed by
 * id, from ListCheckPayments) PLUS this session's own tracked payments that
 * have settled and are not already in that server snapshot.
 *
 * The id-keyed dedup matters: once a tracked payment completes, the next
 * ListCheckPayments refresh returns it too, and naively summing both buckets
 * would double-count it into an overpaid check.
 */
export function settledTotal(
  serverCompleted: ReadonlyMap<string, number>,
  tracked: readonly TrackedPayment[],
  remoteSettled: readonly RemoteSettledFiscal[] = [],
): number {
  let total = 0
  for (const amount of serverCompleted.values()) total += amount
  for (const p of tracked) {
    if (countsAsSettled(p.status) && !serverCompleted.has(p.id)) total += p.amountTotal
  }
  for (const r of remoteCompletedOnly(remoteSettled, tracked, serverCompleted)) {
    total += r.amountTotal
  }
  return total
}

/**
 * Remote payments that COMPLETED and are not already accounted for locally.
 *
 * This is the FAST path for the cross-station case. A payment completed at
 * another till drops out of the branch `pending` list, releasing its
 * reservation; this credits it from the same snapshot in the very next poll,
 * with no extra round trip. `serverCompleted` (see below) then confirms it
 * durably.
 *
 * DEDUPE — skipped when the id appears in `tracked` OR in `serverCompleted`,
 * the same "local record wins" rule remotePendingOnly applies. A manager
 * session gets the payment via serverCompleted; this station's own payments
 * live in tracked (as `completed`, or as `unknown` which already counts as
 * settled). Crediting it a second time here would overpay the check.
 *
 * `failed`/`voided` are deliberately NOT credited: no receipt was cut and that
 * cash genuinely is collectable again.
 *
 * WINDOW LIMIT — CLOSED. The branch snapshot's `recently_settled` window is
 * finite (~5 min), and this list used to be a cashier's ONLY credit for a
 * payment completed elsewhere: once it aged out, the money vanished from every
 * view the cashier had and snapped back into "kalan", inviting a second
 * collection. `serverCompleted` is now filled for a cashier session too, from
 * the windowless check-scoped GET /payments/checks/{id}/settlement
 * (payment.fiscal_status.read — the narrower action pos.go's ListCheckPayments
 * comment called for, rather than widening payment.payment.read). The credit
 * therefore outlives the window, and no double-count arises: this function
 * already skips any id present in serverCompleted.
 *
 * REMAINING ASSUMPTIONS, in the order they would bite:
 *   - The session holds payment.fiscal_status.read. Without it BOTH sources
 *     403 and serverCompleted stays empty, degrading to the pre-feature
 *     behavior (this list alone, window and all). Fail-open by design — a
 *     permission gap must never block a cashier.
 *   - The settlement read is trigger-driven, not continuous: it fires on check
 *     selection and whenever the branch snapshot reports movement on the
 *     SELECTED check (see App.tsx's branchSignalForSelected). Money collected
 *     on a check nobody has selected is therefore credited when it is next
 *     selected — which is the only moment it can be collected against anyway.
 *   - The backend reports `completed` only. A payment stuck `pending`
 *     server-side is still reserved via the branch snapshot, not credited.
 */
export function remoteCompletedOnly(
  settled: readonly RemoteSettledFiscal[],
  tracked: readonly TrackedPayment[],
  serverCompleted: ReadonlyMap<string, number>,
): RemoteSettledFiscal[] {
  const trackedIds = new Set(tracked.map((p) => p.id))
  return settled.filter(
    (s) => s.status === 'completed' && !trackedIds.has(s.paymentId) && !serverCompleted.has(s.paymentId),
  )
}

/** Narrows a branch snapshot's settled items to one check. */
export function remoteSettledForCheck(
  settled: readonly RemoteSettledFiscal[],
  checkId: string | null,
): RemoteSettledFiscal[] {
  if (!checkId) return []
  return settled.filter((s) => s.checkId === checkId)
}

/**
 * The dedupe chokepoint. Drops every remote pending item this station already
 * tracks itself, by payment id. Without it a station's own in-flight payment
 * would be counted twice — once as `tracked`, once as `remote` — reserving
 * double its amount and reporting "2 ödemenin mali kaydı bekleniyor" for one
 * payment.
 *
 * Matches against ALL tracked payments, not just pending ones: a payment this
 * station has already resolved to failed/unknown must not be resurrected as a
 * remote pending just because the branch snapshot has not caught up yet.
 */
export function remotePendingOnly(
  remote: readonly RemotePendingFiscal[],
  tracked: readonly TrackedPayment[],
): RemotePendingFiscal[] {
  const trackedIds = new Set(tracked.map((p) => p.id))
  return remote.filter((r) => !trackedIds.has(r.paymentId))
}

/** Money committed to an in-flight fiscal registration: not yet paid, but not
 * collectable again either. Includes payments registered at ANOTHER station in
 * this branch (`remote`, already scoped to the check in question by the
 * caller) — that money is just as uncollectable as this station's own. */
/*
 * KNOWN TRANSIENT (errs safe, self-correcting) — now that serverCompleted is
 * filled for a cashier session too, this station's OWN payment can briefly be
 * counted in both directions as it completes: the settlement refetch (driven by
 * the 3s branch signal) may credit it in settledTotal before the 2s GetPayment
 * poller has flipped its tracked entry off `pending` here. For up to ~2s
 * collectableRemaining then understates what is still owed.
 *
 * Deliberately NOT guarded by intersecting against serverCompleted: the error
 * is in the safe direction (the cashier is offered LESS to collect, never more,
 * so no double collection), isFullyPaid ignores reserved money entirely so the
 * close gate stays correct throughout, and the next poll resolves it. Adding a
 * serverCompleted parameter here would widen this function's signature for a
 * two-second cosmetic drift.
 */
export function reservedTotal(
  tracked: readonly TrackedPayment[],
  remote: readonly RemotePendingFiscal[] = [],
): number {
  const own = tracked.reduce((sum, p) => (p.status === 'pending' ? sum + p.amountTotal : sum), 0)
  const other = remotePendingOnly(remote, tracked).reduce((sum, r) => sum + r.amountTotal, 0)
  return own + other
}

/**
 * What the customer still owes and the cashier may still collect. Pending
 * payments hold their amount back so a split payment mid-fiscal-registration
 * cannot be double-collected; a payment that ends up `failed`/`voided` releases
 * its amount straight back into this number (it simply stops being reserved).
 *
 * `remote` must already be scoped to THIS check (see remotePendingForCheck).
 * Its effect is the cross-station case of the same rule: a colleague who just
 * took ₺100 on this adisyon at another till has reserved that ₺100 here too.
 */
export function collectableRemaining(
  checkTotal: number,
  serverCompleted: ReadonlyMap<string, number>,
  tracked: readonly TrackedPayment[],
  remote: readonly RemotePendingFiscal[] = [],
  remoteSettled: readonly RemoteSettledFiscal[] = [],
): number {
  // remainingBalance keeps the never-negative rule in one place (an overpaid
  // check must not show a negative "kalan").
  return remainingBalance(
    checkTotal,
    settledTotal(serverCompleted, tracked, remoteSettled) + reservedTotal(tracked, remote),
  )
}

/** Narrows a branch snapshot to the pending items belonging to one check. */
export function remotePendingForCheck(
  remote: readonly RemotePendingFiscal[],
  checkId: string | null,
): RemotePendingFiscal[] {
  if (!checkId) return []
  return remote.filter((r) => r.checkId === checkId)
}

/** Payments still awaiting a fiscal record. Requirement 4: while this is
 * non-empty the check must not be closable. */
export function pendingPayments(tracked: readonly TrackedPayment[]): TrackedPayment[] {
  return tracked.filter((p) => p.status === 'pending')
}

/**
 * Requirement 4's block. Returns null when the close may proceed, otherwise the
 * Turkish reason to show instead of the close button.
 *
 * Note the asymmetry with `isFullyPaid`: a check can be fully settled AND still
 * blocked, when a *further* payment (e.g. an accidental extra installment) is
 * mid-registration. Closing then would strand that payment's receipt.
 */
export function closeBlockReason(
  tracked: readonly TrackedPayment[],
  remote: readonly RemotePendingFiscal[] = [],
): string | null {
  const own = pendingPayments(tracked).length
  const other = remotePendingOnly(remote, tracked).length
  if (own + other === 0) return null

  // The cross-station case gets its own wording on purpose: "başka bir
  // istasyonda" is the difference between a cashier waiting at their own
  // screen and one who needs to go look at the till next to them. A generic
  // count would leave them staring at a block they cannot act on.
  if (own === 0) {
    return `${other} ödemenin mali kaydı başka bir istasyonda bekleniyor`
  }
  if (other === 0) {
    return `${own} ödemenin mali kaydı bekleniyor`
  }
  return `${own + other} ödemenin mali kaydı bekleniyor (${other} tanesi başka istasyonda)`
}

/**
 * Whether the settled money covers the check. Deliberately ignores pending and
 * failed money — this mirrors the backend's TotalPaidForCheck exactly.
 *
 * `remoteSettled` is threaded in for that same mirroring reason, and it is
 * load-bearing rather than cosmetic: the backend's TotalPaidForCheck ALREADY
 * counts a payment completed at another till, so leaving it out here would put
 * this client strictly further from the server's own guard, not closer.
 *
 * Concretely, omitting it strands the cashier. Receipt.tsx only renders the
 * close button when isFullyPaid, and only offers "Nakit al" while
 * remaining > 0. A check paid in full at the till next door would then show
 * remaining = 0 (correct, once completed money is subtracted) AND
 * isFullyPaid = false — no way to pay, no way to close, on a check the backend
 * would happily accept. The two numbers must be fed from the same settled sum.
 */
export function isFullyPaid(
  checkTotal: number,
  serverCompleted: ReadonlyMap<string, number>,
  tracked: readonly TrackedPayment[],
  remoteSettled: readonly RemoteSettledFiscal[] = [],
): boolean {
  return checkTotal > 0 && settledTotal(serverCompleted, tracked, remoteSettled) >= checkTotal
}

/**
 * Requirement 6 — anti-flicker. The mock adapter settles in well under a
 * second, and a badge that flashes amber for 80ms before turning green reads as
 * a glitch in a cash-heavy flow the cashier runs hundreds of times a shift.
 *
 * A pending payment younger than this threshold renders NOTHING (not a badge,
 * not a placeholder — the row simply has no badge yet), so a fast completion
 * goes straight to green with no intermediate state. This is a delayed render,
 * NOT an optimistic one: nothing is ever shown as "kesildi" before the server
 * says so.
 */
export const PENDING_BADGE_DELAY_MS = 300

export function shouldRenderPendingBadge(payment: TrackedPayment, nowMs: number): boolean {
  if (payment.status !== 'pending') return true
  return nowMs - payment.registeredAtMs >= PENDING_BADGE_DELAY_MS
}

/**
 * Cash to print as "ALINAN" on the receipt: the sum of what the customer handed
 * over across every installment whose fiscal record actually settled.
 *
 * A `failed`/`voided` payment is excluded — no receipt was cut for it, and its
 * cash was handed back (or re-taken by the retry, which registers a fresh
 * payment of its own). Including it would overstate the change due.
 *
 * KNOWN LIMITATION (pre-existing, unchanged): payments made in an EARLIER app
 * session are not tracked here, so a reprint after a mid-split restart still
 * shows only what this session collected.
 */
export function receivedTotalForPrint(tracked: readonly TrackedPayment[]): number {
  return tracked.reduce((sum, p) => (countsAsSettled(p.status) ? sum + p.receivedAmount : sum), 0)
}

/**
 * Check ids with at least one payment awaiting a fiscal record — drives the
 * amber dot on the masa planı / adisyon listesi (requirement 5).
 *
 * Now branch-wide: the union of this station's own pending payments and every
 * OTHER station's, deduped by payment id. This is what makes the dot mean
 * "bu adisyonda mali kayıt bekleniyor" rather than the far weaker "bu
 * istasyonun aldığı bir ödemede mali kayıt bekleniyor" — a cashier looking at
 * the masa planı has no way to know which till took the money.
 */
export function checkIdsAwaitingFiscal(
  tracked: readonly TrackedPayment[],
  remote: readonly RemotePendingFiscal[] = [],
): Set<string> {
  const ids = new Set(pendingPayments(tracked).map((p) => p.checkId))
  for (const r of remotePendingOnly(remote, tracked)) {
    // A payment not bound to a check arrives with an empty checkId (the
    // backend's check_id is nullable — see apiclient's PendingFiscalItem).
    // Adding "" would put a meaningless member in a set that is only ever
    // queried by real check id.
    if (r.checkId) ids.add(r.checkId)
  }
  return ids
}

/**
 * Failed fiscal registrations the cashier is not already being shown.
 *
 * SUPPRESSION RULE — only a payment ALREADY TRACKED AS `failed` is excluded,
 * not every tracked payment. That distinction is the difference between this
 * feature working and being useless for the role it targets:
 *
 *   manager session — GetPayment succeeds, the payment resolves to `failed`,
 *     and FiscalStatusBadge already reports it with a retry affordance.
 *     Banner-ing it too would double-report one failure. Suppressed.
 *   cashier session — GetPayment 403s (payment.payment.read is manager-only),
 *     so the payment collapses to `unknown`, which countsAsSettled treats as
 *     PAID and whose badge only says "durum okunamıyor". If a tracked
 *     `unknown` were suppressed here, a cashier who took ₺100 for a receipt
 *     that never got cut would be told NOTHING — and would then hit a
 *     misleading `insufficient_payment` at close time. Surfaced.
 *
 * The branch feed is the only channel that reaches a cashier at all
 * (payment.fiscal_status.read is granted to them), which is exactly why it
 * must not filter itself out on a status the cashier could never have read.
 *
 * `voided`/`completed` are excluded because neither needs cashier action: a
 * completed receipt is the happy path, and a void was somebody's deliberate
 * decision.
 */
export function unreportedRemoteFailures(
  settled: readonly RemoteSettledFiscal[],
  tracked: readonly TrackedPayment[],
): RemoteSettledFiscal[] {
  const alreadyShownAsFailed = new Set(tracked.filter((p) => p.status === 'failed').map((p) => p.id))
  return settled.filter((s) => s.status === 'failed' && !alreadyShownAsFailed.has(s.paymentId))
}

/**
 * The Turkish message shown for a remote failed fiscal registration. Mirrors
 * useFiscalStatusPolling's own failure wording so a cashier reads the same
 * sentence whichever station took the money; the raw `failureReason` stays
 * available as drill-down detail (a `title` attribute), never as the primary
 * text — it is untranslated device/adapter output.
 */
export function describeRemoteFailure(): string {
  return 'Mali kayıt cihaz tarafından tamamlanamadı. Fiş kesilmedi — ödeme yeniden alınmalı.'
}

/** Narrows an arbitrary backend status string. An unrecognized status must not
 * silently become `completed` — it stays `pending` so the UI keeps polling and
 * keeps the close blocked, which fails safe (a stuck badge) rather than
 * dangerous (a check closed against an unregistered sale). */
export function parseStatus(raw: string): FiscalStatus {
  switch (raw) {
    case 'completed':
    case 'failed':
    case 'voided':
    case 'pending':
      return raw
    default:
      return 'pending'
  }
}
