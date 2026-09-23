// Pure arithmetic for split/partial cash payments — kept separate from any
// one component/screen so the rules are readable and testable in one place,
// rather than inline in a component.
//
// All amounts are integers in kuruş (see format.ts's file-level comment).
//
// NOTE: this file predates a test runner being wired up for it (it was moved
// here, into @onlinemenu/pos-core, from pos-desktop's lib/, where it shipped
// with no automated test — see git history). It has no dedicated test file of
// its own yet; add one if this arithmetic needs enforced regression coverage.

/** Outstanding balance still owed on a check, never negative (a check that
 * was ever overpaid, e.g. by a pre-fix client, must not show a negative
 * "kalan"). */
export function remainingBalance(checkTotal: number, paidSoFar: number): number {
  return Math.max(0, checkTotal - paidSoFar)
}

/**
 * The amount actually sent to RegisterCashPayment for one cash-payment
 * step. Never more than what is still owed — a cashier entering more than
 * the remaining balance (to make change) must not have the excess
 * registered as a payment; only the change is given back in cash, the
 * system only ever records up to `remaining` (see changeDue below for the
 * excess).
 */
export function clampToRemaining(enteredKurus: number, remaining: number): number {
  return Math.max(0, Math.min(enteredKurus, remaining))
}

/**
 * Change owed back to the customer for one cash-payment step — zero for
 * every partial installment except (optionally) the final one, where the
 * cashier may enter more than the remaining balance.
 */
export function changeDue(enteredKurus: number, remaining: number): number {
  return Math.max(0, enteredKurus - remaining)
}

/**
 * Suggested amount (in kuruş) for one of `parts` equal-ish shares of the
 * remaining balance — rounded UP so `parts` consecutive equal payments
 * never fall short of covering `remaining` (the last share may be a few
 * kuruş smaller than the others rather than larger). Purely a starting
 * suggestion for the input field — the cashier can edit it before
 * confirming.
 */
export function splitSuggestion(remaining: number, parts: number): number {
  if (parts <= 0) return remaining
  return Math.ceil(remaining / parts)
}
