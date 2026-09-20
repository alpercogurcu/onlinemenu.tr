import { useEffect, useRef, useState } from 'react'
import { TURKISH_DENOMINATIONS, denominationTotal, type DenominationRow } from '../lib/cashSession'
import { formatMoney } from '../lib/format'
import { Numpad } from './Numpad'

type DenominationCounterProps = {
  rows: readonly DenominationRow[]
  onChange: (rows: DenominationRow[]) => void
  disabled?: boolean
}

const MAX_COUNT = 9999

const STEP_BUTTON_CLASS =
  'min-h-12 min-w-12 rounded-md border border-line text-xl font-bold text-ink disabled:opacity-40'

/**
 * Sayım (closing count) input: one row per fixed Turkish banknote/coin
 * (lib/cashSession.ts's TURKISH_DENOMINATIONS), a large +/- stepper pair per
 * row (touchscreen — no hover-only affordance), and a running total footer
 * that is the ONLY total this screen ever computes — the cashier never types a
 * total separately (see denominationTotal's doc comment on why that makes
 * ErrDenominationSumMismatch unreachable by construction).
 *
 * The kiosk has no keyboard, so the count itself is a button: tapping it opens
 * a Numpad under that row for counts the stepper would take too long to reach
 * (say, 40 × ₺5). The first key press replaces the shown count rather than
 * appending to it.
 */
export function DenominationCounter({ rows, onChange, disabled = false }: DenominationCounterProps) {
  const [editing, setEditing] = useState<number | null>(null)
  const [replaceOnNextKey, setReplaceOnNextKey] = useState(false)
  const editingRowRef = useRef<HTMLLIElement | null>(null)

  useEffect(() => {
    // The modal body scrolls; without this the keypad can open below the fold.
    editingRowRef.current?.scrollIntoView?.({ block: 'nearest' })
  }, [editing])

  function setCount(denominationMinor: number, count: number) {
    const clamped = Math.max(0, Math.min(MAX_COUNT, count))
    onChange(rows.map((r) => (r.denominationMinor === denominationMinor ? { ...r, count: clamped } : r)))
  }

  function toggleEditing(denominationMinor: number) {
    setReplaceOnNextKey(true)
    setEditing((current) => (current === denominationMinor ? null : denominationMinor))
  }

  return (
    <div>
      <ul className="divide-y divide-line">
        {TURKISH_DENOMINATIONS.map((d) => {
          const row = rows.find((r) => r.denominationMinor === d.denominationMinor)
          const count = row?.count ?? 0
          const isEditing = editing === d.denominationMinor
          return (
            <li key={d.denominationMinor} ref={isEditing ? editingRowRef : undefined} className="py-2">
              <div className="flex items-center justify-between gap-3">
                <span className="w-20 shrink-0 font-medium text-ink tabular-nums">{d.label}</span>
                <div className="flex flex-1 items-center justify-center gap-2">
                  <button
                    type="button"
                    disabled={disabled || count <= 0}
                    onClick={() => setCount(d.denominationMinor, count - 1)}
                    className={STEP_BUTTON_CLASS}
                    aria-label={`${d.label} sayısını azalt`}
                  >
                    −
                  </button>
                  <button
                    type="button"
                    disabled={disabled}
                    aria-expanded={isEditing}
                    aria-label={`${d.label} adedi ${count}, tuş takımıyla değiştir`}
                    onClick={() => toggleEditing(d.denominationMinor)}
                    className={`min-h-12 w-16 rounded-md border bg-surface text-center text-lg tabular-nums text-ink ${
                      isEditing ? 'border-amber' : 'border-line'
                    }`}
                  >
                    {count}
                  </button>
                  <button
                    type="button"
                    disabled={disabled}
                    onClick={() => setCount(d.denominationMinor, count + 1)}
                    className={STEP_BUTTON_CLASS}
                    aria-label={`${d.label} sayısını artır`}
                  >
                    +
                  </button>
                </div>
                <span className="w-24 shrink-0 text-right text-ink-dim tabular-nums">
                  {count > 0 ? formatMoney(d.denominationMinor * count) : ''}
                </span>
              </div>

              {isEditing && (
                <div className="mt-2 space-y-2">
                  <Numpad
                    mode="integer"
                    maxLength={String(MAX_COUNT).length}
                    disabled={disabled}
                    value={String(count)}
                    pendingReplace={replaceOnNextKey}
                    onChange={(next) => {
                      setReplaceOnNextKey(false)
                      setCount(d.denominationMinor, Number.parseInt(next, 10) || 0)
                    }}
                  />
                  <button
                    type="button"
                    onClick={() => setEditing(null)}
                    className="min-h-12 w-full rounded-md border border-line font-semibold text-ink"
                  >
                    Tamam
                  </button>
                </div>
              )}
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
