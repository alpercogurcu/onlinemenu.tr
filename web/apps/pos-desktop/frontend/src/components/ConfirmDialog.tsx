import { useEffect } from 'react'

type ConfirmDialogProps = {
  title: string
  message: string
  confirmLabel: string
  onConfirm: () => void
  onCancel: () => void
  busy?: boolean
}

/**
 * Centered confirmation for the one step that cannot be undone (merging two
 * adisyons). The POS otherwise avoids confirmation modals — closing or voiding
 * uses hold-to-confirm — but a merge starts from a floor-plan tap, so a hold
 * has nothing to be held on; this names what will happen instead.
 */
export function ConfirmDialog({ title, message, confirmLabel, onConfirm, onCancel, busy = false }: ConfirmDialogProps) {
  useEffect(() => {
    function onKeyDown(event: KeyboardEvent) {
      if (event.key === 'Escape') onCancel()
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [onCancel])

  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-labelledby="confirm-title"
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4"
    >
      <div className="w-full max-w-md rounded-lg border border-line bg-panel p-4">
        <h2 id="confirm-title" className="font-display text-lg font-bold text-ink">
          {title}
        </h2>
        <p className="mt-2 text-sm text-ink">{message}</p>
        <div className="mt-4 flex gap-2">
          <button type="button" onClick={onCancel} className="min-h-14 flex-1 rounded-lg border border-line font-semibold text-ink">
            Vazgeç
          </button>
          <button
            type="button"
            autoFocus
            disabled={busy}
            onClick={onConfirm}
            className="min-h-14 flex-[2] rounded-lg bg-amber font-display text-lg font-bold text-amber-ink disabled:opacity-40"
          >
            {busy ? 'İşleniyor…' : confirmLabel}
          </button>
        </div>
      </div>
    </div>
  )
}
