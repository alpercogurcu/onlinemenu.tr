import { useState } from 'react'
import type { main } from '../../wailsjs/go/models'
import { formatMoney } from '@onlinemenu/pos-core'
import { checkTitle, elapsedLabel, serviceKind, SERVICE_LABELS } from '../lib/checkDisplay'
import { PendingFiscalDot } from './PendingFiscalDot'

// İkincil bilgi satırı (AnaEkran tasarımı): geçen süre; masasız serviste süre
// yoksa bile satır tipi okunur kalsın diye servis etiketi tek başına düşer.
// (Kalem sayısı liste yanıtında yok — adisyon açılınca fişte görünür.)
function secondaryLabel(check: main.CheckDTO): string {
  const elapsed = elapsedLabel(check.opened_at)
  if (elapsed) return elapsed
  const kind = serviceKind(check)
  return kind ? SERVICE_LABELS[kind] : ''
}

type CheckRailProps = {
  checks: main.CheckDTO[]
  selectedCheckId: string | null
  onSelect: (checkId: string) => void
  onOpenTakeaway: () => Promise<void>
  canOpenCheck: boolean
  /** Checks with at least one payment awaiting its fiscal record — BRANCH-WIDE
   * as of the fiscal:branch-pending feed: this station's own tracked payments
   * unioned with every other station's, deduped by payment id (see
   * fiscalStatus.ts's checkIdsAwaitingFiscal). Degrades to this station's own
   * payments only when the session's role lacks payment.fiscal_status.read. */
  awaitingFiscalCheckIds: ReadonlySet<string>
}

/**
 * Left rail: open adisyon list (teal status) + masasız satış ("Paket
 * servis") entry point. Table-bound adisyon açma artık burada değil — bkz.
 * TablePlan.tsx (Sprint-5 Wave 2 masa planı, center panel when no adisyon is
 * selected).
 */
export function CheckRail({
  checks,
  selectedCheckId,
  onSelect,
  onOpenTakeaway,
  canOpenCheck,
  awaitingFiscalCheckIds,
}: CheckRailProps) {
  const [opening, setOpening] = useState(false)

  async function handleOpenTakeaway() {
    setOpening(true)
    try {
      await onOpenTakeaway()
    } finally {
      setOpening(false)
    }
  }

  return (
    <aside className="flex h-full w-72 shrink-0 flex-col border-r border-line bg-panel">
      <div className="border-b border-line p-4">
        <h2 className="font-display text-lg font-bold text-ink">Açık adisyonlar</h2>
      </div>

      <div className="flex-1 overflow-y-auto">
        {checks.length === 0 ? (
          <p className="p-4 text-sm text-ink-dim">Açık adisyon yok — masa seçerek başlayın.</p>
        ) : (
          <ul>
            {checks.map((chk) => (
              <li key={chk.id}>
                <button
                  type="button"
                  onClick={() => onSelect(chk.id)}
                  className={`flex min-h-14 w-full items-center gap-3 border-b border-line px-4 py-3 text-left ${
                    selectedCheckId === chk.id ? 'bg-surface' : ''
                  }`}
                >
                  <span aria-hidden="true" className="h-2.5 w-2.5 shrink-0 rounded-full bg-teal" />
                  <span className="min-w-0 flex-1">
                    {/* Adisyon rayı (AnaEkran tasarımı): bir masasız servis
                        adisyonu masa adı yerine müşterisiyle anılır — bkz.
                        lib/checkDisplay.checkTitle. */}
                    <span className="block truncate font-medium text-ink">{checkTitle(chk)}</span>
                    <span className="block text-xs text-ink-dim tabular-nums">
                      {secondaryLabel(chk)}
                    </span>
                  </span>
                  {awaitingFiscalCheckIds.has(chk.id) && <PendingFiscalDot />}
                  {chk.total !== undefined && chk.total !== null && (
                    <span className="money shrink-0 font-semibold tabular-nums text-ink">{formatMoney(chk.total)}</span>
                  )}
                </button>
              </li>
            ))}
          </ul>
        )}
      </div>

      <div className="border-t border-line p-4">
        <button
          type="button"
          onClick={handleOpenTakeaway}
          disabled={!canOpenCheck || opening}
          className="min-h-14 w-full rounded-md border border-line bg-panel px-4 font-semibold text-ink disabled:opacity-40"
        >
          Paket servis (masasız satış)
        </button>
        {!canOpenCheck && (
          <p className="mt-2 text-xs text-ink-dim">
            Bu istasyon şubeye bağlı değil — adisyon açmak için şube bazlı oturum gerekli.
          </p>
        )}
      </div>
    </aside>
  )
}
