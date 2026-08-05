# WP2 — Public Storefront API Sözleşmesi (`/api/public/v1`)

> Kaynak karar: `docs/adr/ARCH-006-online-ordering-storefront.md`
> Plan: `docs/plans/2026-08-05-online-order-mvp.md` (WP2)
> Bu dosya WP4 (`web/apps/menu`) için bağlayıcı istek/cevap sözleşmesidir.

## Genel kurallar

- Prefix `/api/public/v1` **staff auth zincirinden muaftır** (`cmd/api/main.go`).
  Muafiyet "auth yok" demek değildir: guard zinciri
  `storefront/http.RegisterPublicRoutes` içindedir —
  IP rate limit → guest cookie → session rate limit → idempotency.
- Kimlik yalnız `om_guest` çerezidir. `Authorization` header'ı bu yüzeyde
  **hiç okunmaz**; staff token'ı buraya konsa da 401 alır.
- Çerez: `HttpOnly; Path=/api/public/v1; SameSite=Lax; Secure` (dev hariç),
  ömrü 4 saat. `withCredentials: true` zorunlu.
  **SameSite=Lax gereği menu app ile API aynı kayıtlı alan adını paylaşmalı**
  (port farkı sorun değil: dev `:3001` → `:8080` çalışır;
  `menu.onlinemenu.tr` → `api.onlinemenu.tr` çalışır; tamamen ayrı bir apex
  alan adı **çalışmaz**).
  `/q/[token]` sunucu bileşeninde session POST'u sunucu tarafında yapılırsa
  gelen `Set-Cookie` tarayıcı cevabına **aktarılmalıdır**.
- Tüm hatalar RFC 7807 (`application/problem+json`):
  `{type, title, status, detail, code}`. İstemci **`code`** alanına dallanır,
  `detail` (Türkçe, kullanıcıya gösterilir) değişebilir.
- Rate limit başlıkları: `X-RateLimit-Limit`, `X-RateLimit-Remaining`,
  `X-RateLimit-Reset`; 429'da ayrıca `Retry-After` (saniye).
  Varsayılanlar: IP 120 istek/dk, guest oturumu 60 istek/dk.

## 1. `POST /api/public/v1/sessions` — QR tokenını oturuma çevir

Auth: yok. Token **gövdede**, path'te asla (D1 — trace/Referer sızıntısı).

```jsonc
// istek
{ "token": "<ham QR token>" }
```
```jsonc
// 200 + Set-Cookie: om_guest=...
{
  "branch_id": "uuid",
  "table_id": "uuid",
  "table_label": "Masa 4",
  "expires_at": "2026-08-05T23:00:00Z"
}
```
Hatalar: `404 not_found` (geçersiz / iptal edilmiş / masası silinmiş ya da
başka şubeye taşınmış kod — ayrım **kasıtlı olarak** dışarı verilmez),
`400 invalid_request`, `429`, `503`.

> **Şube adı yoktur.** `storefront` modülü go-arch-lint gereği yalnız
> `catalog/public` + `pos/public`'e bağlanabilir; şube meta verisi hiçbirinde
> yok. UI masa etiketini gösterir.

## 2. `GET /api/public/v1/menu`

Auth: `om_guest` çerezi. `Cache-Control: private, max-age=30`.

```jsonc
{
  "categories": [{
    "id": "uuid",             // "0000...0000" = kategorisiz kova
    "name": "Sıcak İçecekler",// "" ise UI "Diğer" yazar
    "sort_order": 1,
    "products": [{
      "id": "uuid",
      "name": "Latte",
      "description": "Espresso ve süt",
      "price_amount": 4500,   // kuruş, menü override'ı uygulanmış
      "currency": "TRY",
      "image_key": "products/latte.jpg", // OBJE ANAHTARI, URL DEĞİL
      "allergens": [],        // şemada kolon yok — her zaman boş
      "is_available": true,   // false = menüde soluk göster, sipariş edilemez
      "modifier_groups": [{
        "id": "uuid",
        "name": "Ekstralar",
        "selection_type": "single" | "multiple",
        "min_select": 0,
        "max_select": 0,      // 0 = üst sınır yok (yalnız "multiple" için)
        "modifiers": [{ "id": "uuid", "name": "Ekstra shot", "price_delta": 500 }]
      }]
    }]
  }]
}
```

- `image_key` MinIO nesne anahtarıdır; URL'i menu app kendi medya tabanıyla
  kurar (imzalı/CDN URL üretimi henüz hiçbir yerde yok).
- **`selection_type: "single"` grubunda sunucu her zaman en fazla 1 seçim
  kabul eder — `max_select` 0 (sınırsız) olsa bile.** Çoklu seçim arayüzü
  kurulursa sipariş 422 ile reddedilir.
- Tüm diziler `null` değil `[]` döner.
- Maliyet, stok, tedarikçi, iç not, SKU, barkod, vergi oranı **hiçbir zaman
  dönmez** — DTO'da alanı yok (`menu_dto_test.go` ham gövdede arıyor).

## 3. `POST /api/public/v1/orders`

Auth: `om_guest`. **`Idempotency-Key` header'ı zorunlu** (ADR-SEC-003).
Aynı key + aynı gövde → ilk cevabın replay'i (`Idempotency-Replayed: true`).
Aynı key + farklı gövde → 422.

```jsonc
// istek — FİYAT ALANI YOKTUR
{
  "lines": [{
    "product_id": "uuid",
    "quantity": 2,
    "modifier_ids": ["uuid"],   // aynı id iki kez gönderilemez
    "note": "az şekerli"
  }],
  "note": "Servis 10 dk sonra"
}
```
```jsonc
// 201
{ "order_id": "uuid", "check_id": "uuid", "status": "pending", "total": 9000 }
```

- İstemcinin gönderdiği her fiyat **yok sayılır** (DTO'da karşılığı yok);
  birim fiyat sunucuda `menu_items.price_override ?? products.price_amount`
  + seçilen modifier delta'ları ile yeniden hesaplanır.
- Sınırlar: gövde ≤ 64 KiB, ≤ 60 kalem, kalem adedi 1–99, kalem başına ≤ 20
  modifier, not ≤ 500 karakter → aşılırsa `422 validation_failed`.
- Modifier isimleri sipariş kaleminin notuna yazılır (`order_items`'ta
  modifier kolonu yok; delta zaten birim fiyata girer).

Hatalar:

| Durum | Kod | `code` |
|---|---|---|
| Boş/geçersiz sepet, sipariş edilemeyen ürün, geçersiz modifier | 422 | `validation_failed` |
| Aynı `modifier_id` bir kalemde birden fazla kez, ya da `single` grubundan >1 seçim | 422 | `validation_failed` |
| Masa temizleniyor | 409 | `table_not_ready` |
| Masa sipariş alamaz durumda | 409 | `table_occupied` |
| QR artık bu masaya ait değil | 409 | `table_branch_mismatch` |
| Idempotency-Key yok / aynı key farklı gövde | 422 | — (düz metin) |
| Aynı key hâlâ işleniyor | 409 | — |

## 4. `GET /api/public/v1/orders` ve `GET /api/public/v1/orders/{id}`

Auth: `om_guest`. Yalnız **bu oturumun** siparişleri görünür; başkasının
`order_id`'si `404 not_found` (403 değil — varlık doğrulanamamalı).

```jsonc
// GET /orders
{ "orders": [ /* aşağıdaki nesneden */ ] }

// GET /orders/{id}
{
  "order_id": "uuid",
  "status": "pending" | "accepted" | "preparing" | "ready" | "delivered" | "rejected" | "cancelled",
  "note": "",
  "items": [{ "name": "Latte", "quantity": 2, "unit_price_amount": 4500, "note": "Ekstra shot | az şekerli" }],
  "total": 9000,
  "created_at": "...",
  "updated_at": "..."
}
```

Durum takibi MVP'de polling ile yapılır (öneri: 5 sn). SSE/WS sonraya.

## Bilinçli sınırlar (WP4 bunlara güvenmemeli)

- `min_select` sunucuda **zorlanmaz**; zorunlu grup kontrolü istemci
  tarafındadır (bayat katalog konfigürasyonu siparişi tamamen bloklamasın).
- Redis erişilemezse rate limiter **fail-closed 503** döner ve bu `/sessions`
  dahil tüm yüzeyi kapatır.
- `/api/public/v1/*` tracing'den tamamen hariçtir (D1); bu yüzeyde gözlem
  yalnızca log + metrik üzerinden.
