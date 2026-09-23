import { applyNumpadKey, isNumpadKeyEnabled, type NumpadKey, type NumpadMode } from '@onlinemenu/pos-core'

type NumpadProps = {
  value: string
  onChange: (next: string) => void
  mode: NumpadMode
  /** Digit cap for integer/pin modes (see @onlinemenu/pos-core's numpad.ts). */
  maxLength?: number
  /** See NumpadOptions.pendingReplace. */
  pendingReplace?: boolean
  disabled?: boolean
}

type KeySpec = { key: NumpadKey; label: string; ariaLabel?: string; wide?: boolean }

// 4 columns: three digit columns plus one utility column (⌫, 00, ,). Zero
// spans the digit columns like a calculator. Utility keys that a mode does
// not use are left as empty cells, so the digit keys never move between modes
// (muscle memory — same rationale as the fixed product-tile order).
const ROWS: KeySpec[][] = [
  [
    { key: '7', label: '7' },
    { key: '8', label: '8' },
    { key: '9', label: '9' },
    { key: 'backspace', label: '⌫', ariaLabel: 'Sil' },
  ],
  [
    { key: '4', label: '4' },
    { key: '5', label: '5' },
    { key: '6', label: '6' },
    { key: '00', label: '00', ariaLabel: 'Çift sıfır' },
  ],
  [
    { key: '1', label: '1' },
    { key: '2', label: '2' },
    { key: '3', label: '3' },
    { key: ',', label: ',', ariaLabel: 'Virgül' },
  ],
  [{ key: '0', label: '0', wide: true }],
]

const KEY_CLASS =
  'flex min-h-14 select-none items-center justify-center rounded-md border border-line bg-surface font-display text-2xl font-semibold text-ink transition-colors active:bg-line disabled:cursor-not-allowed disabled:opacity-40'

/**
 * On-screen number pad — the ONLY way to enter an amount, count or PIN on the
 * kiosk (it has no soft keyboard; docs/pos-ux-spec.md §2 ilke 6). Controlled:
 * `value` is the string being edited and `onChange` receives the next one; all
 * key semantics live in @onlinemenu/pos-core's numpad.ts. Keys are 56px, the 8px grid gap keeps
 * neighbours from being mis-hit.
 */
export function Numpad({ value, onChange, mode, maxLength, pendingReplace, disabled = false }: NumpadProps) {
  function press(key: NumpadKey) {
    onChange(applyNumpadKey(value, key, { mode, maxLength, pendingReplace }))
  }

  return (
    <div className="grid grid-cols-4 gap-2" role="group" aria-label="Sayı tuş takımı">
      {ROWS.flat().map((spec) => {
        const enabled = isNumpadKeyEnabled(mode, spec.key)
        if (!enabled) return <span key={spec.key} aria-hidden="true" />
        return (
          <button
            key={spec.key}
            type="button"
            disabled={disabled}
            aria-label={spec.ariaLabel}
            onClick={() => press(spec.key)}
            className={`${KEY_CLASS} ${spec.wide ? 'col-span-3' : ''}`}
          >
            {spec.label}
          </button>
        )
      })}
    </div>
  )
}
