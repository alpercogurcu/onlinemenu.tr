import type { main } from '../../wailsjs/go/models'
import { cashChipLabel, deviceBadges, type DeviceStatusLike } from '../lib/statusBar'

type StatusBarProps = {
  /** null only while the first cash-session fetch is in flight — the kasa
   * kilidi screen (App.tsx) guarantees an open session once `checked`. */
  cashSession: main.CashSessionDTO | null
  cashierName: string
  cashierEmail: string
  printer: DeviceStatusLike | null
  kitchenPrinter: DeviceStatusLike | null
  onOpenCashScreen: () => void
  onSwitchCashier: () => void
  canSwitchCashier: boolean
  onLogout: () => void
}

/**
 * Üst durum çubuğu (AnaEkran tasarımı): left identifies the shift — the
 * green "KASA AÇIK" chip plus the cashier — and the right side holds the
 * device badges and the session-level actions. Replaces the old header's
 * CashSessionStatusButton and the permanent cash banner: an open, on-track
 * kasa now lives here, the banner slot is for warnings only (stale sayım,
 * fiscal/print failures).
 *
 * Teal = done/on-track per the token contract (style.css); the chip is a
 * button because "one tap to the kasa screen" was the status button's whole
 * job and the chip inherits it. Warn (never red) marks a missing device —
 * red stays reserved for void/cancel.
 */
export function StatusBar({
  cashSession,
  cashierName,
  cashierEmail,
  printer,
  kitchenPrinter,
  onOpenCashScreen,
  onSwitchCashier,
  canSwitchCashier,
  onLogout,
}: StatusBarProps) {
  return (
    <header className="flex min-h-14 shrink-0 flex-wrap items-center gap-x-4 gap-y-1 border-b border-line px-4 py-1 text-sm">
      {cashSession ? (
        <button
          type="button"
          onClick={onOpenCashScreen}
          className="inline-flex min-h-11 shrink-0 items-center gap-2 rounded-full bg-teal/15 px-4 text-[13px] font-bold text-teal"
        >
          <span aria-hidden="true" className="h-2 w-2 rounded-full bg-teal" />
          {cashChipLabel(cashSession)}
        </button>
      ) : (
        <span className="inline-flex min-h-11 items-center text-ink-dim">Kasa durumu yükleniyor…</span>
      )}
      <span className="truncate text-ink-dim" title={cashierEmail}>
        {cashierName}
      </span>

      <div className="ml-auto flex flex-wrap items-center gap-x-4 gap-y-1">
        {deviceBadges(printer, kitchenPrinter).map((badge) => (
          <span
            key={badge.key}
            className="inline-flex items-center gap-1.5 text-xs text-ink-dim"
            title={badge.detail}
          >
            <span aria-hidden="true" className="h-2 w-2 shrink-0 rounded-full bg-warn" />
            {badge.label}
          </span>
        ))}
        <button
          type="button"
          onClick={onOpenCashScreen}
          className="min-h-11 rounded-md border border-line px-4 font-semibold text-ink"
        >
          Gün Sonu / Kasa Kapat
        </button>
        <button
          type="button"
          disabled={!canSwitchCashier}
          title={
            canSwitchCashier
              ? undefined
              : 'Kasiyer değiştirmek için önce kasa açık olmalı — kasa oturumu, katılımın bağlı olduğu şey.'
          }
          onClick={onSwitchCashier}
          className="min-h-11 rounded px-3 text-ink-dim disabled:opacity-40"
        >
          Kasiyer Değiştir
        </button>
        <button type="button" onClick={onLogout} className="min-h-11 rounded px-3 text-ink-dim">
          Çıkış
        </button>
      </div>
    </header>
  )
}
