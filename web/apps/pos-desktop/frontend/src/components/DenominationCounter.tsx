import { TURKISH_DENOMINATIONS, denominationTotal, type DenominationRow } from '../lib/cashSession'
import { formatMoney } from '../lib/format'

type DenominationCounterProps = {
  rows: readonly DenominationRow[]
  onChange: (rows: DenominationRow[]) => void
  disabled?: boolean
}

/**
 * Sayım (closing count) input: one row per fixed Turkish banknote/coin
 * (lib/cashSession.ts's TURKISH_DENOMINATIONS), a large +/- stepper pair per
 * row (touchscreen — no hover-only affordance, no bare number input as the
 * only way to change a count), and a running total footer that is the ONLY
 * total this screen ever computes — the cashier never types a total
 * separately (see denominationTotal's doc comment on why that makes
 * ErrDenominationSumMismatch unreachable by construction).
 */
export function DenominationCounter({ rows, onChange, disabled = false }: DenominationCounterProps) {
  function setCount(denominationMinor: number, count: number) {
    const clamped = Math.max(0, Math.min(9999, count))
    onChange(rows.map((r) => (r.denominationMinor === denominationMinor ? { ...r, count: clamped } : r)))
  }

  return (
    <div>
      <ul className="divide-y divide-line">
        {TURKISH_DENOMINATIONS.map((d) => {
          const row = rows.find((r) => r.denominationMinor === d.denominationMinor)
          const count = row?.count ?? 0
          return (
            <li key={d.denominationMinor} className="flex items-center justify-between gap-3 py-2">
              <span className="w-20 shrink-0 font-medium text-ink tabular-nums">{d.label}</span>
              <div className="flex flex-1 items-center justify-center gap-3">
                <button
                  type="button"
                  disabled={disabled || count <= 0}
                  onClick={() => setCount(d.denominationMinor, count - 1)}
                  className="min-h-12 min-w-12 rounded-md border border-line text-xl font-bold text-ink disabled:opacity-40"
                  aria-label={`${d.label} sayısını azalt`}
                >
                  −
                </button>
                <input
                  type="number"
                  inputMode="numeric"
                  min={0}
                  max={9999}
                  disabled={disabled}
                  value={count}
                  onChange={(e) => setCount(d.denominationMinor, Number.parseInt(e.target.value, 10) || 0)}
                  className="min-h-12 w-16 rounded-md border border-line bg-surface text-center text-lg tabular-nums text-ink"
                  aria-label={`${d.label} adedi`}
                />
                <button
                  type="button"
                  disabled={disabled}
                  onClick={() => setCount(d.denominationMinor, count + 1)}
                  className="min-h-12 min-w-12 rounded-md border border-line text-xl font-bold text-ink disabled:opacity-40"
                  aria-label={`${d.label} sayısını artır`}
                >
                  +
                </button>
              </div>
              <span className="w-24 shrink-0 text-right text-ink-dim tabular-nums">
                {count > 0 ? formatMoney(d.denominationMinor * count) : ''}
              </span>
            </li>
          )
        })}
      </ul>
      <div className="mt-3 flex items-center justify-between border-t border-line pt-3">
        <span className="font-semibold text-ink">Toplam</span>
        <span className="font-display text-lg font-bold tabular-nums text-ink">{formatMoney(denominationTotal(rows))}</span>
      </div>
    </div>
  )
}
