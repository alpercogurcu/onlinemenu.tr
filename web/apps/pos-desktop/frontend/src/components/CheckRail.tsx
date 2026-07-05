import { useState } from 'react'
import type { main } from '../../wailsjs/go/models'

type CheckRailProps = {
  checks: main.CheckDTO[]
  selectedCheckId: string | null
  onSelect: (checkId: string) => void
  onOpenTakeaway: () => Promise<void>
  canOpenCheck: boolean
}

function elapsedLabel(openedAt: string): string {
  const openedMs = Date.parse(openedAt)
  if (Number.isNaN(openedMs)) return ''
  const minutes = Math.max(0, Math.floor((Date.now() - openedMs) / 60000))
  if (minutes < 60) return `${minutes} dk`
  return `${Math.floor(minutes / 60)} sa ${minutes % 60} dk`
}

/**
 * Left rail: open adisyon list (teal status) + masasız satış ("Paket
 * servis") entry point. Table-bound adisyon açma artık burada değil — bkz.
 * TablePlan.tsx (Sprint-5 Wave 2 masa planı, center panel when no adisyon is
 * selected).
 */
export function CheckRail({ checks, selectedCheckId, onSelect, onOpenTakeaway, canOpenCheck }: CheckRailProps) {
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
                  <span className="flex-1">
                    <span className="block font-medium text-ink">{chk.table_label || 'Masa'}</span>
                    <span className="block text-xs text-ink-dim tabular-nums">
                      {elapsedLabel(chk.opened_at)}
                    </span>
                  </span>
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
