# ADR-SEC-003: Idempotency-Key Altyapısı

**Durum:** Kabul Edildi
**Tarih:** 2026-04-19
**İlgili delta:** P0-4
**Kategori:** Güvenlik (SEC)

## Bağlam

POS + Payment akışında ağ kesintisi + kullanıcı tekrar tıklama = çift ödeme, çift adisyon, çift fatura. Kasiyer "ödeme al" butonuna basar, terminal onayı gelir, ama cloud yanıtı dönerken network kesilir; kasiyer tekrar basar. Sonuç: müşteri iki kez ödeme kaydı, bir kez para ödedi. Bu tür bug'lar şirket öldürür.

Idempotency, HTTP katmanında bir konvansiyondur (Stripe, Shopify standardı): client her yazma isteğinde unique bir `Idempotency-Key` gönderir. Aynı key ile gelen ikinci istek, ilk isteğin cached cevabını döner — handler'a ulaşmaz.

## Karar

1. **Platform middleware:** `internal/platform/httpx/idempotency.go` içinde HTTP middleware olarak.

2. **Header:** `Idempotency-Key: <uuid-v4>`. Client tarafında üretilir, retry'da aynı kalır.

3. **Zorunluluk matrisi:**
   - **Zorunlu** (eksikse 400): `POST /v1/payments/*`, `POST /v1/invoices/*`, `POST /v1/checks/{id}/close`, `POST /v1/orders`
   - **Opsiyonel ama önerilir:** Diğer POST/PUT/PATCH endpoint'leri
   - **Uygulanmaz:** GET, DELETE, HEAD

4. **Storage:** Redis. Key: `idempotency:{tenant_id}:{idempotency_key}`. TTL 24 saat.

5. **Saklanan değer:**
   - Request payload'unun SHA-256 hash'i (path + body + tenant_id)
   - Response (HTTP status + body + content-type)

6. **Çakışma davranışı:**
   - İlk istek: handler çalışır, response Redis'e yazılır, 24 saat cache.
   - Aynı key + aynı hash: cached response döner, handler **çağrılmaz**.
   - Aynı key + farklı hash: 422 Unprocessable Entity + "idempotency key reused with different payload" — client bug.

7. **Race condition:** Redis'te `SET NX` ile "işleniyor" marker'ı konur; paralel ikinci istek 409 Conflict + `Retry-After: 2` döner. İlk istek tamamlanınca marker response ile değiştirilir.

8. **Response snapshot'ı:** Handler'ın tüm yan etkileri (DB, NATS publish) tamamlandıktan **sonra** cache'lenir. Handler hata verirse cache yazılmaz; client retry edebilir.

9. **Fail-close kararı:** Redis down → yazma endpoint'leri 503 döner. Idempotency'den ödün verilmez.

## Sonuçlar

**İyi:**
- Ağ kesintisi + retry kombinasyonu güvenli; "çift ödeme" sınıfı bug'lar yapısal olarak önlenir.
- Single middleware, tek platform mekanizması; modüller kendi idempotency çözümünü yazmaz.
- Client tarafı kolay: UUID üret, retry'larda aynı UUID'yi kullan.

**Dikkat:**
- 24 saat cache = Redis'te storage büyür. 1M transaction/gün × 2 KB ≈ 2 GB/gün. TTL + eviction yönetilmeli.
- Handler uzun sürerse ikinci istek 409 + retry döngüsüne girer. Uzun işlemler Temporal'a taşınmalı.

**Risk:**
- Client sabit string gönderirse → 422 ile erken yakalanır.
- Redis down → fail-close (503). Idempotency'den ödün verilmez.

## Değerlendirilen Alternatifler

- **DB'de idempotency tablosu:** Reddedildi Faz 0-1 için. Ek DB roundtrip + yük artışı. Faz 2'de denetim kaydı gerekliliği doğarsa eklenir.
- **Handler'ın kendisinin idempotent olması:** Reddedildi tek başına. Harici API çağrısı + DB write kombinasyonu kendi içinde idempotent yapılamaz.
- **Request body hash key olsun:** Reddedildi. Aynı body = aynı key = retry fark edilemez.
