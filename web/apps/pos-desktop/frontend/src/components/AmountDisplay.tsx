import { formatMoneyInputDisplay } from '../lib/numpad'

type AmountDisplayProps = {
  label: string
  /** The numpad string being edited ("" while nothing is typed). */
  value: string
}

/**
 * Read-only readout for an amount being keyed on a Numpad. It is a `status`
 * region, not an input: on the kiosk there is nothing to focus and no keyboard
 * to type with, so making it a text field would only invite the OS to raise a
 * keyboard that does not exist.
 */
export function AmountDisplay({ label, value }: AmountDisplayProps) {
  return (
    <div
      role="status"
      aria-label={label}
      className="flex min-h-14 items-baseline justify-between gap-2 rounded-md border border-line bg-surface px-3 py-2"
    >
      <span className="text-sm text-ink-dim">{label}</span>
      <span className="money font-display text-3xl font-bold tabular-nums text-ink">
        {formatMoneyInputDisplay(value)} ₺
      </span>
    </div>
  )
}
