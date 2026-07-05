// Wails surfaces Go errors as plain strings (the *APIError.Error() message,
// e.g. "apiclient: place order: apiclient: unexpected status 422: ..."). This
// maps the status codes callers actually hit in this flow to a specific,
// actionable Turkish message — per the design plan's "hatalar spesifik"
// requirement, rather than showing the raw Go error string.
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
  if (raw.includes('status 422')) return 'Eksik veya geçersiz bilgi — girdileri kontrol edin.'
  if (raw.includes('status 409')) return 'Bu adisyon/sipariş başka bir işlemle çakışıyor — sayfayı yenileyin.'
  if (raw.includes('status 500')) {
    return 'Sunucu hatası — adisyon tam ödenmemiş olabilir ya da beklenmeyen bir sorun oluştu.'
  }
  return raw
}
