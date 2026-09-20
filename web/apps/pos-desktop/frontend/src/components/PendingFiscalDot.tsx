import { ClockIcon } from './icons'

/**
 * Requirement 5: a small warn-colored indicator on the adisyon listesi / masa
 * planı for a check with a payment still awaiting its fiscal record.
 *
 * Not color-only: the dot is paired with a clock glyph and screen-reader text.
 * The AUTHORITATIVE, fully-labelled status lives on the payment row in the
 * receipt rail (FiscalStatusBadge) — this is a glanceable pointer to it, which
 * is why a 8px dot is an acceptable density here.
 *
 * It always sits on its own dark chip (bg-surface). The masa planı paints a
 * table card in whatever color its status dictates (slate for occupied), and a
 * bare warn dot on slate falls below 3:1; on the chip the contrast is the same
 * wherever the indicator is placed, so it needs no per-surface variant.
 */
export function PendingFiscalDot() {
  return (
    <span
      className="inline-flex shrink-0 items-center gap-1 rounded-full bg-surface px-1.5 py-0.5 text-warn"
      title="Mali kayıt bekleniyor"
    >
      <span aria-hidden="true" className="fiscal-pending-pulse h-2 w-2 rounded-full bg-warn" />
      <ClockIcon size={12} />
      <span className="sr-only">Mali kayıt bekleniyor</span>
    </span>
  )
}
