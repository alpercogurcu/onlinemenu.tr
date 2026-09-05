import { useCallback, useEffect, useState } from 'react'
import {
  CloseCashSession,
  GetActiveCashSession,
  ListCashMovements,
  OpenCashSession,
  RecordCashMovement,
  SubmitClosingCount,
} from '../../wailsjs/go/main/App'
import type { main } from '../../wailsjs/go/models'
import { describeError } from '../lib/errors'
import { isClosingCountStale, toClosingSnapshot, type ClosingSnapshot, type DenominationRow } from '../lib/cashSession'

/** How often the Kapanış (closing_control) screen re-reads the branch's cash
 * session while it is on screen, to catch expected_close/closing figures
 * moving out from under an already-submitted count (ADR-DATA-008: these are
 * computed fresh on every read, never stored — see cashSession.ts's
 * isClosingCountStale). Not run for any other status: nothing is "frozen"
 * yet outside closing_control, so there is nothing to detect drift against. */
const CLOSING_CONTROL_POLL_MS = 5000

export type UseCashSessionResult = {
  /** True only for the initial fetch and explicit refreshes — mutations set
   * their own submitting flags on the caller side (form-local), not this. */
  loading: boolean
  /** True once the initial GetActiveCashSession call has resolved at least
   * once, so callers can distinguish "still checking" from "confirmed no
   * session" — both render nothing in `session`. */
  checked: boolean
  session: main.CashSessionDTO | null
  /** Generic failure message (open/movement/submit/close-transport errors),
   * already Turkish via describeError. Distinct from cannotCloseReasons,
   * which is a structured refusal, not a failure. */
  error: string
  /** The figures the last submitted closing count was counted against —
   * frozen at submission time, from SubmitClosingCount's own response. The
   * Kapanış screen must render ONLY this, never `session`'s live figures
   * directly (see `stale`). */
  closingSnapshot: ClosingSnapshot | null
  /** True once the live session's figures have drifted from closingSnapshot
   * — see lib/cashSession.ts's isClosingCountStale for what "drifted" means. */
  stale: boolean
  /** Verbatim blocking reasons from the backend's cannotClose guard
   * (ADR-DATA-008), set when closeSession() resolves with cannot_close=true.
   * Cleared by dismissCannotClose or by a subsequent successful close. */
  cannotCloseReasons: string[] | null
  /** The full hareket defteri (movement ledger) for `session`, oldest first —
   * only populated on demand via loadMovements (the ledger view), never
   * fetched implicitly alongside session refreshes, since most cashier
   * interactions never open it. */
  movements: main.CashMovementDTO[]
  refresh: () => Promise<main.CashSessionDTO | null>
  loadMovements: () => Promise<void>
  openSession: (openingCountedAmount: number, openingNotes: string) => Promise<boolean>
  recordMovement: (direction: 'in' | 'out', amountMinor: number, reason: string) => Promise<boolean>
  submitClosingCount: (rows: readonly DenominationRow[], notes: string) => Promise<boolean>
  closeSession: () => Promise<boolean>
  dismissCannotClose: () => void
  /** Explicit reset for handleLogout — belt and braces alongside the
   * automatic branchId-keyed reset below (a logout that leaves branchId
   * unchanged, e.g. a chain-wide staff session with no branch at all, would
   * otherwise not trigger it). */
  reset: () => void
}

/**
 * Owns the whole ADR-DATA-008 cash-session lifecycle for one branch: fetching
 * the active session, opening, recording movements, submitting a closing
 * count, closing, and detecting a stale closing count. Kept as a hook
 * (mirroring useBranchFiscalPending/useFiscalStatusPolling) rather than
 * inline in App.tsx so the polling/staleness orchestration has one owner and
 * App.tsx stays wiring, not logic.
 *
 * branchId should come from the current session (SessionDTO.branch_id).
 * Undefined/empty (chain-wide staff session, or logged out) means there is no
 * branch to ask about — every action becomes a no-op and `session` stays
 * null, mirroring ListTables/OpenCheck's same requirement elsewhere in this
 * app.
 */
export function useCashSession(branchId: string | undefined): UseCashSessionResult {
  const [session, setSession] = useState<main.CashSessionDTO | null>(null)
  const [checked, setChecked] = useState(false)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [closingSnapshot, setClosingSnapshot] = useState<ClosingSnapshot | null>(null)
  const [cannotCloseReasons, setCannotCloseReasons] = useState<string[] | null>(null)
  const [movements, setMovements] = useState<main.CashMovementDTO[]>([])

  const reset = useCallback(() => {
    setSession(null)
    setChecked(false)
    setError('')
    setClosingSnapshot(null)
    setCannotCloseReasons(null)
    setMovements([])
  }, [])

  // Render-time reset on branch change — same pattern and same rationale as
  // useBranchFiscalPending's resetKey handling: a branch switch (or logout,
  // which drops branchId to undefined) must drop the PREVIOUS branch's
  // session/snapshot synchronously, during this render, not one frame later
  // in an effect. Otherwise one committed render would show the previous
  // branch's cash session (or a frozen count counted against ITS numbers)
  // layered onto the new branch's screen.
  const [appliedBranchId, setAppliedBranchId] = useState(branchId)
  if (appliedBranchId !== branchId) {
    setAppliedBranchId(branchId)
    setSession(null)
    setChecked(false)
    setError('')
    setClosingSnapshot(null)
    setCannotCloseReasons(null)
    setMovements([])
  }

  // refresh returns the freshly-fetched session (or null) directly, rather
  // than relying on callers reading `session` state right after awaiting
  // this — a state update from setSession here is not visible through the
  // closure of an async function that called refresh() a moment earlier
  // (see closeSession, which depends on this for its pre-close staleness
  // re-check).
  const refresh = useCallback(async (): Promise<main.CashSessionDTO | null> => {
    if (!branchId) {
      setSession(null)
      setChecked(true)
      return null
    }
    setLoading(true)
    try {
      const result = await GetActiveCashSession(branchId)
      const next = result.has_active_session ? (result.session ?? null) : null
      setSession(next)
      setError('')
      return next
    } catch (err) {
      setError(describeError(err))
      return null
    } finally {
      setChecked(true)
      setLoading(false)
    }
  }, [branchId])

  useEffect(() => {
    void refresh()
  }, [refresh])

  // loadMovements is deliberately NOT called from refresh/the effects above:
  // most status-screen visits never open the ledger, so fetching it on every
  // session poll would be pure waste. The ledger view (CashSessionModal)
  // calls this itself when the cashier navigates to it.
  const loadMovements = useCallback(async (): Promise<void> => {
    if (!session) {
      setMovements([])
      return
    }
    try {
      const list = await ListCashMovements(session.id)
      setMovements(list)
    } catch (err) {
      setError(describeError(err))
    }
  }, [session])

  // Staleness poll — ONLY while closing_control (see CLOSING_CONTROL_POLL_MS).
  // Keyed on id+status rather than the whole `session` object so a poll tick
  // that only moves movements_net/expected_close does not restart the timer.
  useEffect(() => {
    if (!session || session.status !== 'closing_control') return
    const id = setInterval(() => {
      void refresh()
    }, CLOSING_CONTROL_POLL_MS)
    return () => clearInterval(id)
  }, [session?.id, session?.status, refresh])

  const stale = closingSnapshot !== null && isClosingCountStale(closingSnapshot, toClosingSnapshot(session))

  const openSession = useCallback(
    async (openingCountedAmount: number, openingNotes: string): Promise<boolean> => {
      if (!branchId) return false
      setError('')
      try {
        const dto = await OpenCashSession(branchId, openingCountedAmount, openingNotes)
        setSession(dto)
        setClosingSnapshot(null)
        setCannotCloseReasons(null)
        return true
      } catch (err) {
        setError(describeError(err))
        return false
      }
    },
    [branchId],
  )

  const recordMovement = useCallback(
    async (direction: 'in' | 'out', amountMinor: number, reason: string): Promise<boolean> => {
      if (!session) return false
      setError('')
      try {
        await RecordCashMovement(session.id, direction, amountMinor, reason)
        // The movement response carries no session-level figures (just the
        // movement itself) — re-read so Durum's movements_net/expected_close
        // reflect it immediately rather than waiting for the next unrelated
        // refresh.
        await refresh()
        return true
      } catch (err) {
        setError(describeError(err))
        return false
      }
    },
    [session, refresh],
  )

  const submitClosingCount = useCallback(
    async (rows: readonly DenominationRow[], notes: string): Promise<boolean> => {
      if (!session) return false
      setError('')
      const nonZero = rows.filter((r) => r.count > 0)
      const total = nonZero.reduce((sum, r) => sum + r.denominationMinor * r.count, 0)
      try {
        const dto = await SubmitClosingCount(
          session.id,
          total,
          nonZero.map((r) => ({ denomination_minor: r.denominationMinor, count: r.count })),
          notes,
        )
        setSession(dto)
        // Freeze the figures THIS submission was counted against — the whole
        // point of closingSnapshot (see its doc comment on the exported type).
        setClosingSnapshot(toClosingSnapshot(dto))
        setCannotCloseReasons(null)
        return true
      } catch (err) {
        setError(describeError(err))
        return false
      }
    },
    [session],
  )

  const closeSession = useCallback(async (): Promise<boolean> => {
    if (!session || !closingSnapshot) return false
    setError('')

    // Defensive re-check immediately before the irreversible call: a poll
    // tick may not have landed yet between the last render and this press,
    // so re-read once more and compare against a truly current live value
    // rather than trusting `stale` as computed at the last render.
    const fresh = await refresh()
    const freshSnapshot = toClosingSnapshot(fresh)
    if (isClosingCountStale(closingSnapshot, freshSnapshot)) {
      setError('Sayım bayatladı — kasa bakiyesi değişti. Kasayı yeniden sayın.')
      return false
    }

    try {
      const result = await CloseCashSession(session.id)
      if (result.cannot_close) {
        setCannotCloseReasons(result.reasons.length > 0 ? result.reasons : ['Kasa şu anda kapatılamıyor.'])
        return false
      }
      setCannotCloseReasons(null)
      setClosingSnapshot(null)
      setSession(null)
      return true
    } catch (err) {
      setError(describeError(err))
      return false
    }
  }, [session, closingSnapshot, refresh])

  const dismissCannotClose = useCallback(() => setCannotCloseReasons(null), [])

  return {
    loading,
    checked,
    session,
    error,
    closingSnapshot,
    stale,
    cannotCloseReasons,
    movements,
    refresh,
    loadMovements,
    openSession,
    recordMovement,
    submitClosingCount,
    closeSession,
    dismissCannotClose,
    reset,
  }
}
