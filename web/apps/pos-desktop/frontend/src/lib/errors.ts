// Wails surfaces Go errors as plain strings (the *APIError.Error() message,
// e.g. "apiclient: place order: apiclient: unexpected status 422: ..."). This
// maps the status codes callers actually hit in this flow to a specific,
// actionable Turkish message — per the design plan's "hatalar spesifik"
// requirement, rather than showing the raw Go error string.
/**
 * True when a Go-side error carries an HTTP 403. Used to distinguish "this
 * station's role may not read payment status" (a permanent property of the
 * session — see fiscalStatus.ts's `unknown`) from a transient read failure that
 * is worth retrying on the next poll tick.
 *
 * String matching is the only option available: Wails flattens Go errors to
 * their Error() text across the IPC boundary, so the *APIError's status code is
 * not recoverable as a number here (see describeError below, which has always
 * worked this way).
 */
export function isForbiddenError(err: unknown): boolean {
  return String(err).includes('status 403')
}

/**
 * Machine-readable error codes the POS backend now returns in its JSON error
 * body (`{"error": "...", "code": "..."}`) — see pos/http/handler.go's
 * respondError. Every 409 that handler emits carries one, which is what makes
 * a conflict actionable: 409 alone is ambiguous (already closed vs. underpaid
 * vs. awaiting a fiscal result all share it).
 */
export type ApiErrorCode = 'fiscal_pending' | 'insufficient_payment' | 'invalid_transition' | 'table_occupied'

const KNOWN_CODES: ReadonlySet<string> = new Set<ApiErrorCode>([
  'fiscal_pending',
  'insufficient_payment',
  'invalid_transition',
  'table_occupied',
])

/**
 * Extracts the backend's `code` from a Wails-flattened Go error string.
 *
 * The raw text looks like:
 *   apiclient: close check: apiclient: unexpected status 409: {"error":"...","code":"fiscal_pending"}
 *
 * A regex rather than JSON.parse because the JSON body is embedded inside a
 * larger Go error message with prefixes on both sides — there is no clean
 * substring to hand a parser (see describeError's note on the IPC boundary).
 * Unknown codes return null so the substring fallbacks below still run: an
 * unrecognized code must degrade to the previous behavior, never to a blank
 * message.
 */
export function errorCode(err: unknown): ApiErrorCode | null {
  const match = /"code"\s*:\s*"([a-z_]+)"/.exec(String(err))
  if (!match) return null
  return KNOWN_CODES.has(match[1]) ? (match[1] as ApiErrorCode) : null
}

export function describeError(err: unknown): string {
  const raw = String(err)

  // The code is the PRIMARY signal — checked before any substring match, so a
  // wording change on the backend's human-readable message can never silently
  // change which Turkish message a cashier sees. The substring branches below
  // remain as the fallback for endpoints that do not emit a code yet.
  switch (errorCode(err)) {
    case 'fiscal_pending':
      return 'Mali kayıt bekleniyor — cihaz onayı gelince adisyonu kapatabilirsiniz.'
    case 'insufficient_payment':
      return 'Adisyon tam ödenmemiş — kalan tutar tahsil edilmeden kapatılamaz.'
    case 'table_occupied':
      return 'Bu masa az önce doldu — plan yenilendi, dolu masaya dokunarak açık adisyona geçebilirsiniz.'
    case 'invalid_transition':
      return 'Bu adisyon/sipariş başka bir işlemle çakışıyor — sayfayı yenileyin.'
    default:
      break
  }

  if (raw.includes('status 401')) return 'Oturum geçersiz — tekrar giriş yapın.'
  if (raw.includes('status 403')) return 'Bu işlem için yetkiniz yok (şube/rol uyuşmazlığı).'
  if (raw.includes('status 404')) return 'Kayıt bulunamadı — sayfayı yenileyin.'
  if (raw.includes('already used with a different')) {
    return 'Tekrarlanan istek uyuşmazlığı (Idempotency-Key) — işlem farklı bir istekle karışmış olabilir, tekrar deneyin.'
  }
  if (raw.includes('already being processed')) {
    return 'Bu işlem hâlâ işleniyor, lütfen birkaç saniye bekleyip tekrar deneyin.'
  }
  // Masa planı (Sprint-5 Wave 2) — specific bodies checked before the
  // generic 409/422 fallbacks below, since a table-select conflict needs an
  // actionable message ("dolu masaya dokunun"), not a generic "çakışıyor".
  if (raw.includes('table is already occupied')) {
    return 'Bu masa az önce doldu — plan yenilendi, dolu masaya dokunarak açık adisyona geçebilirsiniz.'
  }
  if (raw.includes('table does not belong to this branch')) {
    return 'Bu masa başka bir şubeye ait — bu istasyondan seçilemez.'
  }
  if (raw.includes('table can only become occupied by opening a check')) {
    return 'Masa durumu yalnızca adisyon açılarak değiştirilebilir.'
  }
  if (raw.includes('status 422')) return 'Eksik veya geçersiz bilgi — girdileri kontrol edin.'
  if (raw.includes('status 409')) return 'Bu adisyon/sipariş başka bir işlemle çakışıyor — sayfayı yenileyin.'
  if (raw.includes('status 500')) {
    // No longer mentions underpayment: that case is now an explicit 409 with
    // code `insufficient_payment` (see the switch above), so a 500 here really
    // is an unexpected server-side fault and saying otherwise would send the
    // cashier chasing a payment problem that does not exist.
    return 'Sunucu hatası — beklenmeyen bir sorun oluştu, tekrar deneyin.'
  }
  return raw
}
