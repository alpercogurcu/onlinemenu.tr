import { describe, expect, it } from "vitest"

import { formatKurus, formatKurusDelta } from "@/lib/money"

// Prices are integer kuruş on the wire and stay integers everywhere except the
// final division by 100. These assertions compare against a non-breaking-space
// normalised string because Intl inserts U+00A0 between the symbol and digits.
function normalise(value: string): string {
  return value.replace(/ /g, " ")
}

describe("formatKurus", () => {
  it("renders kuruş as Turkish lira with two decimals", () => {
    expect(normalise(formatKurus(4500))).toBe("₺45,00")
  })

  it("keeps a trailing kuruş digit", () => {
    expect(normalise(formatKurus(4505))).toBe("₺45,05")
  })

  it("groups thousands the Turkish way", () => {
    expect(normalise(formatKurus(123456))).toBe("₺1.234,56")
  })

  it("renders zero", () => {
    expect(normalise(formatKurus(0))).toBe("₺0,00")
  })
})

describe("formatKurusDelta", () => {
  it("signs a positive delta", () => {
    expect(normalise(formatKurusDelta(500))).toBe("+₺5,00")
  })

  it("renders nothing for a zero delta", () => {
    expect(formatKurusDelta(0)).toBe("")
  })
})
