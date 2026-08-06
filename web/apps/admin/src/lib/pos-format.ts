// POS (checks/tables) display formatting helpers — money and elapsed-time
// text shared by the checks list and tables cards.

import { formatKurus } from "@/lib/money"

// formatCheckTotal renders an amount in kuruş (backend's int64 `total`) as a
// Turkish lira string, e.g. 12345 -> "₺123,45". `total` is optional because
// checkResponse.Total is only populated by the list/get endpoints
// (omitempty on open/close/cancel) — callers must handle "no total yet".
export function formatCheckTotal(totalKurus: number | null | undefined): string {
  return formatKurus(totalKurus)
}

const MINUTE_MS = 60_000
const HOUR_MS = 60 * MINUTE_MS

const DAY_MS = 24 * HOUR_MS

// formatOpenDuration renders how long a check has been open as a short
// Turkish label ("az önce", "12 dk", "1s 05dk", "3s+"), computed from
// `openedAt` relative to `now` (frontend-computed — the backend never sends
// an elapsed duration, only `opened_at`; see toCheckResponse).
//
// This is a RELATIVE-TIME formatter for a still-running check: "az önce" and
// the "3s+" cap are deliberate ("it is still open and it has been too long"),
// which is why a finished check must NOT use it — a check opened and closed
// in 14 seconds a month ago is not "az önce". Closed rows use
// formatCheckDuration below.
//
// `now` defaults to `new Date()` but accepts an explicit value so callers
// (and tests) can pin the clock instead of depending on wall time.
export function formatOpenDuration(openedAt: string, now: Date = new Date()): string {
  const openedMs = new Date(openedAt).getTime()
  if (Number.isNaN(openedMs)) return "—"

  const elapsedMs = now.getTime() - openedMs
  if (elapsedMs < MINUTE_MS) return "az önce"

  const totalMinutes = Math.floor(elapsedMs / MINUTE_MS)
  if (totalMinutes < 60) return `${totalMinutes} dk`

  const hours = Math.floor(totalMinutes / 60)
  const minutes = totalMinutes % 60
  if (hours >= 3) return "3s+"

  return `${hours}s ${String(minutes).padStart(2, "0")}dk`
}

// formatCheckDuration renders the span a FINISHED check stayed open —
// opened_at -> closed_at — as a short Turkish label: "14 sn", "12 dk",
// "1s 05dk", "31g 0s". It is a DURATION, not a relative time, which is the
// whole reason it exists next to formatOpenDuration: a QR check can be
// opened, served and closed inside a minute, and "az önce" would both
// misreport that span and keep implying "just now" a month later.
//
// There is no "3s+" style cap here: an already-closed check cannot be
// rescued by an alarm, so the text stays literally true. It does switch to
// days past 24 hours, because a check that stayed open across a weekend
// reads as "62s 30dk" otherwise — correct and unreadable.
//
// `to` is the end of the span (closed_at). A `to` before `from` (clock skew
// between server and browser) clamps to zero rather than rendering a
// negative duration.
export function formatCheckDuration(from: string, to: Date = new Date()): string {
  const fromMs = new Date(from).getTime()
  if (Number.isNaN(fromMs)) return "—"

  const elapsedMs = Math.max(0, to.getTime() - fromMs)
  if (elapsedMs < MINUTE_MS) return `${Math.floor(elapsedMs / 1000)} sn`

  const totalMinutes = Math.floor(elapsedMs / MINUTE_MS)
  if (totalMinutes < 60) return `${totalMinutes} dk`

  if (elapsedMs >= DAY_MS) {
    const days = Math.floor(elapsedMs / DAY_MS)
    const hours = Math.floor((elapsedMs % DAY_MS) / HOUR_MS)
    return `${days}g ${hours}s`
  }

  const hours = Math.floor(totalMinutes / 60)
  const minutes = totalMinutes % 60
  return `${hours}s ${String(minutes).padStart(2, "0")}dk`
}

// isLongOpenCheck flags checks open longer than the given threshold (default
// 2 hours) so the UI can call attention to a table that may have been
// forgotten. Only meaningful for open checks — callers should not apply it
// to closed/cancelled ones.
export function isLongOpenCheck(openedAt: string, now: Date = new Date(), thresholdMs = 2 * HOUR_MS): boolean {
  const openedMs = new Date(openedAt).getTime()
  if (Number.isNaN(openedMs)) return false
  return now.getTime() - openedMs >= thresholdMs
}
