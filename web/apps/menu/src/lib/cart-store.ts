import { create } from "zustand"
import { persist } from "zustand/middleware"

import type { MenuModifier, MenuProduct, PlaceOrderRequest } from "@/types/storefront"

export interface CartModifier {
  id: string
  name: string
  priceDelta: number
}

export interface CartLine {
  /** Identity of a line: same product + same modifier set + same note merge. */
  key: string
  productId: string
  productName: string
  /** Menu price in kuruş, for display only — the server re-prices the order. */
  unitPriceAmount: number
  modifiers: CartModifier[]
  note: string
  quantity: number
}

interface CartState {
  lines: CartLine[]
  orderNote: string
  /**
   * Identifies the guest session the persisted cart belongs to.
   *
   * The cart lives in localStorage so an accidental reload does not lose it —
   * but localStorage outlives the session, and the next diner to scan a QR on
   * the same phone (or the same diner at a different table) must not inherit
   * it. `syncSession` compares this against the current session and wipes the
   * cart when they differ.
   */
  sessionKey: string | null
  /**
   * The Idempotency-Key of the submission currently in flight.
   *
   * It is created ONCE, when the diner taps "order", and lives here rather
   * than inside the mutation: generating it in mutationFn would give every
   * retry a fresh key, and a retried timeout would then place a second real
   * order. It is persisted for the same reason — a reload mid-submit must
   * reuse the key so the server replays instead of duplicating.
   */
  idempotencyKey: string | null

  addLine: (product: MenuProduct, modifiers: MenuModifier[], note: string, quantity: number) => void
  setQuantity: (key: string, quantity: number) => void
  removeLine: (key: string) => void
  setOrderNote: (note: string) => void
  clear: () => void
  syncSession: (sessionKey: string | null) => void

  beginSubmission: () => string
  endSubmission: () => void
}

function lineKey(productId: string, modifierIds: string[], note: string): string {
  return [productId, [...modifierIds].sort().join(","), note.trim()].join("|")
}

export function newIdempotencyKey(): string {
  return crypto.randomUUID()
}

export const useCartStore = create<CartState>()(
  persist(
    (set, get) => ({
      lines: [],
      orderNote: "",
      sessionKey: null,
      idempotencyKey: null,

      addLine: (product, modifiers, note, quantity) => {
        const cartModifiers: CartModifier[] = modifiers.map((m) => ({
          id: m.id,
          name: m.name,
          priceDelta: m.price_delta,
        }))
        const key = lineKey(
          product.id,
          cartModifiers.map((m) => m.id),
          note,
        )
        const existing = get().lines.find((l) => l.key === key)
        if (existing) {
          set({
            lines: get().lines.map((l) =>
              l.key === key ? { ...l, quantity: clampQuantity(l.quantity + quantity) } : l,
            ),
            // An Idempotency-Key identifies ONE exact body. Once the cart
            // changes, replaying the old key against the new body is a
            // guaranteed 422 the diner can never clear by retrying — so every
            // edit burns the key and the next submission starts a new one.
            idempotencyKey: null,
          })
          return
        }
        set({
          lines: [
            ...get().lines,
            {
              key,
              productId: product.id,
              productName: product.name,
              unitPriceAmount: product.price_amount,
              modifiers: cartModifiers,
              note: note.trim(),
              quantity: clampQuantity(quantity),
            },
          ],
          idempotencyKey: null,
        })
      },

      setQuantity: (key, quantity) => {
        if (quantity <= 0) {
          set({ lines: get().lines.filter((l) => l.key !== key), idempotencyKey: null })
          return
        }
        set({
          lines: get().lines.map((l) =>
            l.key === key ? { ...l, quantity: clampQuantity(quantity) } : l,
          ),
          idempotencyKey: null,
        })
      },

      removeLine: (key) =>
        set({ lines: get().lines.filter((l) => l.key !== key), idempotencyKey: null }),

      setOrderNote: (note) => set({ orderNote: note, idempotencyKey: null }),

      clear: () => set({ lines: [], orderNote: "", idempotencyKey: null }),

      syncSession: (sessionKey) => {
        // null means "the session cookie has not been read yet", NOT "a new
        // session". Treating it as a new session wiped the persisted cart on
        // every full page load, which is precisely what persisting it is for.
        if (sessionKey === null || get().sessionKey === sessionKey) return
        set({ lines: [], orderNote: "", idempotencyKey: null, sessionKey })
      },

      beginSubmission: () => {
        const existing = get().idempotencyKey
        if (existing !== null) return existing
        const key = newIdempotencyKey()
        set({ idempotencyKey: key })
        return key
      },

      endSubmission: () => set({ idempotencyKey: null }),
    }),
    {
      name: "onlinemenu-cart",
      version: 1,
      // Hydration is driven by AppShell, not by the store's own module load.
      //
      // Two reasons, both observable:
      //   * automatic rehydration happens during the client's first render, so
      //     the server HTML (always an empty cart) and the first client render
      //     (a restored cart) disagree — a hydration mismatch on /menu and
      //     /cart, which are server-rendered per request;
      //   * the cart may belong to a PREVIOUS guest session, and that can only
      //     be decided after the session cookie has been read on the client.
      // hydrateCartForSession below orders the two correctly.
      skipHydration: true,
    },
  ),
)

let rehydrateStarted = false

/**
 * Restores the persisted cart, then decides whether it still belongs to this
 * diner. Order matters: syncing first and rehydrating second would restore a
 * cart the session check had just discarded.
 */
export async function hydrateCartForSession(sessionKey: string): Promise<void> {
  if (!rehydrateStarted) {
    rehydrateStarted = true
    await useCartStore.persist.rehydrate()
  }
  useCartStore.getState().syncSession(sessionKey)
}

// The API caps a line at 99 (storefront/http/public_handler.go maxLineQuantity);
// clamping here keeps the UI from ever building a cart the server must reject.
export const MAX_LINE_QUANTITY = 99
export const MAX_CART_LINES = 60

function clampQuantity(quantity: number): number {
  return Math.min(Math.max(Math.trunc(quantity), 1), MAX_LINE_QUANTITY)
}

export function lineTotal(line: CartLine): number {
  const unit = line.unitPriceAmount + line.modifiers.reduce((sum, m) => sum + m.priceDelta, 0)
  return unit * line.quantity
}

/** Indicative total, in kuruş. The authoritative total comes back from the API. */
export function cartTotal(lines: CartLine[]): number {
  return lines.reduce((sum, line) => sum + lineTotal(line), 0)
}

export function cartItemCount(lines: CartLine[]): number {
  return lines.reduce((sum, line) => sum + line.quantity, 0)
}

export function toPlaceOrderRequest(lines: CartLine[], orderNote: string): PlaceOrderRequest {
  return {
    lines: lines.map((line) => ({
      product_id: line.productId,
      quantity: line.quantity,
      modifier_ids: line.modifiers.map((m) => m.id),
      note: line.note,
    })),
    note: orderNote.trim(),
  }
}
