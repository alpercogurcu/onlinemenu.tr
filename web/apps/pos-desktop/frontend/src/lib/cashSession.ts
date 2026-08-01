// ADR-DATA-008: kasa oturumu (cash session) — the money arithmetic and state
// derivation for açılış/durum/sayım/kapanış, kept here (not in components)
// for the same reason as lib/payment.ts and lib/fiscalStatus.ts: this is
// where a bug would actually live, and it is testable without the generated
// (gitignored) wailsjs bindings — see lib/branchFiscal.ts's file-level
// comment for why these wire types are hand-declared rather than imported
// from '../../wailsjs/go/models'.
//
// All amounts are integers in kuruş (see format.ts).

/** One line of a kupur dokumu (denomination breakdown) as the UI edits it. */
export type DenominationRow = {
  denominationMinor: number
  count: number
}

/**
 * The fixed Turkish banknote/coin list (ADR-DATA-008: "the POS client is
 * responsible for offering the fixed set of banknotes/coins" — the backend
 * deliberately does not model this as a tenant-configurable table). Hard-coded
 * on purpose, descending by value so the counting form reads the way a
 * cashier physically sorts a drawer.
 */
export const TURKISH_DENOMINATIONS: ReadonlyArray<{ denominationMinor: number; label: string }> = [
  { denominationMinor: 20000, label: '200 ₺' },
  { denominationMinor: 10000, label: '100 ₺' },
  { denominationMinor: 5000, label: '50 ₺' },
  { denominationMinor: 2000, label: '20 ₺' },
  { denominationMinor: 1000, label: '10 ₺' },
  { denominationMinor: 500, label: '5 ₺' },
  { denominationMinor: 100, label: '1 ₺' },
  { denominationMinor: 50, label: '50 kr' },
  { denominationMinor: 25, label: '25 kr' },
  { denominationMinor: 10, label: '10 kr' },
  { denominationMinor: 5, label: '5 kr' },
]

/** Builds the initial (all-zero) count rows for the fixed denomination list —
 * the Sayım form's starting state. */
export function emptyDenominationRows(): DenominationRow[] {
  return TURKISH_DENOMINATIONS.map((d) => ({ denominationMinor: d.denominationMinor, count: 0 }))
}

/**
 * The counted total, derived from denomination rows rather than typed
 * separately. This is the whole reason ErrDenominationSumMismatch
 * (payment/domain.ValidateDenominations) is unreachable from this client by
 * construction: `closing_counted_amount` sent to SubmitClosingCount is always
 * exactly this sum, never an independently-entered figure that could drift
 * from it.
 */
export function denominationTotal(rows: readonly DenominationRow[]): number {
  return rows.reduce((sum, r) => sum + r.denominationMinor * Math.max(0, r.count), 0)
}

/** Only non-zero rows are meaningful to submit — trims the fixed list down to
 * what the cashier actually counted, matching payment/domain.DenominationCount's
 * "empty/nil breakdown is valid, but a supplied one must reconcile" contract
 * (an all-zero row for a banknote nobody has in the drawer is noise, not data). */
export function nonZeroDenominationRows(rows: readonly DenominationRow[]): DenominationRow[] {
  return rows.filter((r) => r.count > 0)
}

/** Sayım/Kapanış difference: counted minus expected. Positive = fazla (kasa
 * fazlası), negative = açık (kasa açığı). Pure so the Sayım screen can preview
 * it live as the cashier counts, before anything is submitted. */
export function computeDifference(countedAmount: number, expectedClose: number): number {
  return countedAmount - expectedClose
}

/**
 * A frozen snapshot of the figures a closing count was counted against —
 * exactly what SubmitClosingCount's own response returned, or what a later
 * GetActiveCashSession poll currently reports. The Kapanış screen renders
 * ONLY the snapshot captured at submission time, never a live re-read (see
 * isClosingCountStale) — the live read exists solely to detect drift, not to
 * feed the displayed numbers.
 */
export type ClosingSnapshot = {
  sessionId: string
  expectedClose: number
  closingCountedAmount: number
  /** null when the session has no closing_submitted_at yet (should not occur
   * once toClosingSnapshot has already gated on status === 'closing_control',
   * but kept nullable to mirror the wire shape exactly rather than assert). */
  closingSubmittedAt: string | null
}

/** Narrow wire shape this module needs from a CashSessionDTO-like value —
 * see this file's header comment for why it is hand-declared. */
export type CashSessionSnapshotSource = {
  id: string
  status: string
  expected_close: number
  closing_counted_amount?: number | null
  closing_submitted_at?: string | null
}

/**
 * Extracts a ClosingSnapshot from a session view, or null when the session
 * has not reached closing_control (nothing has been counted against yet —
 * there is nothing to freeze). Used for BOTH the frozen snapshot (captured
 * once, from SubmitClosingCount's response) and the live comparison value
 * (recomputed on every GetActiveCashSession poll) — same extraction, two
 * different call sites, so isClosingCountStale is comparing like with like.
 */
export function toClosingSnapshot(session: CashSessionSnapshotSource | null | undefined): ClosingSnapshot | null {
  if (!session) return null
  if (session.status !== 'closing_control') return null
  if (session.closing_counted_amount === undefined || session.closing_counted_amount === null) return null
  return {
    sessionId: session.id,
    expectedClose: session.expected_close,
    closingCountedAmount: session.closing_counted_amount,
    closingSubmittedAt: session.closing_submitted_at ?? null,
  }
}

/**
 * Whether a frozen closing-count snapshot is stale, i.e. no longer describes
 * reality. ADR-DATA-008: expected_close is computed fresh on EVERY read, never
 * stored — so a pending fiscal submission settling (or a second station
 * submitting a recount on the same branch-scoped session) between the count
 * being submitted and the cashier hitting "Kapat" silently changes the
 * backend's own numbers out from under a count already on screen.
 *
 * Deliberately compares every field of the snapshot object, not just
 * expectedClose: a same-branch recount (another station, or this one, calling
 * SubmitClosingCount again) can change closing_counted_amount/
 * closing_submitted_at while expected_close itself sits still — an
 * expectedClose-only compare would miss that the number on screen is no
 * longer THIS count.
 *
 * `live === null` (no snapshot extractable right now — the session left
 * closing_control, or the branch has no active session at all) is treated as
 * stale: the frozen figures no longer describe any real, comparable state.
 */
export function isClosingCountStale(snapshot: ClosingSnapshot, live: ClosingSnapshot | null): boolean {
  if (!live) return true
  return (
    live.sessionId !== snapshot.sessionId ||
    live.expectedClose !== snapshot.expectedClose ||
    live.closingCountedAmount !== snapshot.closingCountedAmount ||
    live.closingSubmittedAt !== snapshot.closingSubmittedAt
  )
}

/** Turkish label for a movement direction. */
export function movementDirectionLabel(direction: 'in' | 'out'): string {
  return direction === 'in' ? 'Giriş' : 'Çıkış'
}

/** Client-side gate mirroring the backend's own validation (payment/service.
 * CashSessionService.RecordMovement) — kept here so the submit button can be
 * disabled instead of round-tripping to a 500 (see errors.ts's note on the
 * backend not mapping this validation error to 422). */
export function isValidMovementAmount(amountKurus: number): boolean {
  return Number.isFinite(amountKurus) && amountKurus > 0
}

export function isValidMovementReason(reason: string): boolean {
  return reason.trim().length > 0
}

/** Mirrors CashSessionService.Open's own non-negative check. */
export function isValidOpeningAmount(amountKurus: number): boolean {
  return Number.isFinite(amountKurus) && amountKurus >= 0
}
