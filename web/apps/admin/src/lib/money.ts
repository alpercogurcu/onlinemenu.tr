// Money helpers. The backend speaks kuruş (int64) everywhere — product
// price_amount, menu item price_override, check total — while every input the
// user types is in lira, so the conversion has to live in exactly one place.

const tryLiraFormatter = new Intl.NumberFormat("tr-TR", {
  style: "currency",
  currency: "TRY",
})

// formatKurus renders an amount in kuruş as a Turkish lira string, e.g.
// 12345 -> "₺123,45". Nullish input renders an em dash rather than "₺0,00",
// because "no value" and "zero" are different claims (a menu item with no
// price override is not a free item).
export function formatKurus(amount: number | null | undefined): string {
  if (amount == null) return "—"
  return tryLiraFormatter.format(amount / 100)
}

// parseLiraToKurus turns user input ("123,45", "123.45", "₺123,45", " 12 ")
// into kuruş, or null when the text is not a usable amount. It returns null —
// rather than 0 or NaN — for invalid input so callers must decide explicitly
// what an unparseable field means; sending NaN would serialise to JSON `null`
// and silently clear a price override instead of failing.
//
// Negative amounts are rejected: a price override below zero has no meaning
// and the backend stores it in an unsigned-in-practice money column.
export function parseLiraToKurus(input: string): number | null {
  const normalized = input.replace(/[₺\s]/g, "").replace(",", ".")
  if (normalized === "") return null

  const lira = Number(normalized)
  if (!Number.isFinite(lira) || lira < 0) return null

  // Round rather than truncate: 0.1 + 0.2 style float error would otherwise
  // turn "10,30" into 1029 kuruş.
  return Math.round(lira * 100)
}

// formatKurusForInput renders kuruş back into the plain lira text an <input>
// should hold ("123,45") — no currency symbol, no thousands separator, so the
// value round-trips through parseLiraToKurus unchanged.
export function formatKurusForInput(amount: number): string {
  return (amount / 100).toFixed(2).replace(".", ",")
}
