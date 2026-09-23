import type { main } from '../../wailsjs/go/models'
import { formatMoney } from '@onlinemenu/pos-core'

type CashSessionBannerProps = {
  kind: 'missing' | 'stale'
  onOpen: () => void
}

/**
 * Warning row for a cash-session problem, same visual slot as the other
 * banners under the header (a badge next to "Çıkış" would be missable on a
 * touchscreen, and ADR-DATA-008's task brief is explicit: "do not silently
 * allow selling as if nothing is missing"). It does not BLOCK selling (the
 * backend does not gate RegisterPayment on card sales, and cash sales are
 * refused server-side with a clear message), it only makes the missing state
 * impossible to miss.
 *
 * Only the two warning states render here (see lib/cashSession's
 * cashSessionBannerKind). An open, on-track session used to add a permanent
 * status row; it is now CashSessionStatusButton in the header, so the banner
 * area holds warnings only (bulgu #11).
 */
export function CashSessionBanner({ kind, onOpen }: CashSessionBannerProps) {
  const missing = kind === 'missing'
  return (
    <div className="flex shrink-0 items-center justify-between gap-3 border-b border-line border-l-4 border-l-warn bg-warn/10 px-4 py-1 text-sm text-ink">
      <span>
        {missing
          ? 'Bu şubede açık kasa oturumu yok — satış öncesi kasa açılmalı.'
          : 'Kasa sayımı bayatladı — kasa bakiyesi değişti, yeniden sayılmalı.'}
      </span>
      <button
        type="button"
        onClick={onOpen}
        className="min-h-12 shrink-0 rounded bg-amber px-3 font-semibold text-amber-ink"
      >
        {missing ? 'Kasa Aç' : 'Kasayı Aç'}
      </button>
    </div>
  )
}

/** Header button for an open, on-track cash session: the expected drawer total, one tap to the kasa screen. */
export function CashSessionStatusButton({ session, onOpen }: { session: main.CashSessionDTO; onOpen: () => void }) {
  return (
    <button type="button" onClick={onOpen} className="min-h-12 rounded px-2 text-ink-dim">
      Kasa açık · {formatMoney(session.expected_close)}
      {session.status === 'closing_control' ? ' (sayım gönderildi)' : ''}
    </button>
  )
}
