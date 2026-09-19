// Kitchen-ticket print failures, tracked per order.
//
// The kitchen ticket is fired right after PlaceOrder succeeds and is
// best-effort by design: the order itself is already recorded, so a printer
// fault must not fail it — but a ticket that never reached the kitchen means
// food nobody cooks, so every failure stays visible until it is reprinted or
// explicitly dismissed. A list (not a single slot) so a second failed order
// cannot silently overwrite the first.

export type KitchenPrintFailure = {
  orderId: string
  tableLabel: string
  message: string
}

/** First 8 characters of the order id — the number printed on the ticket. */
export function shortOrderId(orderId: string): string {
  return orderId.slice(0, 8)
}

export function addKitchenFailure(
  failures: readonly KitchenPrintFailure[],
  failure: KitchenPrintFailure,
): KitchenPrintFailure[] {
  return [...removeKitchenFailure(failures, failure.orderId), failure]
}

export function removeKitchenFailure(
  failures: readonly KitchenPrintFailure[],
  orderId: string,
): KitchenPrintFailure[] {
  return failures.filter((f) => f.orderId !== orderId)
}

export function describeKitchenFailure(failure: KitchenPrintFailure): string {
  const ref = `#${shortOrderId(failure.orderId)}`
  const where = failure.tableLabel ? `${failure.tableLabel} · ${ref}` : ref
  return `Mutfak fişi yazdırılamadı (${where}): ${failure.message}`
}

/**
 * Wire shape of the `hardware:kitchen-print` event (main.KitchenPrintResultDTO):
 * the outcome of a dispatcher-driven print — an order that reached the branch
 * without passing through this station's own PlaceOrder, e.g. a QR self-order.
 * Success is emitted too so a later automatic retry can clear the banner.
 */
export type KitchenPrintResultEvent = {
  order_id: string
  table_label: string
  ok: boolean
  error?: string
}

export function applyKitchenPrintResult(
  failures: readonly KitchenPrintFailure[],
  evt: KitchenPrintResultEvent,
): KitchenPrintFailure[] {
  if (evt.ok) return removeKitchenFailure(failures, evt.order_id)
  return addKitchenFailure(failures, {
    orderId: evt.order_id,
    tableLabel: evt.table_label,
    message: evt.error || 'bilinmeyen hata',
  })
}
