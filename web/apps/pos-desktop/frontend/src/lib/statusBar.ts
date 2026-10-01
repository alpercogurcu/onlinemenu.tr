// Derivations for the üst durum çubuğu (StatusBar): the kasa chip text and
// the device badge list. Pure functions, node-testable — wire shapes are
// hand-declared (see lib/cashSession.ts's file-level comment).

import { formatMoney } from '@onlinemenu/pos-core'

/** The slice of CashSessionDTO the chip reads. */
export type CashSessionLike = {
  status: string
  opening_counted_amount: number
}

/** The hardware:printer event shape (see App.tsx's PrinterEvent). */
export type DeviceStatusLike = {
  status: 'connected' | 'disconnected' | 'error'
  error?: string
}

/**
 * "KASA AÇIK · ₺2.000,00 açılış" — the opening count, not the running
 * expected total: the chip identifies the session, the kasa screen itself
 * owns the live drawer math. A submitted closing count is flagged so the
 * cashier knows the session is mid gün sonu.
 */
export function cashChipLabel(session: CashSessionLike): string {
  const base = `KASA AÇIK · ${formatMoney(session.opening_counted_amount)} açılış`
  return session.status === 'closing_control' ? `${base} · sayım gönderildi` : base
}

export type DeviceBadge = {
  key: string
  label: string
  /** Raw device error for the badge's title attribute ("" when none). */
  detail: string
}

/**
 * Right-side device badges, problem states only — a connected device is
 * silent (pos-ux-spec §2: the bar reports what needs attention, not an
 * inventory). ÖKC is a fixed "yok" badge until the Faz 2 device integration
 * lands: the mock adapter means no fiscal device is ever attached, and
 * pretending otherwise would train the cashier to ignore the slot.
 */
export function deviceBadges(
  printer: DeviceStatusLike | null,
  kitchenPrinter: DeviceStatusLike | null,
): DeviceBadge[] {
  const badges: DeviceBadge[] = []
  if (printer && printer.status !== 'connected') {
    badges.push({
      key: 'printer',
      label: printer.status === 'error' ? 'Yazıcı hata' : 'Yazıcı yok',
      detail: printer.error ?? '',
    })
  }
  if (kitchenPrinter && kitchenPrinter.status !== 'connected') {
    badges.push({
      key: 'kitchen-printer',
      label: kitchenPrinter.status === 'error' ? 'Mutfak yazıcısı hata' : 'Mutfak yazıcısı yok',
      detail: kitchenPrinter.error ?? '',
    })
  }
  badges.push({ key: 'okc', label: 'ÖKC yok', detail: 'Mali cihaz entegrasyonu Faz 2 — mock adaptör etkin' })
  return badges
}
