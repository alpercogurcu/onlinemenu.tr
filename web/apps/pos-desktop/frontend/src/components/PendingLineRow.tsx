import { composeOptionNote, formatMoney, pendingLineTotal, type PendingLine } from '@onlinemenu/pos-core'
import { TriangleAlertIcon } from './icons'

type PendingLineRowProps = {
  line: PendingLine
  onChangeQuantity: (clientId: string, delta: number) => void
  onRemove: (clientId: string) => void
}

const STEP_BUTTON_CLASS =
  'flex min-h-12 min-w-12 items-center justify-center rounded-md border border-line bg-surface text-xl font-bold text-ink disabled:cursor-not-allowed disabled:opacity-40'

/**
 * One not-yet-sent line on the receipt rail: name and total on top, then the
 * stepper and the remove button on their own row. The 384px rail cannot hold
 * three 48px controls beside a product name, so the controls get a row of
 * their own — every target stays >= 48px with an 8px gap (spec §2 ilke 3).
 * "−" stops at 1; deleting a line is the deliberate ×, never a stray tap.
 * The chosen options sit under the name as a second, smaller line, so the
 * cashier can read back exactly what the kitchen will get.
 */
export function PendingLineRow({ line, onChangeQuantity, onRemove }: PendingLineRowProps) {
  return (
    <div className="receipt-line-enter border-t border-dashed border-line/60 py-2 text-ink-dim">
      <div className="flex items-baseline justify-between gap-2">
        <span className="min-w-0 flex-1 break-words">
          {line.productName} <span className="text-xs">(gönderilmedi)</span>
        </span>
        <span className="money tabular-nums">{formatMoney(pendingLineTotal(line))}</span>
      </div>

      {(line.modifiers.length > 0 || line.note) && (
        <p className="mt-0.5 break-words text-xs text-ink-dim">{composeOptionNote(line.modifiers, line.note)}</p>
      )}
      {line.optionsUnavailable && (
        <p className="mt-0.5 flex items-center gap-1 text-xs font-semibold text-warn">
          <TriangleAlertIcon size={12} />
          Seçenek alınamadı — mutfağa sözlü iletin
        </p>
      )}

      <div className="mt-1 flex items-center gap-2">
        <button
          type="button"
          disabled={line.quantity <= 1}
          aria-label={`${line.productName} adedini azalt`}
          onClick={() => onChangeQuantity(line.clientId, -1)}
          className={STEP_BUTTON_CLASS}
        >
          −
        </button>
        <span className="qty min-w-8 text-center text-base font-semibold text-ink" aria-live="polite">
          {line.quantity}
        </span>
        <button
          type="button"
          aria-label={`${line.productName} adedini artır`}
          onClick={() => onChangeQuantity(line.clientId, 1)}
          className={STEP_BUTTON_CLASS}
        >
          +
        </button>
        <button
          type="button"
          aria-label={`${line.productName} satırını kaldır`}
          onClick={() => onRemove(line.clientId)}
          className="ml-auto flex min-h-12 min-w-12 items-center justify-center rounded-md text-xl text-danger"
        >
          ×
        </button>
      </div>
    </div>
  )
}
