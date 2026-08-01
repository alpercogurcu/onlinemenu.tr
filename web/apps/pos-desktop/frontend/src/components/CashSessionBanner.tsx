import type { main } from '../../wailsjs/go/models'
import { formatMoney } from '../lib/format'

type CashSessionBannerProps = {
  /** False until the first GetActiveCashSession call has resolved — renders
   * nothing rather than flashing "kasa kapalı" before the app even knows. */
  checked: boolean
  session: main.CashSessionDTO | null
  /** A submitted closing count has drifted from what it was counted against
   * (see hooks/useCashSession.ts) — shown even when the modal is closed, so
   * a cashier who walked away mid-kapanış is not surprised by it later. */
  stale: boolean
  onOpen: () => void
}

/**
 * Full-width banner, same visual slot as the printError/mali-kayıt-hatası
 * rows in App.tsx (right under the header) — a badge next to "Çıkış" would be
 * missable on a touchscreen, and ADR-DATA-008's task brief is explicit: "do
 * not silently allow selling as if nothing is missing". This does not BLOCK
 * selling (the backend does not gate RegisterCashPayment on cash session
 * state — see the report), it only makes the missing state impossible to miss.
 *
 * Renders nothing once a session is open and not stale — an open, on-track
 * cash session needs no persistent chrome; the modal (opened via this same
 * banner turning into a compact status line) is where its figures live.
 */
export function CashSessionBanner({ checked, session, stale, onOpen }: CashSessionBannerProps) {
  if (!checked) return null

  if (!session) {
    return (
      <div className="flex shrink-0 items-center justify-between gap-3 border-b border-line bg-amber/10 px-4 py-2 text-sm text-ink">
        <span>Bu şubede açık kasa oturumu yok — satış öncesi kasa açılmalı.</span>
        <button
          type="button"
          onClick={onOpen}
          className="min-h-10 shrink-0 rounded bg-amber px-3 font-semibold text-amber-ink"
        >
          Kasa Aç
        </button>
      </div>
    )
  }

  if (stale) {
    return (
      <div className="flex shrink-0 items-center justify-between gap-3 border-b border-line bg-amber/10 px-4 py-2 text-sm text-ink">
        <span>Kasa sayımı bayatladı — kasa bakiyesi değişti, yeniden sayılmalı.</span>
        <button
          type="button"
          onClick={onOpen}
          className="min-h-10 shrink-0 rounded bg-amber px-3 font-semibold text-amber-ink"
        >
          Kasayı Aç
        </button>
      </div>
    )
  }

  return (
    <div className="flex shrink-0 items-center justify-between gap-3 border-b border-line px-4 py-1.5 text-xs text-ink-dim">
      <span>
        Kasa açık — beklenen kapanış {formatMoney(session.expected_close)}
        {session.status === 'closing_control' ? ' (sayım gönderildi)' : ''}
      </span>
      <button type="button" onClick={onOpen} className="min-h-8 shrink-0 rounded px-2 font-semibold text-ink">
        Kasa
      </button>
    </div>
  )
}
