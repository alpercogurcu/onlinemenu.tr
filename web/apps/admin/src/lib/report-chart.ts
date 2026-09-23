// Pure helpers for the dashboard's Günlük Satış chart.

export interface DailySale {
  date: string
  gross: number
  check_count: number
}

const liraAxis = new Intl.NumberFormat("tr-TR", {
  style: "currency",
  currency: "TRY",
  maximumFractionDigits: 0,
})

/** Y-axis tick: the report is in kuruş, the reader thinks in lira. */
export function axisLira(kurus: number): string {
  return liraAxis.format(kurus / 100)
}

function localDate(d: Date): string {
  const m = String(d.getMonth() + 1).padStart(2, "0")
  const day = String(d.getDate()).padStart(2, "0")
  return `${d.getFullYear()}-${m}-${day}`
}

/**
 * One point per business day in [from, to). by_day lists only days that
 * sold; without the zero days the area is drawn straight across them, which
 * reads as sales that never happened. `from`/`to` are the local-midnight
 * instants periodRange() produces, so walking local dates matches the
 * backend's tenant-local business days for a same-zone operator.
 */
export function fillDailySeries(byDay: DailySale[], from: string, to: string): DailySale[] {
  const known = new Map(byDay.map((d) => [d.date, d]))
  const out: DailySale[] = []
  const cursor = new Date(from)
  const end = new Date(to)
  // Safety cap: the backend refuses ranges over 92 days anyway.
  for (let i = 0; cursor < end && i < 400; i++) {
    const date = localDate(cursor)
    out.push(known.get(date) ?? { date, gross: 0, check_count: 0 })
    cursor.setDate(cursor.getDate() + 1)
  }
  return out
}
