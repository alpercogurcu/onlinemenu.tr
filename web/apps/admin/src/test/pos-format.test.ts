import { describe, expect, it } from "vitest"

import { formatCheckTotal, formatOpenDuration, isLongOpenCheck } from "@/lib/pos-format"

describe("formatCheckTotal", () => {
  it("formats kurus as Turkish lira", () => {
    expect(formatCheckTotal(12345)).toBe("₺123,45")
  })

  it("formats zero", () => {
    expect(formatCheckTotal(0)).toBe("₺0,00")
  })

  it("returns a dash when total is not yet populated", () => {
    expect(formatCheckTotal(null)).toBe("—")
    expect(formatCheckTotal(undefined)).toBe("—")
  })
})

describe("formatOpenDuration", () => {
  const now = new Date("2026-07-06T12:00:00Z")

  it("returns 'az önce' for under a minute", () => {
    expect(formatOpenDuration("2026-07-06T11:59:30Z", now)).toBe("az önce")
  })

  it("returns minutes for under an hour", () => {
    expect(formatOpenDuration("2026-07-06T11:48:00Z", now)).toBe("12 dk")
  })

  it("returns hours and minutes, zero-padded, under 3 hours", () => {
    expect(formatOpenDuration("2026-07-06T10:45:00Z", now)).toBe("1s 15dk")
  })

  it("caps at '3s+' at or beyond 3 hours", () => {
    expect(formatOpenDuration("2026-07-06T09:00:00Z", now)).toBe("3s+")
    expect(formatOpenDuration("2026-07-05T12:00:00Z", now)).toBe("3s+")
  })

  it("returns a dash for an invalid opened_at", () => {
    expect(formatOpenDuration("not-a-date", now)).toBe("—")
  })

  it("supports computing the elapsed open span for a closed check (opened_at -> closed_at)", () => {
    expect(formatOpenDuration("2026-07-06T10:00:00Z", new Date("2026-07-06T10:42:00Z"))).toBe(
      "42 dk",
    )
  })
})

describe("isLongOpenCheck", () => {
  const now = new Date("2026-07-06T12:00:00Z")

  it("is false under the threshold", () => {
    expect(isLongOpenCheck("2026-07-06T11:00:00Z", now)).toBe(false)
  })

  it("is true at/over the default 2h threshold", () => {
    expect(isLongOpenCheck("2026-07-06T09:59:00Z", now)).toBe(true)
  })

  it("returns false for an invalid opened_at", () => {
    expect(isLongOpenCheck("not-a-date", now)).toBe(false)
  })
})
