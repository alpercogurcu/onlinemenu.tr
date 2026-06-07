# Omnichannel Integration Fabric

**Tarih:** 2026-06-08  
**Durum:** Onaylandı  
**Kapsam:** Faz 1 + Faz 2 hazırlığı  

---

## Özet

Platformu SAP benzeri genişletilebilir bir ticaret altyapısına dönüştüren mimari tasarım. Merkezi değer: her domain modülü kendi harici entegrasyonuna sahip, `platform/integration` ortak altyapıyı sağlar. Delivery (Yemeksepeti/Getir/Trendyol), e-fatura, ÖKC/POS terminal ve catalog sync entegrasyonları bu çerçevede büyür. Yarın yeni bir platform eklemek → tek adapter paketi, başka dokunuş yok.

---

## 1. Mimari Yaklaşım

### Temel Karar: Per-Module Ownership

Her domain modülü kendi harici entegrasyonlarını barındırır. Ortak altyapı `platform/integration` paketinde yaşar.

```
billing  modülü → BillingAdapter  → EDM, Paraşüt, İzibiz
delivery modülü → DeliveryAdapter → Yemeksepeti, Getir, Trendyol
catalog  modülü → CatalogSyncAdapter → Google Business, QR menü
payment  modülü → FiscalAdapter + TerminalAdapter → ÖKC, POS terminali
```

**Neden bu yaklaşım:**
- Modül izolasyonu korunur — billing kodu delivery'den habersiz
- Domain bilgisi (fatura formatı, sipariş yapısı) doğru modülde kalır
- Faz 4 mikroservis geçişinde entegrasyon kodu domain koduyla birlikte taşınır
- `public/` interface → bugün in-process çağrı, yarın gRPC/HTTP transport

### 3 Entegrasyon Modu

| Mod | Çalışma Şekli | Kimler Kullanır |
|---|---|---|
| **Routing** | Kural bazlı, 1 kazanan seçilir | billing (B2C → EDM, B2B → Paraşüt) |
| **Broadcast** | Tüm aktif adapter'lara aynı anda | delivery (YS + Getir + Trendyol), catalog sync |
| **Device Registry** | Fiziksel cihaz kaydı, serial no bazlı | payment terminali, ÖKC |

### Katman Şeması

```
İstemciler
  Wails POS (kasa) · Wails POS (garson) · KDS · Next.js Admin
        ↕ WebSocket / REST
API Katmanı
  api-core · api-pos · api-finance · api-delivery (yeni)
        ↕ NATS JetStream
Domain Modülleri
  identity · tenant · catalog · pos · payment · billing · delivery (yeni) · inventory · ...
        ↕ platform interfaces
Platform Katmanı
  db/RLS · auth/OPA · eventbus/NATS · vault · cache · otel · httpx
  platform/integration (yeni) ← WebhookRouter, AdapterFactory, CircuitBreaker, RetryPolicy, RateLimiter
        ↕ adapters
Harici Sistemler
  Yemeksepeti · Getir · Trendyol · EDM · İzibiz · Paraşüt · Google Business · ÖKC · POS Terminal
```

---

## 2. platform/integration Paketi

`backend/internal/platform/integration/` — yeni paket, tüm modüllerin harici servislere bağlandığı ortak altyapı.

### Temel Tipler

```go
// provider.go
type ProviderConfig struct {
    Provider        string
    TenantID        uuid.UUID
    BranchID        *uuid.UUID     // nil = tenant geneli, dolu = şube override
    Config          map[string]any // hassas olmayan config (JSONB'den)
    VaultSecretPath string         // Vault'taki API credentials
    Environment     string         // "test" | "production"
    IsActive        bool
}

// factory.go
type AdapterFactory[T any] func(cfg ProviderConfig, creds map[string]string) (T, error)

type Registry[T any] struct { /* ... */ }
func (r *Registry[T]) Register(provider string, factory AdapterFactory[T])
func (r *Registry[T]) Build(cfg ProviderConfig, vault VaultClient) (T, error)
```

### Altyapı Bileşenleri

```go
// webhook.go
type WebhookRouter struct { /* ... */ }
func (r *WebhookRouter) Handle(w http.ResponseWriter, req *http.Request)
// Endpoint: POST /webhooks/{integration_type}/{integrator_id}
// HMAC/bearer doğrulama provider'a göre WebhookVerifier'dan gelir.

// retry.go
type RetryPolicy struct {
    MaxAttempts int
    // Backoff: min(60s, 2^attempt + jitter(0-1s))
}

// circuit.go
type CircuitBreaker struct {
    FailureThreshold int   // 5 hata → open
    RecoveryTimeout  time.Duration
}

// ratelimit.go
type RateLimiter struct {
    // Per-provider, tenant-aware
    // Redis token bucket
}
```

---

## 3. Order Channel Mimarisi

### `orders` Tablosu — Yeni Alanlar

```sql
order_channel           TEXT NOT NULL
  CHECK (order_channel IN ('dine_in','takeaway','click_collect','delivery')),
delivery_integrator_id  UUID REFERENCES branch_delivery_integrators(id),
external_order_id       TEXT,          -- Yemeksepeti/Getir'in kendi sipariş no'su
external_order_ref      JSONB,         -- platformdan gelen ham veri (audit)
delivery_address        JSONB,         -- kurye adresi (sadece delivery)
channel_commission      NUMERIC(12,2), -- platform komisyonu (raporlama)
accept_deadline_at      TIMESTAMPTZ,   -- zaman limitli kabul için
accepted_at             TIMESTAMPTZ,
accepted_by             UUID           -- NULL = oto-kabul, dolu = kasiyerin kabulü
```

**`order_channel` neden TEXT + CHECK, lookup tablosu değil:**
- 4 sabit değer, domain sabiti — `order_status` gibi
- Harici platforma ait detay `delivery_integrator_id` FK'sında
- Composite/partial index ile yeterli performans:
  ```sql
  CREATE INDEX idx_orders_delivery ON orders(branch_id, created_at)
    WHERE order_channel = 'delivery';
  CREATE INDEX idx_orders_channel_date ON orders(tenant_id, order_channel, created_at);
  ```

### Sipariş Kanalları

| Kanal | `table_id` | `delivery_integrator_id` | Açıklama |
|---|---|---|---|
| `dine_in` | dolu | NULL | Masada oturan müşteri |
| `takeaway` | NULL | NULL | Kasadan gel-al |
| `click_collect` | NULL | NULL | Online ödenmiş, gelip alacak |
| `delivery` | NULL | dolu | Yemeksepeti/Getir/Trendyol |

---

## 4. Delivery Modülü

### İç Yapı

```
backend/internal/modules/delivery/
├── public/
│   ├── adapter.go      ← DeliveryAdapter interface
│   └── types.go        ← ExternalOrder, ProductAvailability, WebhookEvent, ...
├── domain/
│   ├── order.go        ← DeliveryOrder aggregate
│   └── accept_timer.go ← zaman limitli kabul mantığı
├── repo/
│   └── integrator_repo.go
├── http/
│   ├── webhook.go      ← inbound webhook handler'ları
│   └── routes.go
├── events/
│   ├── publisher.go    ← delivery.order.received.v1, accepted, rejected
│   └── subscriber.go   ← catalog.product.updated.v1, availability.changed, branch_hours.updated
├── adapter/
│   ├── yemeksepeti/    ← YemeksepatiAdapter implements DeliveryAdapter
│   ├── getir/          ← GetirAdapter implements DeliveryAdapter
│   └── trendyol/       ← TrendyolAdapter implements DeliveryAdapter
└── module.go           ← fx.Module
```

### DeliveryAdapter Interface

```go
// delivery/public/adapter.go
type DeliveryAdapter interface {
    // Inbound
    ParseWebhook(body []byte, headers map[string]string) (WebhookEvent, error)
    ConfirmOrder(ctx context.Context, externalID string) error
    RejectOrder(ctx context.Context, externalID, reason string) error

    // Outbound
    UpdateAvailability(ctx context.Context, products []ProductAvailability) error
    SyncMenu(ctx context.Context, snapshot CatalogSnapshot) error
    UpdateHours(ctx context.Context, hours BranchHours) error
}
```

### Inbound Sipariş Akışı

```
1. POST /webhooks/delivery/{integrator_id}
   └─ platform/integration.WebhookRouter
      └─ HMAC/bearer doğrulama (provider'a göre)
      └─ delivery.order.received.v1 NATS event

2. delivery consumer
   └─ orders tablosuna yazar
      order_channel = 'delivery'
      accept_deadline_at = now() + platform_timeout (YS: 90s, Getir: 60s, Trendyol: 75s)
      status = 'pending_accept'

3. WebSocket → ws/branch/{id}/role/kasa
   └─ sadece 'kasa' rolündeki cihazlar bildirim alır
   └─ garson tableti ve KDS almaz

4. Zaman limitli oto-kabul
   ├─ Kasiyer kabul eder → accepted_by = user_id
   ├─ Kasiyer reddeder → delivery.order.rejected.v1 → adapter.RejectOrder
   └─ Süre dolar  → accepted_by = NULL (oto-kabul)

5. Kabul sonrası
   └─ status = 'preparing'
   └─ pos.order.created.v1 → KDS'e düşer
      KDS kartı başlığı: platform adı + ikonu
      Mutfak fişine platform adı basılır (kurye paketi zımbalama)
```

### Outbound Catalog Sync Tetikleyicileri

| NATS Event | Tetikleyen Aksiyon |
|---|---|
| `catalog.product_availability.changed.v1` | `UpdateAvailability` → aktif tüm delivery platformları (broadcast). "Tüm platformlarda kapat" = 3 ayrı satır yazar, admin panel bunu tek işlemle yapabilir. |
| `catalog.product.updated.v1` | `SyncMenu` → fiyat/isim değişikliği |
| `catalog.image.updated.v1` | `SyncMenu` → görsel güncellemesi |
| `tenant.branch_hours.updated.v1` | `UpdateHours` → tüm aktif platformlar |

**Rate limiting:** Her platform için `platform/integration.RateLimiter` devrede. Aynı anda 50 ürün güncellendiyse → batch queue → sırayla gönderilir.

### `branch_delivery_integrators` Tablosu

```sql
CREATE TABLE branch_delivery_integrators (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id        UUID NOT NULL,
    branch_id        UUID NOT NULL,           -- delivery her zaman şube bazında
    provider         TEXT NOT NULL,           -- 'yemeksepeti' | 'getir' | 'trendyol'
    display_name     TEXT NOT NULL,
    config           JSONB NOT NULL DEFAULT '{}', -- hassas olmayan config
    vault_secret_path TEXT NOT NULL,          -- API credentials Vault'ta
    environment      TEXT NOT NULL DEFAULT 'production',
    external_store_id TEXT NOT NULL,          -- platformdaki mağaza kimliği
    menu_sync_mode   TEXT NOT NULL DEFAULT 'realtime', -- 'realtime' | 'scheduled'
    is_active        BOOL NOT NULL DEFAULT TRUE,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (branch_id, provider)
);
```

---

## 5. Catalog Per-Channel Availability

### `product_channel_availability` Tablosu (Yeni)

```sql
CREATE TABLE product_channel_availability (
    product_id   UUID NOT NULL,
    branch_id    UUID NOT NULL,
    channel      TEXT NOT NULL, -- 'pos' | 'click_collect' | 'yemeksepeti' | 'getir' | 'trendyol'
                               -- 'delivery' generic değil, platform bazında granüler
    is_available BOOL NOT NULL DEFAULT TRUE,
    changed_by   UUID,          -- audit: kim kapattı (NULL = sistem/auto-close)
    changed_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (product_id, branch_id, channel)
);
```

**"Closed set" yaklaşımı:** Sadece kapalı veya override edilmiş ürünler için satır yazılır. Kayıt yoksa = açık. Insert sayısı minimumda kalır.

### `products` Tablosuna Ek Alan

```sql
ALTER TABLE products
    ADD COLUMN auto_close_on_zero_stock BOOL NOT NULL DEFAULT FALSE;
```

### Öncelik Sırası

1. **Manuel kapatma** (`is_available = false`) — her zaman kazanır
2. **Auto-close** (`auto_close_on_zero_stock = true AND stock ≤ 0`) — manuel açık override eder
3. **Varsayılan** (kayıt yok) — açık

**Neden `auto_close_on_zero_stock` default false:** Restoran ortamında stok hataları yaygın. Negatif stok oluştuğunda tüm platformlarda ürün kapanmasını önlemek için şube bazında karar verilir.

### Yetki

| Rol | `product.availability.write` |
|---|---|
| `kasa` | ✓ |
| `mutfak` | ✓ — KDS üzerinden anlık kapatma |
| `garson` | ✗ |
| `admin` / `müdür` | ✓ |

### UI Metadata (Go side, DB'ye girmiyor)

```go
var ChannelMeta = map[string]ChannelUI{
    "dine_in":       {Icon: "🪑", Color: "#4a9eff", Label: "Masa"},
    "takeaway":      {Icon: "🛍️", Color: "#f39c12", Label: "Gel-Al"},
    "click_collect": {Icon: "📱", Color: "#1abc9c", Label: "Click & Collect"},
    "delivery":      {Icon: "🛵", Color: "#e74c3c", Label: "Delivery"},
}
```

---

## 6. Device Role Sistemi

### `devices` Tablosu

```sql
CREATE TABLE devices (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id    UUID NOT NULL,
    branch_id    UUID NOT NULL,
    name         TEXT NOT NULL,        -- "Kasa 1", "Mutfak Ekranı", "Garson 3"
    roles        TEXT[] NOT NULL,      -- ['kasa'] | ['garson'] | ['mutfak'] | ['kasa','garson']
    device_type  TEXT NOT NULL,        -- 'tablet' | 'kds_screen' | 'desktop' | 'pos_terminal'
    is_active    BOOL NOT NULL DEFAULT TRUE,
    last_seen_at TIMESTAMPTZ,          -- heartbeat
    -- SEC-004 pairing alanları: fingerprint, pairing_code_hash, pairing_expires_at,
    --                           last_token_rotated_at, revoked_at, revoke_reason
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

### MVP Rolleri

| Rol | Alınan Bildirimler | Yapabilecekleri |
|---|---|---|
| `kasa` | Delivery accept bildirimi, ödeme olayları | Kabul/red, ödeme, gün açma/kapama, kasa raporu |
| `garson` | Masa/adisyon güncellemeleri | Sipariş alma, masa yönetimi |
| `mutfak` | Yeni sipariş (KDS), availability event | KDS görüntüleme, ürün kapatma/açma |

Tek cihaz birden fazla rol taşıyabilir: küçük işletmelerde `['kasa', 'garson']`.

### WebSocket Role-Based Routing

```
ws://api-pos/branch/{branch_id}/role/{role}

delivery.order.received.v1   → ws/.../role/kasa    (garson ve mutfak almaz)
pos.order.created.v1         → ws/.../role/mutfak  (kanal bilgisiyle)
catalog.availability.changed → ws/.../role/kasa + ws/.../role/garson
```

---

## 7. Raporlama

### Ay Sonu Ciro Raporu Yapısı

```
orders tablosu + delivery_integrator_id JOIN branch_delivery_integrators

GROUP BY order_channel, provider (integrators.provider)

Brüt ciro   = SUM(total_amount)
Komisyon    = SUM(channel_commission)   ← platformun kestiği
Net ciro    = brüt - komisyon

Çıktı:
  dine_in         → 45.200 ₺
  takeaway        → 8.400 ₺
  click_collect   → 3.600 ₺
  delivery / yemeksepeti → 12.800 ₺  (-1.920 ₺ komisyon)
  delivery / getir       → 9.100 ₺   (-1.365 ₺ komisyon)
  delivery / trendyol    → 6.300 ₺   (-945 ₺ komisyon)
```

---

## 8. Event Sözleşmeleri (Yeni)

```
contracts/events/delivery/
├── delivery.order.received.v1.json
├── delivery.order.accepted.v1.json
├── delivery.order.rejected.v1.json
└── delivery.order.completed.v1.json

contracts/events/catalog/
└── catalog.product_availability.changed.v1.json  (yeni)
```

---

## 9. Kapsam Sınırı

### MVP'de Var

- `delivery` modülü tam yapısı (interface + adapter iskeletleri + event sözleşmeleri)
- `platform/integration` temel tipler (`ProviderConfig`, `AdapterFactory[T]`, `WebhookRouter`, `RetryPolicy`, `CircuitBreaker`, `RateLimiter`)
- `order_channel` alanı ve CHECK constraint
- `product_channel_availability` tablosu
- `devices` tablosu + MVP rolleri (`kasa`, `garson`, `mutfak`)
- WebSocket role-based routing
- Zaman limitli oto-kabul akışı
- KDS kanal bilgisi + mutfak fişi platform adı
- Raporlama alanları (`channel_commission`, `accepted_by`)

### MVP Dışı

| Özellik | Faz |
|---|---|
| Gerçek YS/Getir/Trendyol SDK entegrasyonu | 2 |
| İskonto / kampanya kural motoru | 2+ |
| Royalty hesap motoru (franchise) | 2+ |
| Bar cihaz rolü | İhtiyaç olunca |
| Click&collect online ödeme (PayTR) | 2 |
| Tenant-specific OPA policy override | 2+ |
| Typesense arama | 2 |
| Google Business gerçek entegrasyonu | 2 |

---

## 10. Etkilenen Mevcut ADR'lar

| ADR | Değişiklik |
|---|---|
| FISCAL-001 | `FiscalDeviceAdapter` pattern'i bu tasarımla uyumlu, değişmez |
| SEC-004 | `devices` tablosu burada tanımlandı, SEC-004 pairing alanlarını extend eder |
| DATA-001 | `delivery_outbox` tablosu outbox pattern'ini takip eder |
| DATA-002 | Delivery eventleri immutable, `ON CONFLICT DO NOTHING` kuralı geçerli |

## 11. Yeni ADR Gerektiren Kararlar

| Karar | Önerilen ADR |
|---|---|
| Delivery adapter interface + broadcast modu | INT-001 — External Integration Adapter Architecture |
| Order channel modeli + per-channel availability | Mevcut pos/catalog modülü spec'ine ek |
| Device role sistemi | SEC-004'ün implementasyon detaylarına ek |
