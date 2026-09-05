import { useQuery } from "@tanstack/react-query"
import { useEffect, useState } from "react"

import api from "@/lib/api"
import type { SaleDetails } from "@/types"

export type ReportPeriod = "today" | "yesterday" | "last7" | "thisMonth"

function localMidnight(d: Date): Date {
  return new Date(d.getFullYear(), d.getMonth(), d.getDate())
}

function calendarDayString(d: Date): string {
  const year = d.getFullYear()
  const month = String(d.getMonth() + 1).padStart(2, "0")
  const day = String(d.getDate()).padStart(2, "0")
  return `${year}-${month}-${day}`
}

// useCalendarDay returns today's LOCAL calendar day ("YYYY-MM-DD") and only
// triggers a re-render of its caller when that day actually changes. It
// exists to be used as a `useMemo`/`useEffect` dependency for anything
// derived from "today" (see periodRange below) — a dashboard left open past
// local midnight would otherwise keep computing "today" as the day it was
// first rendered, since neither state nor props change on their own when the
// clock crosses midnight. A 60s poll is simpler than scheduling a precise
// midnight timeout and accurate enough: nothing here needs to roll over
// within a second of midnight.
export function useCalendarDay(): string {
  const [day, setDay] = useState(() => calendarDayString(new Date()))

  useEffect(() => {
    const id = setInterval(() => {
      const current = calendarDayString(new Date())
      setDay((prev) => (prev === current ? prev : current))
    }, 60_000)
    return () => clearInterval(id)
  }, [])

  return day
}

function addDays(d: Date, days: number): Date {
  const copy = new Date(d)
  copy.setDate(copy.getDate() + days)
  return copy
}

// periodRange computes LOCAL calendar-day boundaries for a preset, then
// serializes them to ISO. `to` is deliberately the half-open upper bound
// (midnight of the day AFTER the period ends) rather than the period's last
// instant, so the backend can filter with a simple `>= from AND < to` and
// never has to reason about which fraction of a second belongs to the range.
export function periodRange(period: ReportPeriod, now: Date = new Date()): { from: string; to: string } {
  const today = localMidnight(now)
  const tomorrow = addDays(today, 1)

  switch (period) {
    case "today":
      return { from: today.toISOString(), to: tomorrow.toISOString() }
    case "yesterday":
      return { from: addDays(today, -1).toISOString(), to: today.toISOString() }
    case "last7":
      return { from: addDays(today, -6).toISOString(), to: tomorrow.toISOString() }
    case "thisMonth":
      return { from: new Date(now.getFullYear(), now.getMonth(), 1).toISOString(), to: tomorrow.toISOString() }
  }
}

// useSaleDetails fetches the end-of-day sale summary for one branch and
// range. `tz` is resolved from the browser (not sent by the caller) because
// the backend needs it to interpret `from`/`to` as the SAME local calendar
// boundaries periodRange computed them from — the request would otherwise be
// ambiguous about which timezone's midnight the ISO instants refer to.
export function useSaleDetails(params: { branchId: string; from: string; to: string }) {
  const { branchId, from, to } = params
  return useQuery({
    queryKey: ["reports", "sale-details", branchId, from, to],
    queryFn: async () => {
      const { data } = await api.get<SaleDetails>("/api/v1/pos/reports/sale-details", {
        params: {
          branch_id: branchId,
          from,
          to,
          tz: Intl.DateTimeFormat().resolvedOptions().timeZone,
        },
      })
      return data
    },
    enabled: branchId !== "",
  })
}
