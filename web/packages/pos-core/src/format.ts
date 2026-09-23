// Amounts everywhere in this app are integers in kuruş (TRY's smallest unit —
// see backend productResponse.price_amount / orderItemResponse.unit_price_amount),
// matching the backend's int64 amount fields.

const tryFormatter = new Intl.NumberFormat('tr-TR', {
  style: 'currency',
  currency: 'TRY',
  minimumFractionDigits: 2,
  maximumFractionDigits: 2,
})

/** Formats a kuruş integer amount as a Turkish lira string, e.g. 12345 -> "123,45 ₺". */
export function formatMoney(amountKurus: number): string {
  return tryFormatter.format(amountKurus / 100)
}

/** Parses a user-typed TRY amount (e.g. "123,45" or "123.45") into kuruş. */
export function parseMoneyInputToKurus(input: string): number {
  const normalized = input.trim().replace(/\./g, '').replace(',', '.')
  const value = Number.parseFloat(normalized)
  if (!Number.isFinite(value) || value < 0) return 0
  return Math.round(value * 100)
}
