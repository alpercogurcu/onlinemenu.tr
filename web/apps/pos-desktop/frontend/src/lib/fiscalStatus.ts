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
): number {
  let total = 0
  for (const amount of serverCompleted.values()) total += amount
  for (const p of tracked) {
    if (countsAsSettled(p.status) && !serverCompleted.has(p.id)) total += p.amountTotal
  }
  return total
}

/** Money committed to an in-flight fiscal registration: not yet paid, but not
 * collectable again either. */
export function reservedTotal(tracked: readonly TrackedPayment[]): number {
  return tracked.reduce((sum, p) => (p.status === 'pending' ? sum + p.amountTotal : sum), 0)
}

/**
 * What the customer still owes and the cashier may still collect. Pending
 * payments hold their amount back so a split payment mid-fiscal-registration
 * cannot be double-collected; a payment that ends up `failed`/`voided` releases
 * its amount straight back into this number (it simply stops being reserved).
 */
export function collectableRemaining(
  checkTotal: number,
  serverCompleted: ReadonlyMap<string, number>,
  tracked: readonly TrackedPayment[],
): number {
  // remainingBalance keeps the never-negative rule in one place (an overpaid
  // check must not show a negative "kalan").
  return remainingBalance(checkTotal, settledTotal(serverCompleted, tracked) + reservedTotal(tracked))
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
export function closeBlockReason(tracked: readonly TrackedPayment[]): string | null {
  const count = pendingPayments(tracked).length
  if (count === 0) return null
  return `${count} ödemenin mali kaydı bekleniyor`
}

/** Whether the settled money covers the check. Deliberately ignores pending and
 * failed money — this mirrors the backend's TotalPaidForCheck exactly. */
export function isFullyPaid(
  checkTotal: number,
  serverCompleted: ReadonlyMap<string, number>,
  tracked: readonly TrackedPayment[],
): boolean {
  return checkTotal > 0 && settledTotal(serverCompleted, tracked) >= checkTotal
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

/** Check ids with at least one payment awaiting a fiscal record — drives the
 * amber dot on the masa planı / adisyon listesi (requirement 5). */
export function checkIdsAwaitingFiscal(tracked: readonly TrackedPayment[]): Set<string> {
  return new Set(pendingPayments(tracked).map((p) => p.checkId))
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
