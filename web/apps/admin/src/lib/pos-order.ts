// Pure glue between the admin's REST/axios world and @onlinemenu/pos-core's
// order-taking rules (web order screen, /pos/order). The rules themselves —
// cart merging, option-group validation, unit price, error wording — live in
// pos-core and are NOT restated here; this file only adapts shapes.
import axios from "axios"

import {
  describeError,
  errorCode,
  requiredCount,
  type ApiErrorCode,
  type ModifierGroupSource,
  type OrderItemInput,
} from "@onlinemenu/pos-core"

import type { Product } from "@/types"

// ---------------------------------------------------------------------------
// Errors
// ---------------------------------------------------------------------------

/**
 * pos-core's describeError/errorCode read the Go client's flattened error text
 * ("... unexpected status 409: {"code":"..."}"). An AxiosError stringifies to
 * "AxiosError: Request failed with status code 409" — no body, and not the
 * "status 409" substring either — so it is rebuilt into that shape here. The
 * backend answers both with JSON {code} bodies and with plain-text
 * http.Error() bodies, hence the string/JSON split.
 */
export function toPosCoreErrorText(err: unknown): string {
  if (axios.isAxiosError(err) && err.response) {
    const { status, data } = err.response
    const body = typeof data === "string" ? data : JSON.stringify(data ?? "")
    return `status ${status}: ${body}`
  }
  return String(err)
}

export type OrderErrorKind = "network" | "price" | "occupied" | "not_open" | "other"

export interface OrderError {
  kind: OrderErrorKind
  code: ApiErrorCode | null
  message: string
}

// No response at all (offline, timeout, DNS): the request may or may not have
// reached the server, so the cart must stay and the same Idempotency-Key must
// be reused by "Tekrar dene". describeError would echo the raw English axios
// text here, which a waiter cannot act on.
const NETWORK_MESSAGE = "Bağlantı kurulamadı. Sepetiniz duruyor."
const SERVER_MESSAGE = "Sunucu yanıt veremedi. Sepetiniz duruyor."
// pos-core's table_occupied text is written for the counter ("…birleştir"),
// an action a waiter does not have. On the waiter screen the retry lands the
// round on the table's existing check, so that is what the message promises.
const OCCUPIED_MESSAGE = "Bu masada açık adisyon var, siparişiniz ona eklenecek. “Tekrar dene”ye dokunun."
const PRICE_MESSAGE = "Fiyat değişmiş. Ürünü sepetten silip yeniden ekleyin; fiyatlar yenilendi."

export function describeOrderError(err: unknown): OrderError {
  if (axios.isAxiosError(err) && !err.response) {
    return { kind: "network", code: null, message: NETWORK_MESSAGE }
  }
  const text = toPosCoreErrorText(err)
  const code = errorCode(text)
  if (code === "price_mismatch") return { kind: "price", code, message: PRICE_MESSAGE }
  if (code === "table_occupied") return { kind: "occupied", code, message: OCCUPIED_MESSAGE }
  if (code === "check_not_open") return { kind: "not_open", code, message: describeError(text) }
  if (axios.isAxiosError(err) && (err.response?.status ?? 0) >= 500) {
    return { kind: "network", code, message: SERVER_MESSAGE }
  }
  return { kind: "other", code, message: describeError(text) }
}

// Web has no payment screen (payments are taken on the POS desktop app), so
// an unpaid check's "Kapat" must say where to go, not just that it failed.
const UNPAID_HINT = "Ödemeyi kasadaki POS uygulamasından alın."
// Not in pos-core's code table (the desktop client never cancels a paid check).
const HAS_PAYMENTS_MESSAGE = "Ödemesi alınmış adisyon iptal edilemez — önce ödemeyi kasadan iade edin."

/** Message for a failed adisyon "Kapat" / "İptal" on the web panel. */
export function describeCheckActionError(err: unknown): string {
  if (axios.isAxiosError(err) && !err.response) return "Bağlantı kurulamadı. Tekrar deneyin."
  const text = toPosCoreErrorText(err)
  if (/"code"\s*:\s*"check_has_payments"/.test(text)) return HAS_PAYMENTS_MESSAGE
  const message = describeError(text)
  return errorCode(text) === "insufficient_payment" ? `${message} ${UNPAID_HINT}` : message
}

/**
 * Worth a "Tekrar dene": a lost response (same Idempotency-Key replays it) or
 * a table somebody else just opened (the retry reads the fresh floor plan and
 * adds the round to that check).
 */
export function isRetryable(error: OrderError): boolean {
  return error.kind === "network" || error.kind === "occupied"
}

// ---------------------------------------------------------------------------
// Idempotency-Key lifecycle (ADR-SEC-003)
// ---------------------------------------------------------------------------

export interface SubmissionKey {
  key: string
  signature: string
}

/**
 * One key per logical order: the same cart sent to the same check reuses the
 * key (a retry after a timeout that actually committed must not place the
 * order twice), anything else gets a fresh one (the server answers 422 when a
 * key is replayed with a different body). Callers drop the key on success.
 */
export function submissionKeyFor(
  previous: SubmissionKey | null,
  signature: string,
  mint: () => string,
): SubmissionKey {
  if (previous && previous.signature === signature) return previous
  return { key: mint(), signature }
}

/**
 * crypto.randomUUID only exists in secure contexts; a waiter's phone on the
 * restaurant LAN over plain http (a dev/pilot setup) would not have it.
 */
export function newIdempotencyKey(): string {
  const c = globalThis.crypto
  if (typeof c?.randomUUID === "function") return c.randomUUID()
  const bytes = new Uint8Array(16)
  c.getRandomValues(bytes)
  bytes[6] = (bytes[6] & 0x0f) | 0x40
  bytes[8] = (bytes[8] & 0x3f) | 0x80
  const hex = Array.from(bytes, (b) => b.toString(16).padStart(2, "0")).join("")
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`
}

export function orderSignature(checkId: string, items: readonly OrderItemInput[]): string {
  return JSON.stringify([checkId, items])
}

export interface PlaceOrderBody {
  branch_id: string
  check_id: string
  order_channel: "dine_in"
  items: OrderItemInput[]
}

export function placeOrderBody(branchId: string, checkId: string, items: OrderItemInput[]): PlaceOrderBody {
  return { branch_id: branchId, check_id: checkId, order_channel: "dine_in", items }
}

// ---------------------------------------------------------------------------
// Catalog normalisation
// ---------------------------------------------------------------------------

const bySortOrder = <T extends { sort_order: number; name: string }>(a: T, b: T) =>
  a.sort_order - b.sort_order || a.name.localeCompare(b.name, "tr")

/** Only what the server will sell: inactive products answer 422 invalid_order_line. */
export function sellableProducts(products: readonly Product[]): Product[] {
  return products.filter((p) => p.is_active).sort(bySortOrder)
}

/** Wire shape of GET /catalog/products/modifier-groups (one entry per product that has groups). */
export interface ProductOptionsWire {
  product_id: string
  groups: {
    id: string
    name: string
    selection_type: string
    min_selections: number
    /** null = no cap */
    max_selections: number | null
    is_required: boolean
    sort_order: number
    /** Active options only; empty when every option of the group is switched off. */
    modifiers: { id: string; name: string; price_delta: number; sort_order: number }[]
  }[]
}

export type OptionLookup = { kind: "ready"; groups: ModifierGroupSource[] } | { kind: "unavailable" }

/**
 * One product's groups from the option tree, in the server's (assignment)
 * order, in pos-core's shape. A product absent from the tree has no options.
 *
 * - `max_selections: null` ("no cap") becomes pos-core's 0.
 * - An empty optional group is dropped (nothing to offer).
 * - An empty REQUIRED group makes the product "unavailable": nobody can answer
 *   it, so the product is added plain and the line tells the waiter to say it
 *   aloud — the same rule pos-desktop applies (options.go buildGroups).
 */
export function optionGroupsFrom(groups: ProductOptionsWire["groups"] | undefined): OptionLookup {
  const out: ModifierGroupSource[] = []
  for (const group of groups ?? []) {
    const source: ModifierGroupSource = {
      id: group.id,
      name: group.name,
      selection_type: group.selection_type,
      min_selections: group.min_selections,
      max_selections: group.max_selections ?? 0,
      is_required: group.is_required,
      sort_order: group.sort_order,
      modifiers: group.modifiers.map((m) => ({ id: m.id, name: m.name, price_delta: m.price_delta, sort_order: m.sort_order })),
    }
    if (source.modifiers.length === 0) {
      if (requiredCount(source) > 0) return { kind: "unavailable" }
      continue
    }
    out.push(source)
  }
  return { kind: "ready", groups: out }
}

// ---------------------------------------------------------------------------
// Feedback
// ---------------------------------------------------------------------------

/** Short haptic tick where supported (Android Chrome); iOS Safari has no vibrate. */
export function tapHaptic(ms = 12): void {
  if (typeof navigator === "undefined" || typeof navigator.vibrate !== "function") return
  if (typeof window !== "undefined" && window.matchMedia?.("(prefers-reduced-motion: reduce)").matches) return
  try {
    navigator.vibrate(ms)
  } catch {
    // Some browsers throw when vibration is blocked by a permissions policy.
  }
}
