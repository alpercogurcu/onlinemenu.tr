// Pure rules behind the web order screen (lib/pos-order.ts): the axios ->
// pos-core error adapter, the Idempotency-Key lifecycle, the order body and the
// catalog normalisation handed to pos-core's option rules.
import { AxiosError, AxiosHeaders, type AxiosResponse } from "axios"
import { describe, expect, it } from "vitest"

import { addProductToPending, toOrderItemInputs } from "@onlinemenu/pos-core"

import {
  describeOrderError,
  isRetryable,
  newIdempotencyKey,
  optionGroupsFrom,
  orderSignature,
  placeOrderBody,
  sellableProducts,
  submissionKeyFor,
  toPosCoreErrorText,
  type ProductOptionsWire,
} from "@/lib/pos-order"
import type { Product } from "@/types"

function httpError(status: number, data: unknown): AxiosError {
  const config = { headers: new AxiosHeaders() }
  const response = { status, data, statusText: "", headers: {}, config } as AxiosResponse
  return new AxiosError(`Request failed with status code ${status}`, "ERR_BAD_RESPONSE", config, {}, response)
}

function networkError(): AxiosError {
  return new AxiosError("Network Error", "ERR_NETWORK", { headers: new AxiosHeaders() }, {})
}

describe("error adapter", () => {
  it("rebuilds the status + body text pos-core parses (JSON and plain-text bodies)", () => {
    expect(toPosCoreErrorText(httpError(409, { error: "x", code: "table_occupied" }))).toBe(
      'status 409: {"error":"x","code":"table_occupied"}',
    )
    expect(toPosCoreErrorText(httpError(422, "items is required\n"))).toBe("status 422: items is required\n")
  })

  it("price_mismatch -> Turkish price message", () => {
    const e = describeOrderError(httpError(422, { error: "price mismatch", code: "price_mismatch" }))
    expect(e.kind).toBe("price")
    expect(e.message).toMatch(/Fiyat değişmiş/)
    expect(isRetryable(e)).toBe(false)
  })

  it("no response (offline/timeout) -> retryable, cart-keeping message", () => {
    const e = describeOrderError(networkError())
    expect(e.kind).toBe("network")
    expect(e.message).toMatch(/Sepetiniz duruyor/)
    expect(isRetryable(e)).toBe(true)
  })

  it("5xx is retryable under the same key", () => {
    expect(isRetryable(describeOrderError(httpError(502, "bad gateway")))).toBe(true)
  })

  it("table_occupied speaks to the waiter (no 'birleştir'), and a retry joins that check", () => {
    const occupied = describeOrderError(httpError(409, { code: "table_occupied" }))
    expect(occupied.kind).toBe("occupied")
    expect(occupied.message).toMatch(/^Bu masada açık adisyon var, siparişiniz ona eklenecek\./)
    expect(occupied.message).not.toMatch(/birleştir/i)
    expect(isRetryable(occupied)).toBe(true)
  })

  it("coded 409s map through pos-core describeError", () => {
    const notOpen = describeOrderError(httpError(409, { code: "check_not_open" }))
    expect(notOpen.kind).toBe("not_open")
    expect(notOpen.message).toMatch(/artık açık değil/)
  })

  it("a code-less 403 gets pos-core's permission wording, never raw English", () => {
    const e = describeOrderError(httpError(403, "forbidden"))
    expect(e.kind).toBe("other")
    expect(e.message).toMatch(/yetkiniz yok/)
  })
})

describe("Idempotency-Key lifecycle", () => {
  let n = 0
  const mint = () => `key-${++n}`

  it("reuses the key for the same cart on the same check (retry)", () => {
    const first = submissionKeyFor(null, orderSignature("c1", []), mint)
    const retry = submissionKeyFor(first, orderSignature("c1", []), mint)
    expect(retry.key).toBe(first.key)
  })

  it("mints a new key after the cart changed", () => {
    const product = { id: "p", name: "Ayran", price_amount: 4000, currency: "TRY", tax_rate_bps: 1000, unit: "adet" }
    const one = toOrderItemInputs(addProductToPending([], product))
    const two = toOrderItemInputs(addProductToPending(addProductToPending([], product), product))
    const first = submissionKeyFor(null, orderSignature("c1", one), mint)
    expect(submissionKeyFor(first, orderSignature("c1", two), mint).key).not.toBe(first.key)
    expect(submissionKeyFor(first, orderSignature("c2", one), mint).key).not.toBe(first.key)
  })

  it("mints RFC 4122 v4 keys", () => {
    expect(newIdempotencyKey()).toMatch(/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/)
  })
})

describe("order body", () => {
  it("sends modifier_ids and unit price = base + Σ delta, dine_in on the given branch/check", () => {
    const kebap = { id: "p1", name: "Adana", price_amount: 32_000, currency: "TRY", tax_rate_bps: 1000, unit: "adet" }
    const lines = addProductToPending([], kebap, {
      modifiers: [
        { id: "m-orta", groupId: "g1", name: "Orta", priceDelta: 0 },
        { id: "m-sos", groupId: "g2", name: "Ekstra sos", priceDelta: 1_500 },
      ],
      note: "Soğansız",
      quantity: 2,
    })
    const body = placeOrderBody("b1", "c1", toOrderItemInputs(lines))
    expect(body).toMatchObject({ branch_id: "b1", check_id: "c1", order_channel: "dine_in" })
    expect(body.items[0]).toMatchObject({
      product_id: "p1",
      quantity: 2,
      product_price_amount: 32_000,
      unit_price_amount: 33_500,
      modifier_ids: ["m-orta", "m-sos"],
      note: "Orta | Ekstra sos(+15) | Soğansız",
    })
  })
})

const stamp = { tenant_id: "t", created_at: "", updated_at: "" }

type WireGroup = ProductOptionsWire["groups"][number]

function wireGroup(overrides: Partial<WireGroup>): WireGroup {
  return {
    id: "g",
    name: "G",
    selection_type: "single",
    min_selections: 1,
    max_selections: 1,
    is_required: true,
    sort_order: 0,
    modifiers: [{ id: "m", name: "M", price_delta: 0, sort_order: 0 }],
    ...overrides,
  }
}

describe("catalog normalisation", () => {
  it("keeps only active products, in sort order", () => {
    const base = { ...stamp, branch_id: null, category_id: null, description: "", image_key: "", currency: "TRY", sku: "", unit: "adet", tax_rate_bps: 1000, price_amount: 1 }
    const out = sellableProducts([
      { ...base, id: "b", name: "B", sort_order: 2, is_active: true },
      { ...base, id: "x", name: "X", sort_order: 0, is_active: false },
      { ...base, id: "a", name: "A", sort_order: 1, is_active: true },
    ] as Product[])
    expect(out.map((p) => p.id)).toEqual(["a", "b"])
  })

  it("keeps the server's group order, maps null max to 0, drops an empty optional group", () => {
    const out = optionGroupsFrom([
      wireGroup({ id: "extra", selection_type: "multiple", min_selections: 0, max_selections: null, is_required: false, sort_order: 20 }),
      wireGroup({ id: "cook", sort_order: 10 }),
      wireGroup({ id: "gone", selection_type: "multiple", min_selections: 0, is_required: false, modifiers: [] }),
    ])
    expect(out.kind).toBe("ready")
    if (out.kind !== "ready") return
    expect(out.groups.map((g) => g.id)).toEqual(["extra", "cook"])
    expect(out.groups[0].max_selections).toBe(0)
  })

  it("an empty REQUIRED group makes the product unavailable (added plain with a warning)", () => {
    expect(optionGroupsFrom([wireGroup({ id: "size", modifiers: [] })])).toEqual({ kind: "unavailable" })
  })

  it("a product absent from the tree has no options", () => {
    expect(optionGroupsFrom(undefined)).toEqual({ kind: "ready", groups: [] })
  })
})
