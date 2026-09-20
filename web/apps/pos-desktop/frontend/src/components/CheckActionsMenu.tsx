import { useEffect, useState } from 'react'

export type CheckActionsDisabled = {
  transfer?: string
  merge?: string
  moveItems?: string
}

type CheckActionsMenuProps = {
  disabled: CheckActionsDisabled
  onTransfer: () => void
  onMerge: () => void
  onMoveItems: () => void
}

type Entry = { key: keyof CheckActionsDisabled; label: string; hint: string; run: () => void }

/**
 * The "⋯" menu in the adisyon header (docs/pos-ux-spec.md §3c): masayı taşı,
 * masaları birleştir, kalem taşı. An action that cannot run right now stays in
 * the list with the reason under it (spec ilke 5) instead of disappearing, so
 * the cashier learns what to do first.
 */
export function CheckActionsMenu({ disabled, onTransfer, onMerge, onMoveItems }: CheckActionsMenuProps) {
  const [open, setOpen] = useState(false)

  useEffect(() => {
    if (!open) return
    function onKeyDown(event: KeyboardEvent) {
      if (event.key === 'Escape') setOpen(false)
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [open])

  const entries: Entry[] = [
    { key: 'transfer', label: 'Masayı taşı', hint: 'Adisyonu boş bir masaya geçir', run: onTransfer },
    { key: 'merge', label: 'Masaları birleştir', hint: 'Bu adisyonu başka bir masanınkine ekle', run: onMerge },
    { key: 'moveItems', label: 'Kalem taşı', hint: 'Seçilen kalemleri başka bir masaya ver', run: onMoveItems },
  ]

  return (
    <div className="relative">
      <button
        type="button"
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label="Adisyon işlemleri"
        onClick={() => setOpen((v) => !v)}
        className="flex min-h-12 min-w-12 items-center justify-center rounded-md border border-line text-xl text-ink"
      >
        ⋯
      </button>
      {open && (
        <>
          <button
            type="button"
            aria-label="Menüyü kapat"
            tabIndex={-1}
            onClick={() => setOpen(false)}
            className="fixed inset-0 z-40 cursor-default"
          />
          <ul role="menu" className="absolute right-0 top-full z-50 mt-2 w-72 space-y-1 rounded-lg border border-line bg-panel p-2 shadow-lg">
            {entries.map((entry) => {
              const reason = disabled[entry.key]
              return (
                <li key={entry.key} role="none">
                  <button
                    type="button"
                    role="menuitem"
                    disabled={Boolean(reason)}
                    onClick={() => {
                      setOpen(false)
                      entry.run()
                    }}
                    className="flex min-h-14 w-full flex-col items-start justify-center rounded-md px-3 py-2 text-left disabled:cursor-not-allowed disabled:opacity-60"
                  >
                    <span className="font-semibold text-ink">{entry.label}</span>
                    <span className={`text-xs ${reason ? 'text-warn' : 'text-ink-dim'}`}>{reason ?? entry.hint}</span>
                  </button>
                </li>
              )
            })}
          </ul>
        </>
      )}
    </div>
  )
}
