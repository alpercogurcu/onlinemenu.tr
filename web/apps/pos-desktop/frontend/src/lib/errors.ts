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

export function describeError(err: unknown): string {
  const raw = String(err)

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
    return 'Sunucu hatası — adisyon tam ödenmemiş olabilir ya da beklenmeyen bir sorun oluştu.'
  }
  return raw
}
