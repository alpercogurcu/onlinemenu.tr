// Every amount on this surface is an integer number of kuruş, exactly as the
// API sends it. Totals are summed in integers and divided by 100 only at the
// moment of rendering — no intermediate float ever holds a price.

const TRY_FORMATTER = new Intl.NumberFormat("tr-TR", {
  style: "currency",
  currency: "TRY",
  minimumFractionDigits: 2,
  maximumFractionDigits: 2,
})

/** 4500 → "₺45,00" */
export function formatKurus(amount: number): string {
  return TRY_FORMATTER.format(amount / 100)
}

/** Signed form for modifier deltas: 500 → "+₺5,00", 0 → "". */
export function formatKurusDelta(amount: number): string {
  if (amount === 0) return ""
  const sign = amount > 0 ? "+" : "−"
  return `${sign}${formatKurus(Math.abs(amount))}`
}
