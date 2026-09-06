// Beyond this the exact figure carries no kitchen signal — the ticket is
// simply stale (a forgotten order, or dev/loadtest data left in the DB). It
// is capped so the counter cannot render as "45308:58" and blow out the card
// layout, while still reading as "far too old".
export const MAX_ELAPSED_MINUTES = 99

// Kitchen SLA thresholds (minutes): under WARN the ticket is on time, between
// WARN and LATE it needs attention, past LATE it is late.
export const ELAPSED_WARN_MINUTES = 10
export const ELAPSED_LATE_MINUTES = 20

export type ElapsedTone = "normal" | "warning" | "late"

export function elapsedMinutes(occurredAt: string, now: number): number | null {
  const occurredMs = new Date(occurredAt).getTime()
  if (Number.isNaN(occurredMs)) return null
  return Math.max(0, Math.floor((now - occurredMs) / 60_000))
}

export function formatElapsed(occurredAt: string, now: number): string {
  const occurredMs = new Date(occurredAt).getTime()
  if (Number.isNaN(occurredMs)) return "—"
  const elapsedSec = Math.max(0, Math.floor((now - occurredMs) / 1000))
  const minutes = Math.floor(elapsedSec / 60)
  if (minutes >= MAX_ELAPSED_MINUTES) return `${MAX_ELAPSED_MINUTES}+ dk`
  const seconds = elapsedSec % 60
  return `${minutes}:${seconds.toString().padStart(2, "0")}`
}

export function elapsedTone(occurredAt: string, now: number): ElapsedTone {
  const minutes = elapsedMinutes(occurredAt, now)
  if (minutes === null) return "normal"
  if (minutes >= ELAPSED_LATE_MINUTES) return "late"
  if (minutes >= ELAPSED_WARN_MINUTES) return "warning"
  return "normal"
}

export const ELAPSED_TONE_CLASS: Record<ElapsedTone, string> = {
  normal: "text-muted-foreground",
  warning: "text-status-warning-fg",
  late: "text-status-danger-fg",
}
