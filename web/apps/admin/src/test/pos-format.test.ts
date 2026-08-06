import { describe, expect, it } from "vitest"

import {
  formatCheckDuration,
  formatCheckTotal,
  formatOpenDuration,
  isLongOpenCheck,
} from "@/lib/pos-format"

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

// formatOpenDuration is the live, still-running reading used by open rows.
// Its "az önce" / "3s+" wording is intentional there and is exactly what must
// NOT reach a closed row — see formatCheckDuration below.
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
})

describe("formatCheckDuration", () => {
  const now = new Date("2026-07-06T12:00:00Z")

  it("returns seconds for under a minute (a QR check can open and close in 14s)", () => {
    expect(formatCheckDuration("2026-07-06T11:59:46Z", now)).toBe("14 sn")
    expect(formatCheckDuration("2026-07-06T12:00:00Z", now)).toBe("0 sn")
  })

  it("returns minutes for under an hour", () => {
    expect(formatCheckDuration("2026-07-06T11:48:00Z", now)).toBe("12 dk")
  })

  it("returns hours and minutes, zero-padded", () => {
    expect(formatCheckDuration("2026-07-06T10:45:00Z", now)).toBe("1s 15dk")
  })

  it("does not cap long durations at three hours", () => {
    expect(formatCheckDuration("2026-07-06T09:00:00Z", now)).toBe("3s 00dk")
    expect(formatCheckDuration("2026-07-06T06:30:00Z", now)).toBe("5s 30dk")
  })

  it("switches to days past 24 hours so a forgotten check stays readable", () => {
    expect(formatCheckDuration("2026-07-05T12:00:00Z", now)).toBe("1g 0s")
    expect(formatCheckDuration("2026-06-05T09:24:00Z", now)).toBe("31g 2s")
  })

  it("clamps a negative span (clock skew) to zero instead of rendering it", () => {
    expect(formatCheckDuration("2026-07-06T12:00:30Z", now)).toBe("0 sn")
  })

  it("returns a dash for an invalid timestamp", () => {
    expect(formatCheckDuration("not-a-date", now)).toBe("—")
  })

  it("measures the closed span when given closed_at (opened_at -> closed_at)", () => {
    expect(formatCheckDuration("2026-07-06T10:00:00Z", new Date("2026-07-06T10:42:00Z"))).toBe(
      "42 dk",
    )
  })

  // The regression this whole formatter exists for: a check opened and closed
  // in 14 seconds, a month before it is looked at.
  it("reports a short closed span as a duration, not as 'az önce'", () => {
    const opened = "2026-07-05T23:52:58Z"
    const closed = new Date("2026-07-05T23:53:12Z")

    expect(formatCheckDuration(opened, closed)).toBe("14 sn")
    expect(formatOpenDuration(opened, closed)).toBe("az önce")
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
