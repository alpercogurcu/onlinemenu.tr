import { describe, expect, it } from "vitest"

import {
  MAX_LINE_MODIFIERS,
  defaultSelection,
  maxSelections,
  selectionPriceDelta,
  toggleModifier,
  unsatisfiedRequiredGroups,
} from "@/lib/modifiers"
import type { MenuModifierGroup } from "@/types/storefront"

function group(overrides: Partial<MenuModifierGroup> = {}): MenuModifierGroup {
  return {
    id: "g1",
    name: "Ekstralar",
    selection_type: "multiple",
    min_select: 0,
    max_select: 0,
    modifiers: [
      { id: "m1", name: "Ekstra shot", price_delta: 500 },
      { id: "m2", name: "Vanilya", price_delta: 250 },
      { id: "m3", name: "Karamel", price_delta: 0 },
    ],
    ...overrides,
  }
}

describe("maxSelections", () => {
  // The contract's sharpest edge: the server accepts at most one selection
  // from a "single" group even when max_select says 0 (= unbounded). A UI that
  // reads max_select literally builds a cart that is rejected with a 422.
  it("caps a single group at one even when max_select is 0", () => {
    expect(maxSelections(group({ selection_type: "single", max_select: 0 }))).toBe(1)
  })

  it("caps an unbounded multiple group at the API's per-line modifier limit", () => {
    expect(maxSelections(group({ selection_type: "multiple", max_select: 0 }))).toBe(
      MAX_LINE_MODIFIERS,
    )
  })

  it("honours an explicit max_select for a multiple group", () => {
    expect(maxSelections(group({ max_select: 2 }))).toBe(2)
  })

  it("never exceeds the API limit even when max_select is larger", () => {
    expect(maxSelections(group({ max_select: 999 }))).toBe(MAX_LINE_MODIFIERS)
  })
})

describe("toggleModifier", () => {
  it("replaces the previous choice in a single group", () => {
    const g = group({ selection_type: "single", max_select: 0 })
    let selection = toggleModifier({}, g, "m1")
    selection = toggleModifier(selection, g, "m2")
    expect(selection[g.id]).toEqual(["m2"])
  })

  it("keeps a required single group non-empty when the choice is re-tapped", () => {
    const g = group({ selection_type: "single", min_select: 1 })
    const selection = toggleModifier(toggleModifier({}, g, "m1"), g, "m1")
    expect(selection[g.id]).toEqual(["m1"])
  })

  it("clears an optional single group when the choice is re-tapped", () => {
    const g = group({ selection_type: "single", min_select: 0 })
    const selection = toggleModifier(toggleModifier({}, g, "m1"), g, "m1")
    expect(selection[g.id]).toEqual([])
  })

  it("refuses a selection past max_select in a multiple group", () => {
    const g = group({ max_select: 2 })
    let selection = toggleModifier({}, g, "m1")
    selection = toggleModifier(selection, g, "m2")
    selection = toggleModifier(selection, g, "m3")
    expect(selection[g.id]).toEqual(["m1", "m2"])
  })

  it("never selects the same modifier twice", () => {
    // The API rejects a line carrying one modifier_id more than once.
    const g = group()
    const selection = toggleModifier(toggleModifier({}, g, "m1"), g, "m2")
    expect(new Set(selection[g.id]).size).toBe(selection[g.id].length)
  })
})

describe("unsatisfiedRequiredGroups", () => {
  // min_select is NOT enforced server-side (a deliberate contract limit), so
  // this is the only thing standing between a stale catalog and a wrong order.
  it("reports a required group with too few selections", () => {
    const g = group({ min_select: 2 })
    expect(unsatisfiedRequiredGroups([g], { g1: ["m1"] })).toHaveLength(1)
  })

  it("passes a satisfied group", () => {
    const g = group({ min_select: 1 })
    expect(unsatisfiedRequiredGroups([g], { g1: ["m1"] })).toHaveLength(0)
  })

  it("passes an optional group with no selection", () => {
    expect(unsatisfiedRequiredGroups([group()], {})).toHaveLength(0)
  })
})

describe("defaultSelection", () => {
  it("pre-selects the first option of a required single group", () => {
    const g = group({ selection_type: "single", min_select: 1 })
    expect(defaultSelection([g])[g.id]).toEqual(["m1"])
  })

  it("leaves optional groups empty", () => {
    expect(defaultSelection([group()]).g1).toEqual([])
  })
})

describe("selectionPriceDelta", () => {
  it("sums deltas in integer kuruş", () => {
    expect(selectionPriceDelta([group()], { g1: ["m1", "m2"] })).toBe(750)
  })
})
