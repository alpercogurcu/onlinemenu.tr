import { ClockIcon } from './icons'

/**
 * Requirement 5: a small amber indicator on the adisyon listesi / masa planı
 * for a check with a payment still awaiting its fiscal record.
 *
 * Not color-only: the dot is paired with a clock glyph and screen-reader text.
 * The AUTHORITATIVE, fully-labelled status lives on the payment row in the
 * receipt rail (FiscalStatusBadge) — this is a glanceable pointer to it, which
 * is why a 8px dot is an acceptable density here.
 *
 * `onAmber` exists because an occupied masa card is already painted
 * --color-amber (TablePlan's `variant`), where an amber dot is invisible. On
 * that surface the indicator flips to --color-amber-ink (the dark ink already
 * used for text on amber), preserving contrast without introducing a fifth
 * meaning-bearing color.
 */
export function PendingFiscalDot({ onAmber = false }: { onAmber?: boolean }) {
  const tone = onAmber ? 'text-amber-ink' : 'text-amber'
  const dot = onAmber ? 'bg-amber-ink' : 'bg-amber'

  return (
    <span className={`inline-flex shrink-0 items-center gap-1 ${tone}`} title="Mali kayıt bekleniyor">
      <span aria-hidden="true" className={`fiscal-pending-pulse h-2 w-2 rounded-full ${dot}`} />
      <ClockIcon size={12} />
      <span className="sr-only">Mali kayıt bekleniyor</span>
    </span>
  )
}
