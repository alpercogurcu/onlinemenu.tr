// Cash rounding (beşli yuvarlama, kasa-rapor-programi G.2): the branch grants
// permission and a ceiling, the cashier rounds the final remainder DOWN with
// one tap. The backend re-checks every rule on the payment (422
// rounding_not_allowed); these helpers only decide what to offer and keep the
// screen's arithmetic in one testable place.
//
// All amounts are integers in kuruş.

import type { PayMethod } from './paymentPlan'

export type RoundingPolicy = {
  cashEnabled: boolean
  cardEnabled: boolean
  /** 50 / 100 / 500 / 1000 kuruş. */
  stepMinor: number
  /** Most a single check may be conceded in total. */
  maxPerCheckMinor: number
}

/** A branch that does not round — the backend default, and the fallback when the policy cannot be read. */
export const ROUNDING_OFF: RoundingPolicy = { cashEnabled: false, cardEnabled: false, stepMinor: 500, maxPerCheckMinor: 1000 }

export function roundingEnabledFor(policy: RoundingPolicy, method: PayMethod): boolean {
  return method === 'cash' ? policy.cashEnabled : policy.cardEnabled
}

export type RoundingOffer = {
  /** What the customer pays. */
  rounded: number
  /** What is conceded: due − rounded, always below the step. */
  rounding: number
}

export type RoundingOfferInput = {
  /** What this payment would settle without rounding. */
  due: number
  /** What the check still owes. */
  remaining: number
  policy: RoundingPolicy
  /** Rounding already granted on this check by earlier payments. */
  conceded: number
}

/**
 * The rounding the screen may offer, or null. Mirrors the server rules: only a
 * payment that closes the whole remainder rounds, never below one step,
 * never past the per-check ceiling, and only when some method may round.
 */
export function roundingOffer({ due, remaining, policy, conceded }: RoundingOfferInput): RoundingOffer | null {
  if (!policy.cashEnabled && !policy.cardEnabled) return null
  const step = policy.stepMinor
  if (!Number.isInteger(step) || step <= 0) return null
  if (due <= 0 || due !== remaining) return null
  const rounded = Math.floor(due / step) * step
  const rounding = due - rounded
  if (rounding <= 0 || rounded <= 0) return null
  if (conceded + rounding > policy.maxPerCheckMinor) return null
  return { rounded, rounding }
}

export type Charge = {
  /** Money taken by this payment. */
  amount: number
  /** Rounding conceded on top of it (0 when none). */
  rounding: number
}

/**
 * What pressing a method's button charges. The method is chosen by the button
 * itself, so an applied rounding only reaches a method whose branch switch is
 * on — the other button still takes the full amount.
 */
export function chargeFor(method: PayMethod, due: number, offer: RoundingOffer | null, policy: RoundingPolicy): Charge {
  if (offer && roundingEnabledFor(policy, method)) return { amount: offer.rounded, rounding: offer.rounding }
  return { amount: due, rounding: 0 }
}

/** What a payment takes off its check: the money taken plus any rounding conceded. */
export function settledAmount(payment: { amount_total: number; rounding_amount?: number | null }): number {
  return payment.amount_total + (payment.rounding_amount ?? 0)
}
