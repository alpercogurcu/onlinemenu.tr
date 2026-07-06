// POS (checks/tables) display formatting helpers — money and elapsed-time
// text shared by the checks list and tables cards.

const tryLiraFormatter = new Intl.NumberFormat("tr-TR", {
  style: "currency",
  currency: "TRY",
})

// formatCheckTotal renders an amount in kuruş (backend's int64 `total`) as a
// Turkish lira string, e.g. 12345 -> "123,45 ₺". `total` is optional because
// checkResponse.Total is only populated by the list/get endpoints
// (omitempty on open/close/cancel) — callers must handle "no total yet".
export function formatCheckTotal(totalKurus: number | null | undefined): string {
  if (totalKurus == null) return "—"
  return tryLiraFormatter.format(totalKurus / 100)
}

const MINUTE_MS = 60_000
const HOUR_MS = 60 * MINUTE_MS

// formatOpenDuration renders how long a check has been open as a short
// Turkish label ("az önce", "12 dk", "1s 05dk", "3s+"), computed from
// `openedAt` relative to `now` (frontend-computed — the backend never sends
// an elapsed duration, only `opened_at`; see toCheckResponse).
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

// isLongOpenCheck flags checks open longer than the given threshold (default
// 2 hours) so the UI can call attention to a table that may have been
// forgotten. Only meaningful for open checks — callers should not apply it
// to closed/cancelled ones.
export function isLongOpenCheck(openedAt: string, now: Date = new Date(), thresholdMs = 2 * HOUR_MS): boolean {
  const openedMs = new Date(openedAt).getTime()
  if (Number.isNaN(openedMs)) return false
  return now.getTime() - openedMs >= thresholdMs
}
