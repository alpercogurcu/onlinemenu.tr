import { describe, expect, it } from "vitest"

import { formatKurus, formatKurusForInput, parseLiraToKurus } from "@/lib/money"

describe("formatKurus", () => {
  it("formats kurus as Turkish lira", () => {
    expect(formatKurus(12345)).toBe("₺123,45")
  })

  it("distinguishes zero from absent", () => {
    expect(formatKurus(0)).toBe("₺0,00")
    expect(formatKurus(null)).toBe("—")
    expect(formatKurus(undefined)).toBe("—")
  })
})

describe("parseLiraToKurus", () => {
  it("accepts the Turkish decimal comma", () => {
    expect(parseLiraToKurus("123,45")).toBe(12345)
  })

  it("accepts a dot as well, so a paste from elsewhere is not rejected", () => {
    expect(parseLiraToKurus("123.45")).toBe(12345)
  })

  it("ignores currency symbol and surrounding spaces", () => {
    expect(parseLiraToKurus(" ₺ 12 ")).toBe(1200)
  })

  it("rounds to the nearest kurus instead of truncating float error", () => {
    expect(parseLiraToKurus("10,30")).toBe(1030)
    expect(parseLiraToKurus("0,005")).toBe(1)
  })

  it("returns null for empty, non-numeric and negative input", () => {
    expect(parseLiraToKurus("")).toBeNull()
    expect(parseLiraToKurus("   ")).toBeNull()
    expect(parseLiraToKurus("abc")).toBeNull()
    expect(parseLiraToKurus("-5")).toBeNull()
  })

  it("treats zero as a valid amount", () => {
    expect(parseLiraToKurus("0")).toBe(0)
  })
})

describe("formatKurusForInput", () => {
  it("round-trips through parseLiraToKurus", () => {
    const original = 12345
    expect(formatKurusForInput(original)).toBe("123,45")
    expect(parseLiraToKurus(formatKurusForInput(original))).toBe(original)
  })
})
