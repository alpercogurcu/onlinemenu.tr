// periodRange must return LOCAL calendar-day boundaries (not UTC-truncated
// ones) converted to ISO strings, because the backend interprets `from`/`to`
// together with the `tz` query param it also receives — a UTC-midnight
// boundary would shift the reported day for any tenant not on UTC. Building
// the expected values with the same local Date constructors as the
// implementation (rather than hardcoded ISO strings) keeps this test
// independent of the machine's timezone running it.
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { renderHook, waitFor } from "@testing-library/react"
import type { ReactNode } from "react"
import { beforeEach, describe, expect, it, vi } from "vitest"

import { periodRange, useSaleDetails } from "@/hooks/use-reports"

const get = vi.fn()

vi.mock("@/lib/api", () => ({
  default: {
    get: (...args: unknown[]) => get(...args),
  },
}))

function localDay(year: number, month: number, day: number): Date {
  return new Date(year, month, day)
}

describe("periodRange", () => {
  const now = new Date(2026, 8, 5, 15, 0, 0) // 2026-09-05 15:00 local

  it("today: local midnight to local midnight tomorrow", () => {
    expect(periodRange("today", now)).toEqual({
      from: localDay(2026, 8, 5).toISOString(),
      to: localDay(2026, 8, 6).toISOString(),
    })
  })

  it("yesterday: local midnight yesterday to local midnight today", () => {
    expect(periodRange("yesterday", now)).toEqual({
      from: localDay(2026, 8, 4).toISOString(),
      to: localDay(2026, 8, 5).toISOString(),
    })
  })

  it("last7: local midnight 6 days ago to local midnight tomorrow", () => {
    expect(periodRange("last7", now)).toEqual({
      from: localDay(2026, 7, 30).toISOString(),
      to: localDay(2026, 8, 6).toISOString(),
    })
  })

  it("thisMonth: local midnight on the 1st to local midnight tomorrow", () => {
    expect(periodRange("thisMonth", now)).toEqual({
      from: localDay(2026, 8, 1).toISOString(),
      to: localDay(2026, 8, 6).toISOString(),
    })
  })
})

function Wrapper({ children }: { children: ReactNode }) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>
}

describe("useSaleDetails", () => {
  beforeEach(() => {
    get.mockReset()
    get.mockResolvedValue({ data: { sales: {}, cash_sessions: [] } })
  })

  it("requests the report with branch, range and the resolved local timezone", async () => {
    const { result } = renderHook(
      () => useSaleDetails({ branchId: "branch-1", from: "2026-09-05T00:00:00.000Z", to: "2026-09-06T00:00:00.000Z" }),
      { wrapper: Wrapper },
    )

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(get).toHaveBeenCalledWith("/api/v1/pos/reports/sale-details", {
      params: {
        branch_id: "branch-1",
        from: "2026-09-05T00:00:00.000Z",
        to: "2026-09-06T00:00:00.000Z",
        tz: Intl.DateTimeFormat().resolvedOptions().timeZone,
      },
    })
  })

  it("does not request when branchId is empty", () => {
    renderHook(() => useSaleDetails({ branchId: "", from: "a", to: "b" }), { wrapper: Wrapper })
    expect(get).not.toHaveBeenCalled()
  })
})
