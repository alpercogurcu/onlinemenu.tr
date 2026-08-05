# ARCH-006 — QR Dine-in Online Sipariş MVP: Dosya-Bazlı İmplementasyon Planı

> Bağlayıcı karar dokümanı: `docs/adr/ARCH-006-online-ordering-storefront.md`
> (plan notları N1-N4 ADR'ye işlendi). Her iş paketi (WP) bağımsız bir
> implementasyon oturumuna verilebilecek netliktedir. İmplementasyona başlamadan
> önce bu dosyanın TAMAMI ve ADR okunmalıdır.

## 0. Plan notları — bağlayıcı kararlar

### N1. RLS token-lookup: GUC'un token hash'ini taşıdığı dar SELECT policy'si

Emsal: `app.tenant_scope = 'all_tenants'` GUC'u + `WithAllTenantsReadTx`
(`backend/internal/platform/db/tenant_tx.go:105-177`, policy:
`backend/migrations/identity/000008_persons_memberships_all_tenants_scope.up.sql`).
`all_tenants`'ı tekrar kullanmak YASAK — o dal tüm tabloyu açar. Bunun yerine:

```sql
CREATE POLICY qr_codes_read ON storefront_qr_codes FOR SELECT TO app_runtime
    USING (
        tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid
        OR token_hash = NULLIF(current_setting('app.storefront_qr_token_hash', TRUE), '')
    );

CREATE POLICY qr_codes_write ON storefront_qr_codes FOR ALL TO app_runtime
    USING      (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid);
```

(FOR SELECT + FOR ALL ikilisi `menus_read`/`menus_write` deseni —
`backend/migrations/catalog/000001_create_catalog.up.sql:196-200`.)

- SECURITY DEFINER reddedildi: fonksiyon sahibi `app_migrator` BYPASSRLS taşıyor
  (`deploy/postgres/init.sql:13`) — yüzey gereksiz genişler.
- `token_hash TEXT NOT NULL` (lowercase hex SHA-256) + UNIQUE. **Ham token DB'ye
  asla yazılmaz** — üretim/rotate cevabında bir kez döner.
- Policy'ye `status = 'active'` KOYMA — revoke kontrolü servis katmanında.
- Platform metodu read-only tx açar, `app.tenant_id`'yi ASLA set etmez.

### N2. `checks.opened_by NOT NULL` — misafir adisyonu

`checks.opened_by UUID NOT NULL` (`backend/migrations/pos/000001_create_pos.up.sql:12`);
`CheckService.Open` principal istiyor (`check_service.go:94-96`). "check_id NULL
kalsın, accept'te bağlansın" alternatifi REDDEDİLDİ: adisyon toplamı yalnız
`check_id` dolu order'lardan hesaplanıyor (`CheckRepo.GetTotal` →
`TotalsByCheckIDs`), `Close` buna kilitli — NULL check'li sipariş kasada tahsil
edilemez. Çözüm (`pos/000006`):

```sql
ALTER TABLE checks ADD COLUMN opened_by_kind TEXT NOT NULL DEFAULT 'staff'
    CHECK (opened_by_kind IN ('staff', 'guest_qr'));
ALTER TABLE checks ALTER COLUMN opened_by DROP NOT NULL;
ALTER TABLE checks ADD CONSTRAINT checks_opened_by_kind_chk
    CHECK ((opened_by_kind = 'staff') = (opened_by IS NOT NULL));
```

`uuid.Nil` / "sistem person" sentinel YASAK. Blast radius: `pos/domain/check.go:43`
(`OpenedBy` → `*uuid.UUID` + `OpenedByKind`), `pos/repo/check_repo.go` (INSERT
:29-35, `scanCheck` :240, RETURNING :31/:50/:71/:103/:220),
`pos/http/handler.go:191`, `pos/service/check_service.go:136` (outbox payload).

### N3. `orders.source` — `order_channel` ile karıştırma

`order_channel` (dine_in|takeaway|delivery) kanaldır. `source ('pos'|'online_qr')`
ayrı kolon — hem `orders` hem `checks`. QR siparişi: `order_channel='dine_in'` +
`source='online_qr'`.

### N4. Repo gerçekleri

- **Repoda sqlc YOK** — elle yazılmış pgx repo deseni (`pgx.Tx` alan metotlar,
  `pos/repo/order_repo.go:21`). CLAUDE.md tech-stack tablosundaki "sqlc" satırı
  drift, düzeltilecek.
- **OPS-003 rate limit implementasyonu SIFIR** — `platform/httpx`'te yalnız
  `idempotency.go` var. Sıfırdan yazılacak; OPS-003 fixed window'u REDDETMİŞ
  (satır 71) → Redis ZSET sliding-window log.
- **`web/packages/ui-kit` boş** (`export {}`) — WP4'te dolduruluyor.
- **Fiyat yeniden hesaplama:** istemci fiyatı tamamen yok sayılır; fiyat
  sunucuda `menu_items.price_override ?? products.price_amount`'tan türetilir.
- **Misafir sipariş görünürlüğü:** `storefront_guest_orders` bağ tablosu —
  misafir yalnız kendi session'ının order'larını okur.

## Bağımlılık sırası

```
WP1 (storefront çekirdek + migration'lar + guest token + platform db metodu)
 ├─► WP2 (public HTTP; catalog/public + pos/public genişletmeleri)
 │     ├─► WP4 (web/apps/menu — WP2 sözleşmesine bağlı)
 │     └─► WP5 (test & kalite — WP1+WP2 bitince; testler WP'lerle birlikte yazılır)
 └─► WP3 (admin QR CRUD — WP1'e bağlı, WP2'den bağımsız, paralel)
```

---

## WP1 — Backend: storefront modülü çekirdeği

### Migration'lar

- `backend/migrations/storefront/000001_create_storefront.up.sql` / `.down.sql`
- `backend/migrations/pos/000006_order_source_guest_check.up.sql` / `.down.sql`

`storefront/000001` (RLS deseni: `backend/migrations/pos/000004_create_table_plan.up.sql:17-26`):

```sql
CREATE TABLE storefront_qr_codes (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   UUID        NOT NULL,
    branch_id   UUID        NOT NULL,       -- modüller arası: FK YOK
    table_id    UUID        NOT NULL,       -- pos.tables: FK YOK
    table_label TEXT        NOT NULL DEFAULT '',
    token_hash  TEXT        NOT NULL,       -- lowercase hex SHA-256
    status      TEXT        NOT NULL DEFAULT 'active'
                            CHECK (status IN ('active', 'revoked')),
    created_by  UUID        NOT NULL,
    revoked_at  TIMESTAMPTZ,
    revoked_by  UUID,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX storefront_qr_codes_token_hash_uidx ON storefront_qr_codes (token_hash);
CREATE INDEX storefront_qr_codes_tenant_branch_idx ON storefront_qr_codes (tenant_id, branch_id);
CREATE UNIQUE INDEX storefront_qr_codes_active_table_uidx
    ON storefront_qr_codes (table_id) WHERE status = 'active';
-- + ENABLE/FORCE RLS, N1 policy'leri, GRANT SELECT, INSERT, UPDATE TO app_runtime

CREATE TABLE storefront_guest_orders (
    order_id           UUID        PRIMARY KEY,   -- pos.orders: FK YOK
    tenant_id          UUID        NOT NULL,
    qr_code_id         UUID        NOT NULL REFERENCES storefront_qr_codes (id) ON DELETE RESTRICT,
    guest_session_id   UUID        NOT NULL,
    check_id           UUID        NOT NULL,      -- pos.checks: FK YOK
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX storefront_guest_orders_session_idx ON storefront_guest_orders (tenant_id, guest_session_id);
-- + ENABLE/FORCE RLS, standart tenant_isolation policy, GRANT SELECT, INSERT
```

> **TUZAK — modül-ötesi FK yasağı.** `table_id`, `branch_id`, `order_id`,
> `check_id` pos'a ait: `REFERENCES` YAZMA. Gerekçe: `pos/000001:2` ve
> `identity/000011_drop_cross_module_fks.up.sql`. Varlık doğrulaması `pos/public`
> üzerinden.

`pos/000006`: N2 bloğu + N3 kolonları:

```sql
ALTER TABLE orders ADD COLUMN source TEXT NOT NULL DEFAULT 'pos'
    CHECK (source IN ('pos', 'online_qr'));
ALTER TABLE checks ADD COLUMN source TEXT NOT NULL DEFAULT 'pos'
    CHECK (source IN ('pos', 'online_qr'));
CREATE INDEX orders_source_idx ON orders (tenant_id, source) WHERE source <> 'pos';
```

`pos/000006` **down** migration'ı (D2): `opened_by SET NOT NULL`'a dönmeden önce
misafir kaynaklı veriyi silmek ZORUNDA, yoksa ilk rollback patlar. Açık uyarı
yorumuyla:

```sql
-- DİKKAT: bu rollback YIKICIDIR. opened_by NOT NULL'a geri dönebilmek için
-- misafir kaynaklı adisyonlar ve bağlı siparişler silinir; bu veriye
-- ihtiyaç varsa rollback öncesi yedek alın (deploy/backup/backup.sh).
DELETE FROM order_items WHERE order_id IN (
    SELECT id FROM orders WHERE check_id IN (
        SELECT id FROM checks WHERE opened_by_kind = 'guest_qr'));
DELETE FROM orders WHERE check_id IN (
    SELECT id FROM checks WHERE opened_by_kind = 'guest_qr');
DELETE FROM checks WHERE opened_by_kind = 'guest_qr';

ALTER TABLE checks DROP CONSTRAINT checks_opened_by_kind_chk;
ALTER TABLE checks ALTER COLUMN opened_by SET NOT NULL;
ALTER TABLE checks DROP COLUMN opened_by_kind;
ALTER TABLE checks DROP COLUMN source;
DROP INDEX orders_source_idx;
ALTER TABLE orders DROP COLUMN source;
```

### Değiştirilecek — unutulursa CI kırar

- `backend/cmd/migrate/main.go` — `moduleOrder` dizisine (satır ~22-30)
  `"storefront"` **pos'tan sonra**; yorumdaki sıra listesi de güncellenir.
- `backend/.go-arch-lint.yml` — yeni bileşenler + deps:
  ```yaml
  storefront_domain:  { mayDependOn: [platform, storefront_domain] }
  storefront_repo:    { mayDependOn: [platform, storefront_domain, storefront_repo] }
  storefront_service: { mayDependOn: [platform, storefront_domain, storefront_repo, storefront_public,
                                      catalog_public, pos_public, storefront_service] }
  storefront_http:    { mayDependOn: [platform, storefront_domain, storefront_service, storefront_public,
                                      catalog_public, pos_public, storefront_http] }
  storefront_public:  { mayDependOn: [platform, storefront_public] }
  ```

### Platform: dar bootstrap tx'i

`backend/internal/platform/db/qr_lookup_tx.go` (yeni):

```go
// WithQRTokenLookupTx: token→tenant çözümlemesi için TEK bootstrap yolu.
// app.tenant_id'yi ASLA set etmez; app.storefront_qr_token_hash GUC'u ile
// storefront_qr_codes'ta yalnız o hash'e sahip TEK satır görünür.
func (p *Pool) WithQRTokenLookupTx(ctx context.Context, tokenHash string, fn func(pgx.Tx) error) error
```

- `ErrEmptyTokenHash` sentinel (boş/hex-olmayan hash reddedilir; `ErrNilTenant`
  tarzı, `tenant_tx.go:20`).
- `pgx.TxOptions{IsoLevel: RepeatableRead, AccessMode: ReadOnly}` —
  `WithTenantReadTx` (`tenant_tx.go:73-103`) şablon; `defer tx.Rollback` hemen
  `BeginTx` sonrası.
- GUC: `tx.Exec(ctx, "SELECT set_config('app.storefront_qr_token_hash', $1, true)", tokenHash)`.

### Guest oturum imzalayıcı — YAPISAL ayrım

`backend/internal/platform/auth/guest_token.go` (yeni; şablon `context_token.go`):

- `guestTokenTyp = "GUEST"` (CTX değil) → `auth.IsContextToken` false döner,
  `auth.Middleware` (`middleware.go:44-67`) Keycloak yoluna sokup reddeder →
  "guest JWT staff endpoint'inde 401" inşaen doğru.
- **Ayrı tip** `GuestTokenSigner` — `ContextTokenSigner`'a metot ekleme YASAK.
- **Ayrı secret** `STOREFRONT_GUEST_TOKEN_SECRET` ≥32 bayt; `newGuestTokenSigner`
  `main.go:542-548` (`newContextTokenSigner`) deseniyle fx'ten sağlanır.
- Claims: `iss`, `aud: "storefront-guest"`, `tid`, `bid`, `table_id`,
  `qr` (qr_code_id), `sid` (guest_session_id, rastgele UUID), `exp` (4 saat).
  `PersonID`/`RoleIDs` YOK.
- `VerifyGuest`: typ + aud + exp açık kontrol; geçerli CTX token'ı da reddeder.
- Dönen tip `auth.GuestSession` — `auth.Principal` DEĞİL; ayrı context key:
  `auth.WithGuestSession` / `auth.GuestFromContext`.

### Modül iskeleti (`backend/internal/modules/storefront/`)

- `domain/qr_code.go` — `QRCode`, `Status` + `Valid()`, `TransitionStatus`
  (active→revoked tek yön; `pos/domain/order.go` transition map deseni).
- `domain/menu.go` — `GuestCategory`, `GuestProduct`, `GuestModifierGroup`,
  `GuestModifier`. Yalnız ADR §6 alanları; cost/stok/tedarikçi alanları
  TANIMLANMAZ.
- `domain/order.go` — `GuestCart`, `GuestCartLine{ProductID, Quantity,
  ModifierIDs, Note}` — fiyat alanı YOK.
- `repo/qr_code_repo.go` — `LookupByTokenHash` (bootstrap tx; dar SELECT),
  `Create`, `ListByBranch`, `Revoke`, `GetByID`.
- `repo/guest_order_repo.go` — `Link`, `ListBySession`, `GetOrderIDForSession`.
- `repo/errors.go` — `ErrNotFound` (`pos/repo/errors.go` şablonu).
- `service/qr_service.go` — `Issue` (crypto/rand 32 bayt → base64url; hash
  SHA-256 hex; ham token yalnız dönüş değerinde), `List`, `Revoke`, `Rotate`.
- `service/session_service.go` — `ResolveToken(ctx, rawToken)`: hash →
  `WithQRTokenLookupTx` → satır → `status` kontrolü (`ErrQRRevoked` /
  `ErrQRNotFound`; HTTP'de ikisi de 404) → `WithTenantReadTx` ile branch/table
  doğrulama (`pos/public`) → guest JWT.
- `service/menu_service.go` — `catalog/public.StorefrontMenuReader` → DTO.
- `service/order_service.go` — sepeti sunucuda yeniden fiyatlar,
  `pos/public.GuestOrderPlacer` çağırır, `storefront_guest_orders` bağını yazar.
- `public/storefront.go` — `ErrQRNotFound`, `ErrGuestForbidden` sentinel'ları.
- `module.go` — `fx.Module("storefront", ...)`; şablon `catalog/module.go:20-39`.
- `backend/cmd/api/main.go` — `fx.Provide(newGuestTokenSigner)` +
  `storefront.Module` (satır 79-86 listesi).

---

## WP2 — Backend: public HTTP yüzeyi

### `/api/public/v1/*` auth istisnası — GÜVENLİK KRİTİK

`backend/cmd/api/main.go:170-180` global middleware'i `/healthz` ve dev yolları
dışında her şeyi auth'a sokar. Public yüzey istisnaya eklenir:

```go
const publicAPIPrefix = "/api/public/v1/"

r.Use(func(next http.Handler) http.Handler {
    protected := authMW(openSessionMW(next))
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        path := r.URL.Path
        if path == "/healthz" || strings.HasPrefix(path, publicAPIPrefix) || (isDev && strings.HasPrefix(path, "/dev/")) {
            next.ServeHTTP(w, r)   // guard zinciri storefront/http.RegisterPublicRoutes içinde
            return
        }
        protected.ServeHTTP(w, r)
    })
})
```

Yorum zorunlu: "auth yok" değil, "auth zinciri RegisterPublicRoutes'ta" — kanıtı
WP5 public-guard smoke testi.

### D1 — QR token span/log sızıntısı önlemi (GÜVENLİK)

QR token API path'inde **asla** taşınmaz: otelhttp, chi route'lamadan önce ham
path'i span attribute olarak yazar (emsal: `main.go:341-343` webhook filtresi
gerekçesi). Session ucu token'ı gövdede alır (`POST /sessions`). Savunma
derinliği için `webhookTracingFilter` çok-prefix'li hale getirilir:

```go
func untracedPath(r *http.Request) bool {
    p := r.URL.Path
    return !strings.HasPrefix(p, paymenthttp.WebhookPathPrefix) &&
           !strings.HasPrefix(p, publicAPIPrefix)
}
```

### D4 — `cleaning` masası: ayrı hata

`openCheckTx` yalnız `empty|reserved` kabul eder. Misafir `cleaning` masasını
tararsa `ErrTableOccupied` ("masa dolu") YANLIŞ mesajdır. Karar: `cleaning`
misafir için reddedilir ama **ayrı sentinel** `pos/public.ErrTableNotReady` →
409, mesaj "Masanız hazırlanıyor, birkaç dakika içinde tekrar deneyin."

### Yeni dosyalar

- `storefront/http/public_handler.go`:
  ```go
  func (h *PublicHandler) RegisterPublicRoutes(r *chi.Mux) {
      r.Route("/api/public/v1", func(r chi.Router) {
          r.Use(httpx.PublicRateLimit(h.cache, httpx.RateLimitConfig{...}))  // IP bazlı
          r.Post("/sessions", h.startSession)   // token GÖVDEDE — path'te ASLA (bkz. D1)
          r.Group(func(r chi.Router) {
              r.Use(h.requireGuestSession)
              r.Get("/menu", h.getMenu)
              r.With(httpx.IdempotencyWithScope(h.cache, guestIdemScope)).Post("/orders", h.placeOrder)
              r.Get("/orders/{id}", h.getOrderStatus)
              r.Get("/orders", h.listMyOrders)
          })
      })
  }
  ```
- `storefront/http/guest_middleware.go` — cookie `om_guest` (HttpOnly, Secure
  (prod), SameSite=Lax, Path=/api/public/v1) → `VerifyGuest` →
  `auth.WithGuestSession`. Başarısız → 401. `auth.FromContext` bu zincirde HİÇ
  çağrılmaz.
- `storefront/http/dto.go` — misafir DTO'ları (domain struct doğrudan serialize
  edilmez).
- `storefront/http/errors.go` — RFC7807 problem detail.
- `backend/internal/platform/httpx/ratelimit.go` — Redis ZSET sliding-window
  (OPS-003 satır 25/71). Key `ratelimit:{level}:{identifier}:{window}`. Bu WP'de
  yalnız `public_ip` + `guest` seviyeleri. 429 + `Retry-After` +
  `X-RateLimit-*`. Redis düşükse **fail-closed: 503**.

### Değişecek dosyalar

**`platform/httpx/idempotency.go` — fork DEĞİL, parametrize.** Mevcut hâli
guest'i 401'ler (satır 92-97 `auth.FromContext`, satır 108 tenant scope):

```go
type IdempotencyScopeFunc func(*http.Request) (string, error)
func IdempotencyWithScope(cache *redis.Client, scope IdempotencyScopeFunc) func(http.Handler) http.Handler
func Idempotency(cache *redis.Client) func(http.Handler) http.Handler {
    return IdempotencyWithScope(cache, principalTenantScope)   // pos/payment çağrıları DEĞİŞMEZ
}
```
Guest scope: `"guest:" + tenantID + ":" + qrCodeID + ":" + guestSessionID`.
Mevcut idempotency testleri aynen geçmeli.

**`catalog/public/catalog.go`** — dar read-model interface'i:

```go
type StorefrontCategory struct { ID uuid.UUID; Name string; SortOrder int16; Products []StorefrontProduct }
type StorefrontProduct struct {
    ID uuid.UUID; Name, Description string
    PriceAmount int64      // menu_items.price_override ?? products.price_amount
    Currency, ImageURL string; Allergens []string; IsAvailable bool
    ModifierGroups []StorefrontModifierGroup
}
type StorefrontModifierGroup struct {
    ID uuid.UUID; Name, SelectionType string; MinSelect, MaxSelect int
    Modifiers []StorefrontModifier   // ID, Name, PriceDelta int64
}
type StorefrontMenuReader interface {
    GetStorefrontMenu(ctx context.Context, tenantID, branchID uuid.UUID) ([]StorefrontCategory, error)
    PriceCart(ctx context.Context, tenantID, branchID uuid.UUID, lines []CartLine) ([]PricedLine, error)
}
```

- `catalog/repo/menu_repo.go` — iki dar sorgu, kolonlar tek tek. Tablolar:
  `menus` (branch filtresi, is_active, valid aralığı) ⋈ `menu_items` ⋈
  `products` ⋈ `categories`; `product_channel_availability` `dine_in AND
  is_available` (`catalog/000001:238-265`); modifier'lar
  `product_modifier_groups` ⋈ `modifier_groups` ⋈ `modifiers`.
- `catalog/service/menu_service.go` — `GetStorefrontMenu` + `PriceCart`
  (`WithTenantReadTx`).
- `catalog/module.go` — `fx.Annotate(..., fx.As(new(pub.StorefrontMenuReader)))`
  (`newProductReader` deseni, satır 34, 41-61).

**`pos/public/pos.go`** — principal ALMAYAN adlandırılmış misafir girişi
(nil-principal ile `OrderService.Place` çağırmak YASAK — `Place` satır 43
`requireBranch` çağırıyor):

```go
type GuestOrderRequest struct {
    TenantID, BranchID, TableID uuid.UUID
    QRCodeID, GuestSessionID    uuid.UUID
    Lines []GuestOrderLine       // ProductID, ProductName, UnitPriceAmount, TaxRateBPS, Quantity, Note
    Note  string
}
type GuestOrderResult struct { OrderID, CheckID uuid.UUID; Status string }
type GuestOrderPlacer interface {
    PlaceGuestOrder(ctx context.Context, req GuestOrderRequest) (GuestOrderResult, error)
}
type GuestOrderReader interface {
    GetGuestOrder(ctx context.Context, tenantID, orderID uuid.UUID) (GuestOrderView, error)
}
var ErrTableNotFound = errors.New("pos: table not found")
var ErrTableNotReady = errors.New("pos: table not ready")   // cleaning — D4
```

- `pos/service/order_service.go` — `PlaceGuest`: tek `WithTenantTx` içinde
  (1) `GetTableForUpdate` + branch eşleşmesi (`ErrTableBranchMismatch`),
  (2) masanın OPEN check'i varsa kullan (`checks_open_table_id_uidx`,
  `pos/000004:66`); yoksa `source='online_qr'`, `opened_by=NULL`,
  `opened_by_kind='guest_qr'` ile aç, masa `occupied` —
  `CheckService.Open` gövdesi `openCheckTx` yardımcısına ÇIKARILIR
  (`check_service.go:104-137`), kopyalanmaz,
  (3) `orderRepo.Create` `order_channel='dine_in'`, `source='online_qr'`,
  `status='pending'`,
  (4) outbox `order.placed` payload'ına `"source": "online_qr"` → KDS mevcut
  davranışla görür, ws değişikliği yok.
- `pos/module.go` — `GuestOrderPlacer` + `GuestOrderReader` fx adaptörleri.
- `pos/http/handler.go:191` — `OpenedBy: &p.PersonID`, `OpenedByKind: staff`.

### Endpoint sözleşmesi (WP4 buna bağlanır)

| Metot | Yol | Auth | Not |
|---|---|---|---|
| POST | `/api/public/v1/sessions` | yok | İstek gövdesi: `{"token": "..."}`. 200 + `Set-Cookie: om_guest`; cevap: branch adı, masa etiketi. Geçersiz/iptal → 404 |
| GET | `/api/public/v1/menu` | guest cookie | Dar read-model. `Cache-Control: private, max-age=30` |
| POST | `/api/public/v1/orders` | guest cookie | `Idempotency-Key` ZORUNLU. İstemci fiyatı yok sayılır |
| GET | `/api/public/v1/orders` | guest cookie | Yalnız bu session'ın siparişleri |
| GET | `/api/public/v1/orders/{id}` | guest cookie | Session eşleşmezse 404 |

---

## WP3 — Admin entegrasyonu (QR üret / görüntüle / iptal)

### Backend

- `storefront/http/admin_handler.go`:
  ```go
  r.Route("/api/v1/storefront", func(r chi.Router) {
      r.With(h.permit("storefront.qr.read")).Get("/qr-codes", h.listQRCodes)      // ?branch_id=
      r.With(h.permit("storefront.qr.manage")).Post("/qr-codes", h.createQRCode)  // {branch_id, table_id, table_label}
      r.With(h.permit("storefront.qr.read")).Get("/qr-codes/{id}", h.getQRCode)
      r.With(h.permit("storefront.qr.manage")).Post("/qr-codes/{id}/revoke", h.revokeQRCode)
      r.With(h.permit("storefront.qr.manage")).Post("/qr-codes/{id}/rotate", h.rotateQRCode)
  })
  ```
  `permit` = `auth.RequirePermission` (`pos/http/handler.go:58-60`). Rotate =
  eski satır revoke + yeni satır. **Ham token yalnız create/rotate cevabında bir
  kez**; list/get'te ASLA. QR görselini frontend üretir.
- `storefront/module.go` — `fx.Invoke` ile İKİ kayıt: `RegisterPublicRoutes` +
  `RegisterRoutes` (iki ayrı smoke testinin ön koşulu).

### OPA + permission seed — TUZAK

**D3 kesin metinleri — tahmine yer yok.** `waiter` rolü registry'de
KULLANILAMAZ (`identity/000006`'da seed edilmemiş, `authz.rego:47-51` notu);
rego kuralı yalnız `cashier`/`shift_manager` alır, `manager` wildcard'la kapsanır.

- `backend/configs/opa/bundles/authz.rego` (satır 229-234 komşusu):
  ```rego
  storefront_qr_read_actions := {"storefront.qr.read"}
  allow if { input.action in storefront_qr_read_actions; any_role({"cashier","shift_manager"}) }
  storefront_qr_manage_actions := {"storefront.qr.manage"}
  allow if { input.action in storefront_qr_manage_actions; has_role("shift_manager") }
  ```
- `backend/migrations/identity/000016_storefront_qr_permissions.up.sql` —
  **literal tuple formu ZORUNLU** (SELECT formu `permission_wiring_test.go`
  parser'ına görünmez; `000014:25-33` uyarısı):
  ```sql
  INSERT INTO role_permissions (role_id, tenant_id, resource, action) VALUES
      ('00000001-0000-0000-0000-000000000001', NULL, 'storefront_qr', 'read'),    -- cashier
      ('00000001-0000-0000-0000-000000000002', NULL, 'storefront_qr', 'read'),    -- shift_manager
      ('00000001-0000-0000-0000-000000000002', NULL, 'storefront_qr', 'manage')   -- shift_manager
  ON CONFLICT (role_id, resource, action) DO NOTHING;
  -- + 000014'teki 2. adım: tenant klonlarının backfill'i (aynı şablon)
  ```
- `backend/internal/platform/auth/permission_wiring_test.go` →
  `permissionWiringRegistry`'ye:
  ```go
  // -- storefront (storefront.qr.*) -------------------------------------
  {"storefront_qr", "read"}: {
      Wired: true, CheckRole: "cashier", CheckAction: "storefront.qr.read",
  },
  {"storefront_qr", "manage"}: {
      Wired: true, CheckRole: "shift_manager", CheckAction: "storefront.qr.manage",
      Reason: "QR üretme/iptal/yenileme tek bir storefront.qr.manage aksiyonunda " +
          "toplanır (pos.table.manage'in create+update+status'ü topladığı gibi).",
  },
  ```

### Frontend (admin)

- `web/apps/admin/src/app/(main)/pos/tables/page.tsx` — sayfa adisyon-tabanlı
  görünümden masa ızgarasına çevrilir (`GET /api/v1/pos/tables?branch_id=`;
  şube seçici `settings/fiscal-sections` deseni; adisyon bilgisi
  `active_check_id`/`status` ile korunur) — boş masaya da QR basılabilmeli.
  Her masa kartına "QR" aksiyonu → dialog. `use-pos.ts`'e `useTables` eklenir.
  Not: create body'deki branch/table tutarlılığının güvenlik ağı, misafir
  session açılışındaki pos/public doğrulamasıdır (yanlış eşleşme → 404).
- `web/apps/admin/src/components/storefront/qr-code-dialog.tsx` — QR görseli
  (`qrcode.react`), yazdır, iptal, yenile. Ham token yalnız üretim anında
  bellekte; state/localStorage'a yazılmaz (`lib/api.ts:19` gerekçesi).
- `web/apps/admin/src/hooks/use-storefront.ts` — `useQRCodes(branchId)`,
  `useCreateQRCode()`, `useRevokeQRCode()`, `useRotateQRCode()`
  (`hooks/use-pos.ts:6-20` deseni).
- `web/apps/admin/src/types/` — `QRCode` tipi.
- `web/apps/admin/src/messages/*` — i18n (hardcoded Türkçe yasak).

---

## WP4 — Frontend: `web/apps/menu`

### İskelet

- `web/pnpm-workspace.yaml` — değişiklik GEREKMEZ (`apps/*` kapsıyor).
- `web/apps/menu/package.json` — `"name": "@onlinemenu/menu"`. Bağımlılıklar
  admin'den daraltılır: next ^16.2.7, react 19.2.6, @tanstack/react-query
  ^5.100.1, axios, tailwindcss ^4.2.4, clsx, tailwind-merge, zustand,
  next-intl, zod. Alınmayacak: nookies, keycloak, recharts, cmdk, ws.
- Config dosyaları (`next.config.ts`, `tsconfig.json`, `postcss.config.mjs`,
  `eslint.config.mjs`, `vitest.config.ts`, `components.json`) admin'den uyarlanır.
- Kök `Taskfile.yml`: `menu:dev` + `menu:build` görevleri (ARCH-004).

### ui-kit gerçeği

`web/packages/ui-kit/src/index.ts` = `export {}` — hiçbir bileşen yok. Admin'in
shadcn bileşenlerinden `apps/menu` import EDEMEZ (ARCH-005). **Karar
(önerilen):** 5-6 primitive `web/packages/ui-kit/src/`'e taşınır — `button.tsx`,
`card.tsx`, `sheet.tsx`, `badge.tsx`, `skeleton.tsx`, `lib/cn.ts` — ve export
edilir. Admin bu WP'de migrate edilmez.

### Sayfalar (App Router)

- `src/app/layout.tsx`, `globals.css`, `providers.tsx` (admin
  `query-provider.tsx` şablonu)
- `src/app/q/[token]/page.tsx` — Server Component: route param'daki token'ı
  `POST /api/public/v1/sessions` GÖVDESİNDE gönderir (path'te asla — D1),
  cookie kurulur, → `/menu`; hata → açık hata sayfası. Bu rotada
  `Referrer-Policy: no-referrer` (token `Referer` ile dış kaynağa sızmasın).
- `src/app/menu/page.tsx` — SSR + `revalidate: 30`.
- `src/app/cart/page.tsx` — zustand sepet.
- `src/app/orders/[id]/page.tsx` — TanStack Query `refetchInterval: 5000`.
- `src/app/orders/page.tsx` — oturumun siparişleri.
- `src/lib/api.ts` — axios, `withCredentials: true`,
  `baseURL: NEXT_PUBLIC_PUBLIC_API_URL`. Authorization header YOK.
  `Idempotency-Key` mutasyon başına bir kez `crypto.randomUUID()`; retry'da AYNI
  key.
- `src/lib/cart-store.ts`, `src/hooks/use-storefront.ts`, `src/messages/tr.json`.

### Cookie / CORS tuzağı

Dev'de menu (:3001) ile API (:8080) farklı origin → axios
`withCredentials: true` + API `Access-Control-Allow-Credentials: true`
(`main.go:200` mevcut) + cookie `SameSite=Lax` (prod aynı-site / reverse proxy
varsayımı). `SameSite=None; Secure` gerekiyorsa açıkça belirt.

---

## WP5 — Test & kalite (kabul kriteri)

### 1. RLS bootstrap testi (en kritik)

`backend/internal/platform/db/qr_lookup_rls_test.go` — `rls_test.go`
`TestRLSAllTenantsScope` (:514) komşusu, aynı `TestMain` container'ı; isim
`TestRLSQRTokenLookupIsRowScoped` (`-run TestRLS` yakalasın):
- İki tenant'a QR seed; `WithQRTokenLookupTx(hashA)` içinde WHERE'siz
  `SELECT count(*) FROM storefront_qr_codes` → **tam 1**.
- Aynı tx'te `products`/`orders`/`checks` count → **0**.
- INSERT denemesi → hata (read-only).
- Boş/hex-olmayan hash → `ErrEmptyTokenHash`, tx açılmaz.
- Normal `WithTenantReadTx`'te başka tenant'ın QR'ı görünmez.

### 2. Guest session güvenlik testleri

`platform/auth/guest_token_test.go`: typ `"GUEST"`; `IsContextToken` → false;
`ContextTokenSigner.Verify(guest)` → hata; `VerifyGuest(staffCTX)` → hata;
yanlış secret / süresi geçmiş / yanlış aud → hata.

`storefront/http/guest_auth_test.go`: guest cookie ile `/api/v1/pos/orders` →
401; staff token ile `/api/public/v1/orders` → 401; cookie'siz → 401; bozuk →
401.

### 3. Cross-tenant / varlık sızıntısı invariant'ları

`storefront/service/integration_test.go` (testcontainers,
`pos/service/integration_test.go` şablonu): B'nin token'ı → 404; revoked → 404
(serviste `ErrQRRevoked` ayrımı loglanır); A session'ı ile B'nin order'ı → 404;
masa başka şubeye taşınmış → 409 (`ErrTableBranchMismatch`).

### 4. Fiyat manipülasyonu

`storefront/service/pricing_test.go`: gövdeye `unit_price_amount: 1` →
persist edilen fiyat read-model değeri; menüde olmayan / dine_in-kapalı ürün →
422; başka tenant'ın ürünü → 422/404.

### 5. Dar DTO — alan yokluğu

`storefront/http/menu_dto_test.go`: ham cevapta `cost_price`, `stock`,
`supplier`, `internal_note` substring'leri YOK.

### 6. Wiring audit — iki smoke testi

- `storefront/http/authz_smoke_test.go` — admin registrar; `chi.Walk`, rolsüz
  principal → her route 403 (`pos/http/authz_smoke_test.go` kopyası).
- `storefront/http/public_guard_smoke_test.go` — public registrar; `chi.Walk`,
  `/sessions` hariç her route guest'siz → 401. Yeni public route
  guard'sız eklenirse test adıyla kırılır.

### 7. Durum makinesi + idempotency + rate limit

- `storefront/domain/qr_code_test.go` — active→revoked serbest, tersi yasak
  (tablo-driven).
- `platform/httpx/idempotency_test.go` — mevcutlar aynen + guest scope: aynı
  key farklı gövde → 422; aynı key aynı gövde → replay (ikinci sipariş
  yazılmaz); iki tenant aynı key → çakışmaz.
- `platform/httpx/ratelimit_test.go` — 429 + Retry-After; sliding window
  doğrulaması; Redis düşükse 503.

### 8. E2E

`backend/internal/e2e/storefront_test.go` (`spine_test.go` şablonu): QR üret →
session → menü → sipariş → KDS WS'te görünür → accept → adisyon kapat (ödeme +
fiscal mock) → masa `cleaning`.

---

## Kabul kontrol listesi (her WP sonunda)

- `task backend:lint` yeşil (go-arch-lint `storefront_*` dahil)
- `task backend:test:rls-leak` yeşil, `TestRLSQRTokenLookupIsRowScoped` koşuyor
- `task backend:migrate:up` + `task backend:migrate:verify` yeşil
- `task security:scan` yeşil
- `web/apps/menu`: `pnpm typecheck` + `pnpm lint` yeşil
