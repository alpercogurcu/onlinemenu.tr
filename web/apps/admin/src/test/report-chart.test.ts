// Günlük Satış chart (2026-09-23 prod sweep): the Y axis printed raw kuruş
// ("600000" for ₺6.000) and days without a sale were skipped, so the line was
// drawn straight across them as if those days had sold.
import { describe, expect, it } from "vitest"

import { axisLira, fillDailySeries } from "@/lib/report-chart"

describe("axisLira", () => {
  it("prints lira, not kuruş", () => {
    expect(axisLira(600_000)).toBe("₺6.000")
    expect(axisLira(0)).toBe("₺0")
  })
})

describe("fillDailySeries", () => {
  // periodRange hands local-midnight instants; the test pins them as local
  // dates so it does not depend on the runner's time zone.
  const from = new Date(2026, 8, 20).toISOString()
  const to = new Date(2026, 8, 24).toISOString()

  it("adds a zero day for every day in the range without a sale", () => {
    const series = fillDailySeries(
      [
        { date: "2026-09-20", gross: 540_000, check_count: 6 },
        { date: "2026-09-23", gross: 180_000, check_count: 2 },
      ],
      from,
      to,
    )
    expect(series.map((d) => [d.date, d.gross])).toEqual([
      ["2026-09-20", 540_000],
      ["2026-09-21", 0],
      ["2026-09-22", 0],
      ["2026-09-23", 180_000],
    ])
  })

  it("returns an all-zero series for a range without sales", () => {
    expect(fillDailySeries([], from, to).every((d) => d.gross === 0)).toBe(true)
  })
})
