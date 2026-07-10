import { useEffect, useState } from 'react'
import {
  PENDING_BADGE_DELAY_MS,
  shouldRenderPendingBadge,
  type FiscalStatus,
  type TrackedPayment,
} from '../lib/fiscalStatus'
import { BanIcon, CheckIcon, ClockIcon, EyeOffIcon, TriangleAlertIcon } from './icons'

/**
 * Status is carried by ICON + TEXT, never by color alone (WCAG 1.4.1). The
 * colors below are the app's existing meaning-locked tokens.
 *
 * PALETTE CONFLICT — flagged to team-lead / ui-designer, implemented as
 * specified: style.css reserves --color-danger exclusively for void/cancel
 * ("başka hiçbir yerde kırmızı yok — anlam koruması"; ErrorBanner.tsx goes out
 * of its way to render errors WITHOUT red for exactly this reason). The task
 * spec assigns red to `failed` and grey to `voided` — which inverts that rule:
 * red now means "fiscal failure" while an actual void (fiş iptali) is grey.
 * Following the explicit spec here; ui-designer should arbitrate.
 */
const PRESENTATION: Record<FiscalStatus, { label: string; className: string; Icon: typeof ClockIcon }> = {
  pending: {
    label: 'Mali kayıt bekliyor',
    className: 'bg-amber/15 text-amber',
    Icon: ClockIcon,
  },
  completed: {
    label: 'Fiş kesildi',
    className: 'bg-teal/15 text-teal',
    Icon: CheckIcon,
  },
  failed: {
    label: 'Başarısız',
    className: 'bg-danger/15 text-danger',
    Icon: TriangleAlertIcon,
  },
  voided: {
    label: 'İptal',
    className: 'bg-line/40 text-ink-dim',
    Icon: BanIcon,
  },
  unknown: {
    label: 'Durum okunamıyor',
    className: 'bg-line/40 text-ink-dim',
    Icon: EyeOffIcon,
  },
}

/** Explains the `unknown` badge on hover/long-press rather than leaving the
 * cashier guessing why the receipt state is invisible. */
const UNKNOWN_TITLE =
  'Bu istasyonun rolü ödeme durumunu okuyamıyor (payment.payment.read yetkisi yok). ' +
  'Adisyon kapanışı yine de sunucu tarafından denetleniyor.'

/**
 * Requirement 6: a payment that settles faster than PENDING_BADGE_DELAY_MS
 * never renders the amber "bekliyor" badge — it goes straight to green. This is
 * a *delayed render*, not an optimistic one: the green badge still only appears
 * once the server has actually reported `completed`.
 *
 * Implemented as a one-shot timer that forces a single re-render at the 300ms
 * mark. If the payment resolves before then, `status !== 'pending'` and the
 * gate opens immediately anyway.
 */
function usePendingBadgeVisible(payment: TrackedPayment): boolean {
  const [, forceRender] = useState(0)
  const visible = shouldRenderPendingBadge(payment, Date.now())

  useEffect(() => {
    if (visible) return
    const elapsed = Date.now() - payment.registeredAtMs
    const id = setTimeout(() => forceRender((n) => n + 1), Math.max(0, PENDING_BADGE_DELAY_MS - elapsed))
    return () => clearTimeout(id)
  }, [visible, payment.registeredAtMs])

  return visible
}

export function FiscalStatusBadge({ payment }: { payment: TrackedPayment }) {
  const visible = usePendingBadgeVisible(payment)
  if (!visible) return null

  const { label, className, Icon } = PRESENTATION[payment.status]
  const isPending = payment.status === 'pending'

  return (
    <span
      className={`inline-flex items-center gap-1.5 rounded-full px-2 py-0.5 text-xs font-semibold ${className} ${
        isPending ? 'fiscal-pending-pulse' : ''
      }`}
      title={payment.status === 'unknown' ? UNKNOWN_TITLE : undefined}
      // Pending is a live-updating status the cashier is waiting on; announcing
      // the transition to "Fiş kesildi" is the whole point of the badge.
      role="status"
    >
      <Icon size={14} />
      {label}
    </span>
  )
}
