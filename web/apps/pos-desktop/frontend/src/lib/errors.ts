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

  // ADR-DATA-008 PIN akışı (kasiyer değiştirme) — checked ahead of the
  // generic 401 fallback: SwitchCashier's 401 is a deliberately generic,
  // enumeration-safe verification failure (wrong pin / pin never set /
  // locked / not a participant, all indistinguishable by design — see
  // apiclient.ErrPinVerificationFailed's doc comment), never a "your
  // session expired" situation. Showing "Oturum geçersiz — tekrar giriş
  // yapın" here would be actively misleading: nobody needs to log back in,
  // and it would not even be true (the CTX token this call was made with is
  // untouched — see cash_session_pin_test.go's recovery-bypass proof).
  if (raw.includes('pin verification failed')) {
    return 'Doğrulama başarısız — isim ve PIN’i kontrol edin.'
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
  // ADR-DATA-008 kasa oturumu — checked ahead of the generic 409 fallback for
  // the same reason as the table-plan bodies above: a cashier staring at a
  // blocked açılış/kapanış needs the specific reason, not "çakışıyor".
  if (raw.includes('branch already has an open cash session')) {
    return 'Bu şubede zaten açık bir kasa oturumu var — sayfayı yenileyin.'
  }
  if (raw.includes('movements can only be recorded while opened')) {
    return 'Sayım gönderildikten sonra nakit giriş/çıkış yapılamaz — kasa yeniden sayılmadan hareket eklenemez.'
  }
  if (raw.includes('submit a closing count first')) {
    return 'Kasayı kapatmadan önce sayım yapılmalı.'
  }
  if (raw.includes('denomination sum') && raw.includes('does not equal')) {
    // Should be unreachable from this client — closing_counted_amount is
    // always derived from the denomination rows themselves (see
    // lib/cashSession.ts's denominationTotal) — but kept as a specific
    // message rather than falling through to a generic 422, in case a future
    // caller regresses that invariant.
    return 'Kupür dökümü toplamı sayılan tutarla eşleşmiyor.'
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
