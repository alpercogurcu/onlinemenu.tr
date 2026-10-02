import { describe, expect, it } from "vitest"

import { overrideDiffers, overrideHasDeviation } from "@/lib/branch-overrides"
import type { BranchProductOverride } from "@/types"

const row = (patch: Partial<BranchProductOverride>): BranchProductOverride => ({
  branch_id: "b1",
  product_id: "p1",
  is_available: true,
  price_amount: null,
  cost_amount: null,
  updated_at: "2026-10-02T10:00:00Z",
  ...patch,
})

describe("branch override predicates", () => {
  it.each([
    ["no row", undefined, false],
    ["empty row", row({}), false],
    ["price set", row({ price_amount: 100 }), true],
    ["closed", row({ is_available: false }), true],
    ["cost only is not a sale difference", row({ cost_amount: 500 }), false],
  ])("overrideDiffers: %s", (_name, input, want) => {
    expect(overrideDiffers(input)).toBe(want)
  })

  it.each([
    ["no row", undefined, false],
    ["empty row", row({}), false],
    ["price set", row({ price_amount: 100 }), true],
    ["closed", row({ is_available: false }), true],
    ["cost only", row({ cost_amount: 500 }), true],
    ["zero cost is a real value", row({ cost_amount: 0 }), true],
  ])("overrideHasDeviation: %s", (_name, input, want) => {
    expect(overrideHasDeviation(input)).toBe(want)
  })
})
