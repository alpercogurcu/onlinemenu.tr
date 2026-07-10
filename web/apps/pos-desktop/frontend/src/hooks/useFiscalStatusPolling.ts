import { useCallback, useEffect, useRef } from 'react'
import { GetPayment } from '../../wailsjs/go/main/App'
import { describeError, isForbiddenError } from '../lib/errors'
import { isTerminal, parseStatus, type FiscalStatus, type TrackedPayment } from '../lib/fiscalStatus'

export const POLL_INTERVAL_MS = 2_000

export type StatusResolution = {
  id: string
  status: FiscalStatus
  failureReason?: string
}

/**
 * Polls GET /api/v1/payments/{id} (through the Wails binding) for every payment
 * still awaiting its fiscal record, until each reaches a terminal status.
 *
 * NOT TanStack Query, deliberately: this app has no HTTP-from-JS data layer at
 * all — every read goes through a Go binding on `window.go` — so there is no
 * fetch for a query client to own, no cache key space, and no existing
 * QueryClientProvider. Adding one for a single 2s poll would be the largest
 * dependency in the app. This mirrors the setInterval + cleanup pattern already
 * used for the 30s masa planı refresh in App.tsx.
 *
 * Lifecycle guarantees (requirement 2):
 *  - stops as soon as no payment is pending (the interval is never even created)
 *  - stops on unmount / when the check is deselected (cleanup clears it)
 *  - refetches immediately when the window or tab regains focus, so a station
 *    left in the background does not show a minutes-stale badge
 *
 * A single in-flight guard (`pollingRef`) prevents overlapping ticks when the
 * backend is slower than the interval.
 */
export function useFiscalStatusPolling(
  tracked: readonly TrackedPayment[],
  onResolve: (resolutions: StatusResolution[]) => void,
) {
  const pollingRef = useRef(false)

  // Kept in a ref so the poll callback never goes stale without making the
  // interval effect depend on an array identity that changes every render.
  const trackedRef = useRef(tracked)
  trackedRef.current = tracked
  const onResolveRef = useRef(onResolve)
  onResolveRef.current = onResolve

  const pendingIds = tracked
    .filter((p) => p.status === 'pending')
    .map((p) => p.id)
    .join(',')

  const poll = useCallback(async () => {
    if (pollingRef.current) return
    const ids = trackedRef.current.filter((p) => p.status === 'pending').map((p) => p.id)
    if (ids.length === 0) return

    pollingRef.current = true
    try {
      const settled = await Promise.all(
        ids.map(async (id): Promise<StatusResolution | null> => {
          try {
            const payment = await GetPayment(id)
            const status = parseStatus(payment.status)
            if (!isTerminal(status)) return null
            return {
              id,
              status,
              // The backend's paymentResponse carries no failure_reason field
              // (nor does domain.Payment) — see the report. Until it does, a
              // failed fiscal registration can only be described generically.
              failureReason:
                status === 'failed'
                  ? 'Mali kayıt cihaz tarafından tamamlanamadı. Fiş kesilmedi — ödeme yeniden alınmalı.'
                  : undefined,
            }
          } catch (err) {
            // 403 is a property of the session's ROLE, not a transient fault:
            // this station will never be allowed to read payment status, so
            // collapse to `unknown` (terminal) instead of hammering the authz
            // engine every 2s for the life of the check.
            if (isForbiddenError(err)) return { id, status: 'unknown' }
            // Any other failure (network blip, 500) is transient — leave the
            // payment pending so the next tick retries it.
            console.warn('GetPayment failed — fiscal status still pending', describeError(err))
            return null
          }
        }),
      )

      const resolutions = settled.filter((r): r is StatusResolution => r !== null)
      if (resolutions.length > 0) onResolveRef.current(resolutions)
    } finally {
      pollingRef.current = false
    }
  }, [])

  // Interval is (re)created only when the SET of pending ids changes — not on
  // every parent render. When nothing is pending, no timer exists at all.
  useEffect(() => {
    if (pendingIds === '') return
    void poll()
    const id = setInterval(() => void poll(), POLL_INTERVAL_MS)
    return () => clearInterval(id)
  }, [pendingIds, poll])

  // Requirement 2: "sekme/ekran geri gelince taze çek". Wails windows emit both
  // of these; `visibilitychange` alone misses an OS-level window refocus that
  // never hid the document.
  useEffect(() => {
    if (pendingIds === '') return
    const onFocus = () => void poll()
    const onVisibility = () => {
      if (document.visibilityState === 'visible') void poll()
    }
    window.addEventListener('focus', onFocus)
    document.addEventListener('visibilitychange', onVisibility)
    return () => {
      window.removeEventListener('focus', onFocus)
      document.removeEventListener('visibilitychange', onVisibility)
    }
  }, [pendingIds, poll])
}
