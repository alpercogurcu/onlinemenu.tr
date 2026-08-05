import { beforeEach, describe, expect, it } from "vitest"

import {
  MAX_LINE_QUANTITY,
  cartItemCount,
  cartTotal,
  toPlaceOrderRequest,
  useCartStore,
} from "@/lib/cart-store"
import type { MenuModifier, MenuProduct } from "@/types/storefront"

const latte: MenuProduct = {
  id: "p1",
  name: "Latte",
  description: "",
  price_amount: 4500,
  currency: "TRY",
  image_key: "",
  allergens: [],
  is_available: true,
  modifier_groups: [],
}

const extraShot: MenuModifier = { id: "m1", name: "Ekstra shot", price_delta: 500 }

beforeEach(() => {
  useCartStore.setState({
    lines: [],
    orderNote: "",
    sessionKey: null,
    idempotencyKey: null,
  })
})

describe("addLine", () => {
  it("merges lines with the same product, modifiers and note", () => {
    const { addLine } = useCartStore.getState()
    addLine(latte, [extraShot], "az şekerli", 1)
    addLine(latte, [extraShot], "az şekerli", 2)

    const { lines } = useCartStore.getState()
    expect(lines).toHaveLength(1)
    expect(lines[0].quantity).toBe(3)
  })

  it("keeps lines with different modifiers apart", () => {
    const { addLine } = useCartStore.getState()
    addLine(latte, [extraShot], "", 1)
    addLine(latte, [], "", 1)
    expect(useCartStore.getState().lines).toHaveLength(2)
  })

  it("merges regardless of the order the modifiers were tapped in", () => {
    const second: MenuModifier = { id: "m2", name: "Vanilya", price_delta: 250 }
    const { addLine } = useCartStore.getState()
    addLine(latte, [extraShot, second], "", 1)
    addLine(latte, [second, extraShot], "", 1)
    expect(useCartStore.getState().lines).toHaveLength(1)
  })

  it("clamps quantity to the API's per-line maximum", () => {
    const { addLine } = useCartStore.getState()
    addLine(latte, [], "", 500)
    expect(useCartStore.getState().lines[0].quantity).toBe(MAX_LINE_QUANTITY)
  })
})

describe("totals", () => {
  it("adds modifier deltas into the unit price, in integer kuruş", () => {
    const { addLine } = useCartStore.getState()
    addLine(latte, [extraShot], "", 2)
    const { lines } = useCartStore.getState()
    expect(cartTotal(lines)).toBe((4500 + 500) * 2)
    expect(cartItemCount(lines)).toBe(2)
  })
})

describe("idempotency key lifecycle", () => {
  // ADR-SEC-003: one key per submission, reused by every retry. A key created
  // per attempt turns a timed-out request into two real orders.
  it("returns the same key for repeated begins", () => {
    const { beginSubmission } = useCartStore.getState()
    const first = beginSubmission()
    const second = useCartStore.getState().beginSubmission()
    expect(second).toBe(first)
  })

  it("issues a fresh key after the submission ends", () => {
    const first = useCartStore.getState().beginSubmission()
    useCartStore.getState().endSubmission()
    const second = useCartStore.getState().beginSubmission()
    expect(second).not.toBe(first)
  })

  it("drops the key when the cart is cleared", () => {
    useCartStore.getState().beginSubmission()
    useCartStore.getState().clear()
    expect(useCartStore.getState().idempotencyKey).toBeNull()
  })

  // A key identifies ONE exact body. Keeping it across an edit means every
  // later attempt hits "same key, different body" => 422, forever, with an
  // error message about the cart contents that the diner cannot act on.
  it.each([
    ["addLine", () => useCartStore.getState().addLine(latte, [], "", 1)],
    ["setQuantity", () => useCartStore.getState().setQuantity(seededLineKey(), 5)],
    ["removeLine", () => useCartStore.getState().removeLine(seededLineKey())],
    ["setOrderNote", () => useCartStore.getState().setOrderNote("acele")],
  ])("burns the key when the cart is edited via %s", (_name, edit) => {
    useCartStore.getState().addLine(latte, [], "", 1)
    useCartStore.getState().beginSubmission()
    expect(useCartStore.getState().idempotencyKey).not.toBeNull()

    edit()
    expect(useCartStore.getState().idempotencyKey).toBeNull()
  })
})

function seededLineKey(): string {
  return useCartStore.getState().lines[0].key
}

describe("syncSession", () => {
  // The cart is persisted in localStorage, which outlives a guest session.
  it("wipes a cart that belongs to a previous session", () => {
    useCartStore.getState().syncSession("session-a")
    useCartStore.getState().addLine(latte, [], "", 1)

    useCartStore.getState().syncSession("session-b")
    expect(useCartStore.getState().lines).toHaveLength(0)
  })

  it("keeps the cart within the same session", () => {
    useCartStore.getState().syncSession("session-a")
    useCartStore.getState().addLine(latte, [], "", 1)

    useCartStore.getState().syncSession("session-a")
    expect(useCartStore.getState().lines).toHaveLength(1)
  })

  // The regression this exists for: useTableSession returns null until the
  // cookie has been read on the client, and a passive effect fires with that
  // null first. Treating it as "a different session" emptied the persisted
  // cart on every full page reload — exactly what persisting it prevents.
  it("keeps the cart when the session is not known yet", () => {
    useCartStore.getState().syncSession("session-a")
    useCartStore.getState().addLine(latte, [], "", 1)

    useCartStore.getState().syncSession(null)
    expect(useCartStore.getState().lines).toHaveLength(1)
    expect(useCartStore.getState().sessionKey).toBe("session-a")
  })
})

describe("toPlaceOrderRequest", () => {
  it("sends no price field of any kind", () => {
    const { addLine } = useCartStore.getState()
    addLine(latte, [extraShot], "az şekerli", 2)

    const body = toPlaceOrderRequest(useCartStore.getState().lines, " servis 10 dk sonra ")
    expect(body).toEqual({
      lines: [
        {
          product_id: "p1",
          quantity: 2,
          modifier_ids: ["m1"],
          note: "az şekerli",
        },
      ],
      note: "servis 10 dk sonra",
    })
    // The server re-derives every amount; a price here would be ignored at
    // best and is not part of the contract at all.
    expect(JSON.stringify(body)).not.toContain("price")
  })
})
