import { render, screen } from "@testing-library/react"
import { describe, expect, it } from "vitest"

import { Badge } from "@/components/ui/badge"
import {
  checkStatusVariant,
  membershipStatusVariant,
  orderStatusVariant,
  paymentStatusVariant,
  productStatusVariant,
  tableStatusVariant,
} from "@/lib/status-badge"

describe("status-badge mappings", () => {
  it.each([
    [true, "success"],
    [false, "neutral"],
  ] as const)("productStatusVariant(%s) → %s", (active, variant) => {
    expect(productStatusVariant(active)).toBe(variant)
  })

  it.each([
    ["open", "warning"],
    ["closed", "success"],
    ["cancelled", "neutral"],
  ] as const)("checkStatusVariant(%s) → %s", (status, variant) => {
    expect(checkStatusVariant(status)).toBe(variant)
  })

  it.each([
    ["pending", "warning"],
    ["accepted", "info"],
    ["preparing", "info"],
    ["ready", "success"],
    ["delivered", "success"],
    ["rejected", "neutral"],
    ["cancelled", "neutral"],
  ] as const)("orderStatusVariant(%s) → %s", (status, variant) => {
    expect(orderStatusVariant(status)).toBe(variant)
  })

  it.each([
    ["empty", "neutral"],
    ["occupied", "warning"],
    ["reserved", "info"],
    ["cleaning", "warning"],
  ] as const)("tableStatusVariant(%s) → %s", (status, variant) => {
    expect(tableStatusVariant(status)).toBe(variant)
  })

  it.each([
    ["completed", "success"],
    ["pending", "info"],
    ["failed", "danger"],
    ["voided", "danger"],
    ["weird", "neutral"],
  ] as const)("paymentStatusVariant(%s) → %s", (status, variant) => {
    expect(paymentStatusVariant(status)).toBe(variant)
  })

  it.each([
    ["active", "success"],
    ["suspended", "neutral"],
    ["terminated", "neutral"],
  ] as const)("membershipStatusVariant(%s) → %s", (status, variant) => {
    expect(membershipStatusVariant(status)).toBe(variant)
  })
})

describe("Badge status variants", () => {
  it("renders the success variant with the status token classes", () => {
    render(<Badge variant="success">Satışta</Badge>)
    const el = screen.getByText("Satışta")
    expect(el.className).toContain("bg-status-success-bg")
    expect(el.className).toContain("text-status-success-fg")
    expect(el.className).toContain("border-status-success-border")
  })
})
