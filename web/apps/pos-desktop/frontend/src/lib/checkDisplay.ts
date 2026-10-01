// Display derivation for adisyon rows and masa planı cards: which checks are
// masasız servis (Gel Al / Paket), what a row is titled, and how long a check
// has been open. Kept out of the components for the same reason as
// lib/cashSession.ts — testable in the node vitest environment without the
// generated wailsjs bindings, so the wire shape is hand-declared here.

/** The slice of CheckDTO this module reads (see lib/cashSession.ts's
 * file-level comment for why this is not imported from wailsjs). */
export type CheckLike = {
  id: string
  table_label: string
  opened_at: string
  total?: number
  service_type?: string
  customer_name?: string
}

export type ServiceKind = 'takeaway' | 'delivery'

/** Short Turkish label for a masasız servis check ("GEL AL" / "PAKET"). */
export const SERVICE_LABELS: Readonly<Record<ServiceKind, string>> = {
  takeaway: 'GEL AL',
  delivery: 'PAKET',
}

export function serviceKind(check: CheckLike): ServiceKind | null {
  if (check.service_type === 'takeaway') return 'takeaway'
  if (check.service_type === 'delivery') return 'delivery'
  // dine_in, "" and older responses without the field are all table service.
  return null
}

/**
 * Splits the open-check list for the masa planı tabs: table-bound (dine-in)
 * checks stay on the plan, takeaway/delivery each get a tab of their own.
 * Order within each bucket preserves the input (backend returns opened_at
 * order).
 */
export function splitServiceChecks<T extends CheckLike>(checks: readonly T[]): {
  dineIn: T[]
  takeaway: T[]
  delivery: T[]
} {
  const dineIn: T[] = []
  const takeaway: T[] = []
  const delivery: T[] = []
  for (const check of checks) {
    const kind = serviceKind(check)
    if (kind === 'takeaway') takeaway.push(check)
    else if (kind === 'delivery') delivery.push(check)
    else dineIn.push(check)
  }
  return { dineIn, takeaway, delivery }
}

/**
 * Row title: a table check is named after its table; a masasız servis check
 * after its customer ("GEL AL · Alper Vural"). A takeaway check without a
 * recorded customer (POS-opened "Paket servis") falls back to its label so
 * the row never reads "GEL AL · " with nothing after the dot.
 */
export function checkTitle(check: CheckLike): string {
  const kind = serviceKind(check)
  if (kind) {
    const name = check.customer_name?.trim()
    return name ? `${SERVICE_LABELS[kind]} · ${name}` : `${SERVICE_LABELS[kind]} · ${check.table_label || 'Müşteri'}`
  }
  return check.table_label || 'Masa'
}

/** "38 dk" / "1 sa 5 dk" since the check opened; "" for an unparsable date. */
export function elapsedLabel(openedAt: string, nowMs: number = Date.now()): string {
  const openedMs = Date.parse(openedAt)
  if (Number.isNaN(openedMs)) return ''
  const minutes = Math.max(0, Math.floor((nowMs - openedMs) / 60000))
  if (minutes < 60) return `${minutes} dk`
  return `${Math.floor(minutes / 60)} sa ${minutes % 60} dk`
}

/**
 * Index of open checks by id, for the masa planı: an occupied table card
 * carries its check's total and elapsed time (active_check_id -> check).
 */
export function checksById<T extends CheckLike>(checks: readonly T[]): ReadonlyMap<string, T> {
  return new Map(checks.map((c) => [c.id, c]))
}
