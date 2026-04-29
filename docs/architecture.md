# Online Menu — Mimari Kılavuzu

## Genel Yaklaşım: Modüler Monolit

Proje, **monolitik yapıda başlar** ancak her modül ilk günden bağımsız bir mikroservise dönüşebilecek şekilde inşa edilir. Bu yaklaşımın nedeni:

- Erken aşamada operasyonel yük (servis keşfi, ağ hataları, dağıtık işlemler) minimaldir.
- Kod tabanı büyüdükçe bounded context'ler netleşir ve bilinçli servis ayrımı yapılır.
- Her modül kendi migration, event sözleşmesi ve public API'siyle zaten ayrıştırılmıştır.

---

## Proje Dizin Yapısı

```
onlinemenu.tr/
├── cmd/
│   ├── api/                    # Tek monolitik HTTP sunucu
│   ├── worker/                 # Asynq iş tüketicileri
│   ├── edge/                   # Şube local server (offline POS)
│   └── migrate/                # Migration koşucusu
│
├── internal/
│   ├── modules/
│   │   ├── identity/           # Keycloak sync, JWT middleware
│   │   ├── tenant/             # İşletme, şube, şube ayarları
│   │   ├── hr/                 # Personel özlüğü, şube ataması
│   │   ├── party/              # Tedarikçi, müşteri (cari)
│   │   ├── catalog/            # Menü, ürün, fiyat listesi
│   │   ├── pos/                # Masa, adisyon, sipariş, mutfak
│   │   ├── inventory/          # Stok, depo, sevkiyat
│   │   ├── billing/            # E-fatura, provider pattern
│   │   ├── payment/            # Nakit, terminal, sanal POS
│   │   └── edge_sync/          # Outbox/inbox, sync protokolü
│   │
│   └── platform/               # Cross-cutting altyapı
│       ├── db/                 # RLS middleware, sqlc helpers
│       ├── eventbus/           # NATS JetStream publisher/subscriber
│       ├── auth/               # Keycloak client, JWT parse, OPA
│       ├── otel/               # OpenTelemetry setup
│       ├── vault/              # HashiCorp Vault client
│       └── cache/              # Redis client
│
├── contracts/
│   ├── events/                 # JSON Schema per event (versiyonlu)
│   └── openapi/                # Modül başına REST spec
│
├── migrations/
│   ├── tenant/
│   ├── catalog/
│   ├── pos/
│   ├── inventory/
│   └── ...                     # Modül başına ayrı dizin
│
├── web/
│   ├── admin/                  # Next.js 14 yönetim paneli
│   └── pos/                    # Wails + React POS istemcisi
│
├── docs/                       # Bu dosyalar
├── deploy/
│   ├── docker-compose.dev.yml
│   └── k8s/                    # Faz 2+
│
├── go.mod
├── go.work
├── .go-arch-lint.yml           # Modül import kuralları
└── .golangci.yml
```

---

## Modül İzolasyon Kuralları

### 1. Kapalı Kutu Prensibi

Her modülün tek genel API yüzeyi `public/` paketidir:

```
internal/modules/pos/
├── public/          ← Dışarıya açık: interface + DTO tanımları
├── domain/          ← Kapalı: iş mantığı, aggregate'ler
├── repo/            ← Kapalı: DB erişim katmanı (sqlc üretilen)
├── http/            ← Kapalı: HTTP handler'lar
├── events/          ← Kapalı: event publisher/subscriber
└── service.go       ← Kapalı: bağımlılık kurulum noktası
```

`public/` dışındaki hiçbir paketi başka modül import edemez.

### 2. Modüller Arası İletişim

**Yasak:** Başka modülün DB tablosuna doğrudan erişim.

**İzin verilen:**
```
A → B.public.Interface   (in-process komut/sorgu)
A → NATS JetStream       (domain event yayınlama)
B → NATS JetStream       (domain event tüketme)
```

Mikroservise geçişte `A → B.public.Interface` satırı RPC transport ile değiştirilir; geri kalan her şey aynı kalır.

### 3. CI Zorunluluğu

`.go-arch-lint.yml` ile yasak import ilişkileri tanımlanır:

```yaml
# Örnek
deps:
  pos:
    may_depend_on: [catalog.public, inventory.public, payment.public, platform]
    must_not_depend_on: [inventory.repo, inventory.domain, billing.domain]
```

`go-arch-lint check` CI'da başarısız olursa PR birleştirilemez.

---

## Multi-Tenant İzolasyonu — PostgreSQL RLS

### Strateji: Shared Schema + Row-Level Security

Her tabloda `tenant_id UUID NOT NULL` kolonu bulunur. PostgreSQL RLS policy'si her sorgunun yalnızca aktif kiracının satırlarını görmesini sağlar.

### İki Ayrı PostgreSQL Rolü (ADR-SEC-002)

```sql
-- Migration rolü: tablo yaratır, policy tanımlar. Tablo sahibi (OWNER).
CREATE ROLE app_migrator LOGIN PASSWORD '...';

-- Uygulama rolü: RUNTIME bağlantıları yalnızca bu rolle yapılır. RLS zorunludur.
CREATE ROLE app_runtime LOGIN PASSWORD '...';
GRANT USAGE ON SCHEMA public TO app_runtime;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO app_runtime;
```

### Policy Şablonu (ADR-SEC-001 + ADR-SEC-002)

```sql
ALTER TABLE orders ENABLE ROW LEVEL SECURITY;
ALTER TABLE orders FORCE ROW LEVEL SECURITY;  -- FORCE: tablo sahibi bile bypass edemez

-- current_setting ikinci argümanı false = set edilmemişse exception (sessiz sızıntı değil)
CREATE POLICY tenant_read ON orders
    FOR SELECT USING (tenant_id = current_setting('app.tenant_id', false)::uuid);

CREATE POLICY tenant_write ON orders
    FOR ALL USING (tenant_id = current_setting('app.tenant_id', false)::uuid)
              WITH CHECK (tenant_id = current_setting('app.tenant_id', false)::uuid);
```

Yeni tablo oluşturan her migration `ENABLE` + `FORCE ROW LEVEL SECURITY` içermek zorundadır. CI lint (`scripts/lint_rls.sh`) eksik tabloları yakalar.

### Platform Helper (ADR-SEC-001)

```go
// internal/platform/db/tenant_tx.go
// Tek izinli desen — modüller bu fonksiyon dışından DB'ye yazamaz
func (p *Pool) WithTenantTx(ctx context.Context, tenantID uuid.UUID, fn func(tx pgx.Tx) error) error
```

**Lint yasağı:** `pool.Query`, `pool.Exec` doğrudan çağrılar modül kodunda yasaktır. `SET` komutu (LOCAL olmadan) yasaktır — session leak riski.

### pgBouncer (ADR-SEC-001)

**Faz 0-1:** pgBouncer yok, `pgxpool` doğrudan. 100-200 concurrent connection'a yeterli.

**Faz 2+:** pgBouncer **transaction mode** kullanılır. `SET LOCAL` transaction scope'a bağlıdır — transaction mode ile tamamen uyumludur. pgBouncer session mode **gerekmez**.

**pgx exec mode:** `pgxpool.Config.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeExec` (transaction mode uyumluluğu için).

### Güvenlik Sızıntı Testi

Her PR'da otomatik çalışır:

```go
// Tenant A token ile Tenant B kaydına erişim → 404 dönmeli
func TestRLSIsolation(t *testing.T) {
    tenantA := createTenant(t)
    tenantB := createTenant(t)
    order := createOrder(t, tenantB)

    resp := apiCall(t, tenantA.Token, "GET", "/orders/"+order.ID)
    assert.Equal(t, 404, resp.StatusCode)
}
```

---

## Kimlik & Yetki

### Keycloak — Tek Realm (ADR-AUTH-002)

- **Tek realm** (`onlinemenu`) — realm-per-tenant reddedildi (ADR-AUTH-002).
- Her kullanıcı Keycloak'ta; `keycloak_sub` kolonu ile DB kullanıcısı eşlenir.
- JWT claim'ler mapper ile basılır: `tenant_id`, `branch_ids[]` (array), `roles[]`.
- Keycloak config-as-code: `keycloak-config-cli` veya Terraform Keycloak provider.

### Authorization — Dört Katmanlı Model (ADR-AUTH-001)

| Katman | Sorumluluk | Nerede |
|---|---|---|
| **RLS (DB)** | Yalnızca `tenant_id` izolasyonu | PostgreSQL policy |
| **OPA (Policy)** | "İzin var mı" + Scope (`ScopeOwn`/`ScopeBranch`/`ScopeTenant`) | In-process embedded |
| **Service (Scope)** | OPA scope'unu WHERE clause'a çevirir | Go service layer |
| **DTO Projection** | Field-level filtreleme (kasiyer `cost_price` görmez) | Response DTO |

**OPA sadece `Decision{Allow bool, Scope Scope}` döner** — permission listesi döndürmez.  
**Domain model rolleri bilmez** — field-level filtreleme yalnızca DTO projection'da.

```
HTTP Request → JWT doğrula → Principal{TenantID, BranchIDs[], Roles[]}
  → BEGIN + SET LOCAL app.tenant_id (RLS devrede)
  → authz.Decide("action", principal) → Decision{Allow, Scope}
  → Service: scope'u WHERE clause'a çevir → query
  → DTO projection: permsForRoles(roles) → field filtreleme
  → JSON response
```

OPA policy bundle'ları: `configs/opa/bundles/`. Decision cache: Redis TTL 60s.

---

## Event Sözleşmeleri

### Dizin Yapısı

```
contracts/events/
├── tenant/
│   ├── tenant.created.v1.json
│   └── branch.created.v1.json
├── pos/
│   ├── order.created.v1.json
│   └── check.closed.v1.json
└── inventory/
    └── stock.depleted.v1.json
```

### Versiyonlama Kuralı

- Minor değişiklik (yeni opsiyonel alan): aynı `vN` güncellenir.
- Breaking değişiklik: `vN+1` yeni dosya oluşturulur, `vN` tüketicileri migrate edilene kadar korunur.

### NATS JetStream Subject Yapısı

```
<module>.<event_type>.<v>.<tenant_id>

Örnekler:
  pos.order.created.v1.550e8400-e29b-41d4-a716-446655440000
  inventory.stock.depleted.v1.*      (wildcard subscription)
```

### Go Struct Üretimi

`eventgen` CLI (contracts/ JSON Schema → Go struct):

```bash
make contracts-gen  # contracts/events/**/*.json → internal/platform/events/generated/
```

---

## Gözlemlenebilirlik

### OpenTelemetry Zorunlulukları

Her HTTP handler, DB sorgusu ve event publish/consume işlemi bir span açar. `platform/otel` paketi middleware ve interceptor'ları sağlar.

```
HTTP → [trace] → DB → [span] → NATS publish → [span]
```

Tüm spanlar Grafana Tempo'ya gönderilir. Loglarda `trace_id` + `span_id` zorunlu.

### Grafana Stack

| Araç | Görev |
|---|---|
| Prometheus | Metrik toplama |
| Loki | Yapılandırılmış log toplama |
| Tempo | Distributed tracing |
| Grafana | Görselleştirme |
| OTel Collector | Tüm sinyallerin tek çıkış noktası |

---

## Mikroservise Geçiş Yolu

Bir modülü ayırma adımları:

1. `internal/modules/<name>/public` interface'ini RPC (gRPC veya HTTP) transport'una bağla.
2. Modülün migration'larını yeni bir veritabanına uygula.
3. Servis discovery'yi Traefik / Consul üzerinden yönet.
4. NATS event'ler değişmeden çalışmaya devam eder.
5. Eski monolitteki modül kodunu kaldır.

İlk ayrılacak adaylar (Faz 4): **payment** (PCI scope), **billing**, **identity**, **reporting**.
