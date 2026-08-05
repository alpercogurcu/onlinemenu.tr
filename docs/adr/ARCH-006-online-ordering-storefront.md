# ARCH-006 — Online Sipariş MVP: Storefront Modülü (QR Dine-in)

**Durum:** Kabul edildi (2026-08-05)
**Kapsam:** Faz 1'e eklenen yeni MVP hedefi — web üzerinden online sipariş alma.

## Bağlam

Roadmap'teki Faz 1 tamamen personel-odaklı (POS, KDS, adisyon). Müşteriye dönük
auth'suz hiçbir yüzey yok. İş hedefi değişti: MVP'de müşteri kendi telefonundan
sipariş verebilmeli. Backend omurgası (catalog CRUD, pos order/check/KDS,
payment nakit akışı) olgun; eksik olan yalnızca public yüzey.

## Karar

### 1. Kapsam: QR ile masa-bazlı dine-in sipariş
- Müşteri masadaki QR'ı okutur → menü → sepet → sipariş → KDS'e düşer →
  personel accept/reject → ödeme **kasada** (mevcut akış).
- **Kapsam dışı (MVP):** delivery/adres teslim, online ödeme (PayTR — Faz 2,
  ADR uyumlu), müşteri hesabı/üyelik, kupon/kampanya, bahşiş.

### 2. Yeni modül: `storefront`
- `backend/internal/modules/storefront/` — kapalı kutu, diğer modüllere yalnız
  `public/` interface ile erişir:
  - `catalog/public` → menü okuma (yeni dar read-model metodu eklenecek)
  - `pos/public` → order place (source alanı `online_qr` ile)
- Modüller arası DB erişimi yok; go-arch-lint kuralları aynen geçerli.

### 3. HTTP yüzeyi: `/api/public/v1/*`
- Ayrı router grubu; Keycloak JWT middleware **yok**, OPA **yok** (principal yok).
- Kendi guard zinciri: QR token çözümleme → guest session → rate limit (OPS-003).
- Sipariş POST'unda `Idempotency-Key` **zorunlu** (SEC-003 aynen geçerli).
- **QR token API path'inde asla taşınmaz** — otelhttp ham path'i span attribute
  olarak yazar (webhook secret emsali); session ucu token'ı istek gövdesinde
  alır ve `/api/public/v1/*` prefix'i tracing'den hariç tutulur.

### 4. QR token: DB-backed opak token
- Tablo: `storefront_qr_codes(id, tenant_id, branch_id, table_id, token_hash, status, ...)`
  — ham token rastgele ≥256 bit, **DB'ye asla yazılmaz**; yalnız SHA-256 hex
  hash'i saklanır (unique index). Ham token üretim/rotate cevabında bir kez döner.
- Statik QR basılabilir; iptal DB'den yapılır (HMAC-imzalı token revoke
  edilemezdi, bu yüzden reddedildi). Rotate = eski satır revoke + yeni satır.
- **RLS bootstrap çözümü (satır-kapsamlı policy):** token→tenant çözümlemesi
  tenant bilinmeden yapılmak zorunda. `app.storefront_qr_token_hash` GUC'unu
  taşıyan ayrı bir `FOR SELECT` policy dalı ile bootstrap okuması **yalnızca o
  hash'e sahip tek satırı** açar (mevcut `all_tenants` scope deseninin dar
  varyantı). SECURITY DEFINER reddedildi: fonksiyon sahibi `app_migrator`
  BYPASSRLS taşıyor, yüzey gereksiz genişlerdi. Go tarafında tek bootstrap yolu
  `platform/db.WithQRTokenLookupTx()` — read-only tx, `app.tenant_id`'yi asla
  set etmez. Policy'de `status` kontrolü yapılmaz (revoke ayrımı servis
  katmanında; denetim izi korunur). Lookup sonrası tüm sorgular normal
  `WithTenantTx()` ile koşar. Bu istisna yalnız bu tabloya özgüdür ve
  genişletilemez.

### 5. Guest session
- QR token doğrulanınca kısa ömürlü (ör. 4 saat) imzalı guest JWT üretilir
  (claims: tenant_id, branch_id, table_id, qr_code_id) — HttpOnly cookie.
- Sipariş POST ve sipariş durumu sorgusu bu session ile yapılır; guest JWT
  hiçbir staff endpoint'inde geçerli değildir (ayrı imza anahtarı/audience).

### 6. Müşteriye dönük dar DTO (b2b dersi)
- Menü read-model'i yalnız şunları döner: kategori/ürün id-ad-açıklama, satış
  fiyatı, görsel, alerjen, müsaitlik, modifier seçenekleri + fiyat farkı.
- `cost_price`, stok miktarı, tedarikçi, iç notlar **asla** seçilmez — dar SQL
  query (SELECT *, geniş domain struct yasak).
- **Fiyat sunucuda yeniden hesaplanır:** sipariş POST'unda istemciden gelen
  fiyat tamamen yok sayılır; birim fiyat aynı dar read-model'den
  (`menu_items.price_override ?? products.price_amount`) yeniden türetilir.

### 7. Frontend: `web/apps/menu`
- Yeni Next.js 16 App Router uygulaması; `web/packages/*` ortak paketlerini
  kullanır, `apps/admin`'den import **edemez** (ARCH-005).
- Akış: `/q/{token}` → menü (SSR + kısa cache) → sepet (client state) →
  sipariş → durum sayfası (polling; SSE iyileştirmesi sonraya).

### 8. Sipariş yaşam döngüsü
- Storefront order'ı mevcut pos order/check akışına bağlanır; yeni bir order
  makinesi **yazılmaz**. `pos/public`'e principal almayan, adlandırılmış ayrı
  bir `GuestOrderPlacer` giriş noktası eklenir (nil-principal ile
  `OrderService.Place` çağırmak yasak). KDS ve adisyon entegrasyonu mevcut
  davranışı aynen kullanır.
- **Misafir adisyonu:** `checks.opened_by` nullable yapılır ve açık
  `opened_by_kind ('staff'|'guest_qr')` kolonu eklenir (CHECK ile tutarlılık
  zorlanır). Sentinel/sihirli UUID yasak. Masanın açık adisyonu varsa sipariş
  ona eklenir; yoksa `guest_qr` kaynaklı yeni adisyon açılır.
- **Kaynak ayrımı:** `orders` ve `checks` tablolarına `source ('pos'|'online_qr')`
  kolonu eklenir — `order_channel` (dine_in/takeaway/delivery) kanaldır, kaynak
  değildir; karıştırılmaz. QR siparişi `order_channel='dine_in'` +
  `source='online_qr'`.
- **Misafir sipariş görünürlüğü:** `storefront_guest_orders` bağ tablosu
  (order_id ↔ guest_session_id) — misafir yalnızca kendi guest session'ıyla
  verilmiş siparişlerin durumunu okuyabilir; ham order_id ile okuma yok.
- Fiscal: sipariş kasada kapandığında mevcut payment/fiscal akışı devreye girer;
  storefront fiscal'a dokunmaz (FISCAL-001 ihlal edilmez).

## Sonuçlar

- ROADMAP Faz 1 kapsamına "online sipariş (QR dine-in)" eklenir; PayTR web
  ödemesi Faz 2'de bu modülün üstüne gelir.
- Yeni public yüzey en riskli auth yüzeyidir: RLS sızıntı testleri
  (`task backend:test:rls-leak`) ve invariant testleri bu modül için zorunlu
  kabul kriteridir (lessons-from-b2b #1-#2).
- Edge/offline senaryosu (şube interneti koptuğunda online sipariş) MVP'de
  kapsam dışı; sipariş alınamazsa müşteriye açık hata gösterilir.
