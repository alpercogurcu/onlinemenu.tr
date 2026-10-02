// How one receipt-rail row reads while the payment screen is open
// (docs/plans/2026-10-02-kasa-rapor-programi.md G.1): the rail is the item
// selection surface, so each row says what is already paid, what is still
// open, and how many of the open units the next payment takes. Pure so the
// visible copy is testable in the node vitest environment.

export type PayRowState = {
  /** Units still open for an item payment. */
  available: number
  /** Open units picked for the next payment (never more than `available`). */
  selected: number
  /** Every unit already paid: the row is dimmed and not tappable. */
  settled: boolean
  /** Some but not all units paid: "1 ödendi, 1 kalan". Empty otherwise. */
  partialLabel: string
  /** Multi-unit rows show the stepper; a single unit just toggles. */
  stepper: boolean
}

export const PAID_ROW_LABEL = 'Ödendi'

export function payRowState(quantity: number, paid: number, selected: number): PayRowState {
  const paidUnits = Math.max(0, Math.min(quantity, paid))
  const available = quantity - paidUnits
  return {
    available,
    selected: Math.max(0, Math.min(selected, available)),
    settled: available === 0,
    partialLabel: paidUnits > 0 && available > 0 ? `${paidUnits} ödendi, ${available} kalan` : '',
    stepper: available > 1,
  }
}
