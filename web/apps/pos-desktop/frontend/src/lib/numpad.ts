// Pure key-press logic behind components/Numpad.tsx. The kiosk has no soft
// keyboard (docs/pos-ux-spec.md §2 ilke 6), so every numeric entry — amount,
// count, PIN — goes through this one state machine; the component is just a
// key grid over it. Kept in lib/ so it is testable without a DOM.
//
// Entry model for money is "whole lira by default, comma for kuruş" (typing
// 2-0-0 is ₺200, never ₺2,00): it is how a Turkish cashier reads and says
// amounts, and it makes a mistyped-by-two-orders-of-magnitude total
// impossible. The value stays a plain string ("12,5") so the caller converts
// once, at the edge, with parseMoneyInputToKurus (format.ts).

export type NumpadDigit = '0' | '1' | '2' | '3' | '4' | '5' | '6' | '7' | '8' | '9'
export type NumpadKey = NumpadDigit | '00' | ',' | 'backspace'

/** money: whole lira + optional ,kuruş. integer: counts. pin: digits only, leading zeros kept. */
export type NumpadMode = 'money' | 'integer' | 'pin'

export type NumpadOptions = {
  mode: NumpadMode
  /** Digit cap for integer/pin. Money has a fixed cap (7 whole-lira digits, 2 kuruş digits). */
  maxLength?: number
  /**
   * The field was just filled by a preset chip (₺100, "2'ye böl", …): the next
   * value-adding key starts a fresh entry instead of appending to the preset.
   * Backspace is exempt — it edits the preset, it does not clear it (clearing
   * would turn "₺200, oops" into an empty field, which the payment screen
   * reads as "the whole remaining balance").
   */
  pendingReplace?: boolean
}

const MONEY_MAX_INT_DIGITS = 7
const MONEY_DECIMALS = 2
const DEFAULT_MAX_LENGTH: Record<'integer' | 'pin', number> = { integer: 4, pin: 6 }

export function isNumpadKeyEnabled(mode: NumpadMode, key: NumpadKey): boolean {
  if (key === ',') return mode === 'money'
  if (key === '00') return mode !== 'pin'
  return true
}

export function applyNumpadKey(current: string, key: NumpadKey, options: NumpadOptions): string {
  const { mode } = options
  if (key === 'backspace') return current.slice(0, -1)
  if (!isNumpadKeyEnabled(mode, key)) return current
  const value = options.pendingReplace ? '' : current

  if (key === ',') {
    if (value.includes(',')) return value
    return `${value === '' ? '0' : value},`
  }

  if (mode === 'money') return applyMoneyDigits(value, key)

  const max = options.maxLength ?? DEFAULT_MAX_LENGTH[mode]
  // A count of "007" is just 7; a PIN of "007" is a different PIN.
  if (mode === 'integer' && (value === '' || value === '0')) return key === '00' ? '0' : key
  const next = value + key
  return next.length > max ? value : next
}

function applyMoneyDigits(value: string, key: NumpadDigit | '00'): string {
  const commaAt = value.indexOf(',')

  if (commaAt >= 0) {
    const decimals = value.length - commaAt - 1
    if (decimals >= MONEY_DECIMALS) return value
    if (key === '00') return value + '0'.repeat(MONEY_DECIMALS - decimals)
    return value + key
  }

  if (value === '' || value === '0') return key === '00' ? '0' : key
  const next = value + key
  return next.length > MONEY_MAX_INT_DIGITS ? value : next
}

/** Kuruş -> the "1234,56" shape the numpad edits (and parseMoneyInputToKurus reads). */
export function kurusToMoneyInput(kurus: number): string {
  return (kurus / 100).toFixed(2).replace('.', ',')
}

/** Adds thousands dots for reading only — the edited value itself never contains them. */
export function formatMoneyInputDisplay(value: string): string {
  if (value === '') return '0'
  const [whole, decimals] = value.split(',')
  const grouped = whole.replace(/\B(?=(\d{3})+(?!\d))/g, '.')
  return decimals === undefined ? grouped : `${grouped},${decimals}`
}
