import { useState } from 'react'
import type { main } from '../../wailsjs/go/models'

type CheckRailProps = {
  checks: main.CheckDTO[]
  selectedCheckId: string | null
  onSelect: (checkId: string) => void
  onOpenCheck: (tableLabel: string) => Promise<void>
  canOpenCheck: boolean
}

function elapsedLabel(openedAt: string): string {
  const openedMs = Date.parse(openedAt)
  if (Number.isNaN(openedMs)) return ''
  const minutes = Math.max(0, Math.floor((Date.now() - openedMs) / 60000))
  if (minutes < 60) return `${minutes} dk`
  return `${Math.floor(minutes / 60)} sa ${minutes % 60} dk`
}

/** Left rail: open adisyon list (teal status) + new-table entry. */
export function CheckRail({ checks, selectedCheckId, onSelect, onOpenCheck, canOpenCheck }: CheckRailProps) {
  const [tableLabel, setTableLabel] = useState('')
  const [opening, setOpening] = useState(false)

  async function handleOpen(e: React.FormEvent) {
    e.preventDefault()
    if (!tableLabel.trim()) return
    setOpening(true)
    try {
      await onOpenCheck(tableLabel.trim())
      setTableLabel('')
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

      <form onSubmit={handleOpen} className="border-t border-line p-4">
        <label className="mb-1 block text-xs text-ink-dim" htmlFor="table-label">
          Yeni masa
        </label>
        <div className="flex gap-2">
          <input
            id="table-label"
            className="min-h-14 flex-1 rounded-md border border-line bg-surface px-3 py-2 text-ink"
            placeholder="Masa 4"
            value={tableLabel}
            onChange={(e) => setTableLabel(e.target.value)}
            disabled={!canOpenCheck}
          />
          <button
            type="submit"
            disabled={!canOpenCheck || opening || !tableLabel.trim()}
            className="min-h-14 rounded-md bg-amber px-4 font-semibold text-amber-ink disabled:opacity-40"
          >
            Aç
          </button>
        </div>
        {!canOpenCheck && (
          <p className="mt-2 text-xs text-ink-dim">
            Bu istasyon şubeye bağlı değil — adisyon açmak için şube bazlı oturum gerekli.
          </p>
        )}
      </form>
    </aside>
  )
}
