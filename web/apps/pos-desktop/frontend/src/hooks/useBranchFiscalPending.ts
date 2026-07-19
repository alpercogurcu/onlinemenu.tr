import { useEffect, useState } from 'react'
import { EventsOn } from '../../wailsjs/runtime/runtime'
import {
  EMPTY_BRANCH_FISCAL,
  parseBranchFiscalEvent,
  type BranchFiscalPending,
  type BranchFiscalPendingEvent,
} from '../lib/branchFiscal'

/**
 * Branch-wide pending-fiscal visibility (istemci tarafı).
 *
 * PUSH, not poll: the polling itself lives in Go (see fiscal_poller.go), which
 * owns the HTTP client, the token, the 3s/15s cadence, the backoff and the
 * 403 stop rule. This hook only subscribes to the resulting
 * `fiscal:branch-pending` event — mirroring how `hardware:printer` is consumed
 * in App.tsx.
 *
 * That split is deliberate and matches the app's architecture: the frontend
 * never performs HTTP and never sees the session token (see
 * internal/apiclient's package doc). It is also why there is no "refetch on
 * focus" here — the Go poller runs regardless of webview focus, so a snapshot
 * is never more than one interval stale.
 *
 * SNAPSHOT SEMANTICS: each event REPLACES the previous state wholesale rather
 * than merging into it. The Go side emits on every successful poll including
 * empty ones, precisely so that a payment which settled elsewhere disappears
 * from this state and its amber dot clears. Merging would make dots sticky
 * forever.
 *
 * A station whose role lacks payment.fiscal_status.read simply never receives
 * an event (the Go poller logs one warning and stops) — the state stays empty
 * and every branch-wide indicator degrades to this station's own tracked
 * payments, exactly the pre-feature behavior. It must never block the cashier.
 *
 * The wire parsing lives in lib/branchFiscal.ts, not here: this module imports
 * the GENERATED (and gitignored) wailsjs runtime, so anything importable by a
 * test has to stay out of it.
 */

/**
 * `resetKey` identifies the session+branch these snapshots belong to (see
 * App.tsx's sessionFiscalKey). When it changes the state is dropped to empty
 * SYNCHRONOUSLY, during render — not in an effect.
 *
 * That matters: this state feeds the failed-fiscal banner and the money
 * arithmetic. Resetting in an effect would leave one committed render in which
 * the PREVIOUS session's (or previous branch's) pending payments still reserve
 * money and its failed banner is still on screen — a cashier logging in after a
 * colleague would briefly see the colleague's branch. This is React's
 * documented "adjust state when a prop changes" pattern, chosen for exactly
 * that no-stale-frame guarantee.
 */
export function useBranchFiscalPending(resetKey: string): BranchFiscalPending {
  const [state, setState] = useState<BranchFiscalPending>(EMPTY_BRANCH_FISCAL)
  const [appliedKey, setAppliedKey] = useState(resetKey)

  if (appliedKey !== resetKey) {
    setAppliedKey(resetKey)
    setState(EMPTY_BRANCH_FISCAL)
  }

  useEffect(() => {
    const unsubscribe = EventsOn('fiscal:branch-pending', (evt: BranchFiscalPendingEvent) => {
      setState(parseBranchFiscalEvent(evt))
    })
    return () => unsubscribe()
  }, [])

  return state
}
