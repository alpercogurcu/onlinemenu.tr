# CLAUDE.md — Online Menu

Türkiye pazarı için çok-kiracılı, modüler POS & işletme yönetim platformu.

**Temel kaynaklar:**
- Mimari: `docs/architecture.md`
- Yol haritası: `docs/ROADMAP.md`
- Veritabanı şeması: `docs/db-schema.md`
- Offline sync: `docs/offline-sync.md`
- Mimari kararlar: `docs/adr/` (17 ADR, kategorili)
- Delta direktifleri: `delta-v2.md` (baseline üzerinde tüm kararlar)

---

## Tech Stack (Özet)

| Katman | Teknoloji |
|---|---|
| Backend | Go 1.26+, chi v5, sqlc 1.30, golang-migrate, asynq 0.26+ |
| DI | uber-go/fx (modül wiring, `cmd/api/main.go`) |
| Veritabanı | PostgreSQL 18 (RLS), Redis 8 |
| Messaging | NATS JetStream 2.12+ (monolith), Kafka (Faz 2+ CDC) |
| Kimlik | Keycloak 26.x (tek realm), OPA 0.68+ (embedded) |
| Gözlemlenebilirlik | OpenTelemetry + Grafana/Loki/Tempo/Prometheus |
| Secrets | HashiCorp Vault (runtime) + SOPS/age (bootstrap & git) |
| Nesne depolama | MinIO |
| Container build | ko (distroless, Dockerfile yok) |
| Test | testcontainers-go, goleak, stretchr/testify, k6 |
| Dev hot-reload | air (`task dev` ile başlatılır) |
| Komut çalıştırıcı | Taskfile — `task <name>`, `task --list` (Makefile yok) |
| Admin frontend | Next.js 16 App Router + shadcn/ui (CLI v4) + TanStack Query |
| POS desktop | Wails v2 (stable) + React + shadcn — v3 alpha izleniyor |
| Frontend monorepo | pnpm workspaces (`web/apps/*` + `web/packages/*`) |
| Mobil (Faz 4) | Flutter |
| Infra | Docker Compose (dev), Kubernetes (Faz 2+) |

---

## Mimari Kurallar (Zorunlu)

### Modül İzolasyonu
- Her modül `internal/modules/<name>/` altında kapalı kutu.
- Dışarıya yalnızca `internal/modules/<name>/public/` sızar.
- Modüller arası DB erişimi **yasak** — yalnızca `public/` interface veya NATS event.
- `go-arch-lint check` CI'da import kurallarını zorlar.

### Multi-Tenant (ADR-SEC-001, ADR-SEC-002)
- Her tabloda `tenant_id UUID NOT NULL` + `FORCE ROW LEVEL SECURITY`.
- `SET LOCAL app.tenant_id` → yalnızca `platform/db.WithTenantTx()` aracılığıyla.
- `pool.Query`, `pool.Exec` doğrudan modül kodunda **yasak**.
- İki ayrı rol: `app_migrator` (tablo sahibi), `app_runtime` (RLS zorunlu).

### Authorization (ADR-AUTH-001)
- RLS → OPA (Allow + Scope) → Service (WHERE clause) → DTO Projection (field filter) sırası.
- OPA permission listesi döndürmez; field-level filtreleme yalnızca DTO'da.
- Domain model rolleri bilmez.

### Event / Outbox (ADR-DATA-001, ADR-DATA-002)
- Event'ler immutable. Güncelleme = yeni event.
- Consumer'larda yalnızca `ON CONFLICT (event_id) DO NOTHING`.
- `UPDATE <module>_outbox SET payload = ...` **yasak**.

### Idempotency (ADR-SEC-003)
- Payment, invoice, check/close, order POST'larında `Idempotency-Key` header **zorunlu**.
- Platform middleware `internal/platform/httpx/idempotency.go`.

### Fiscal (ADR-FISCAL-001)
- Payment modülü her işlemde `FiscalDeviceAdapter.RegisterSale()` çağırır (mock bile olsa).
- `fiscal_device_type = 'none'` üretimde yasak.

### Graceful Shutdown (Platform Zorunluluğu)
- `cmd/api/main.go` ve `cmd/edge/main.go` `signal.NotifyContext` + `errgroup` kullanır.
- Kapatma sırası: HTTP drain → NATS consumer flush → asynq queue drain.
- `os.Exit` ve `log.Fatal` `main()` dışında **yasak** — context cancel ile kapatma.

### Dependency Injection (uber-go/fx)
- Her modül `fx.Module` olarak tanımlanır; `cmd/api/main.go` modülleri birleştirir.
- `os.Getenv` modül kodunda **yasak** — tüm config `fx.Provide` ile enjekte edilir.
- Vault client `platform/vault` paketinde; modüller doğrudan Vault API çağırmaz.

### Secrets (Vault + SOPS)
- Runtime sırlar (DB credentials, NATS token, Keycloak secret) → Vault dynamic secrets.
- Bootstrap değerler (Vault root token, docker-compose admin şifreleri) → `.env.sops` (git'e şifreli commit).
- `.env` düz metin dosyası repo'ya **asla** commit edilmez.

---

## Agent Kullanım Kılavuzu

### ⚠️ Proaktif Kullanım Kuralı
Bu proje büyük ve karmaşık. **Şüphe duyduğunda, yeni bir şey tasarladığında veya kritik kod yazarken agent'ları çağır.** Tek başına karar verme.

---

### UI/UX Tasarımı

#### `/ui-ux-pro-max` (Skill — Ana UI Kaynağı)
**Tüm ekran tasarımları için ilk çağrılacak skill.** POS ekranı, admin panel, masa planı, mutfak ekranı (KDS), raporlama arayüzü tasarımından önce çağır.

```
Trigger: Herhangi bir ekran, sayfa veya component tasarımı başlamadan önce
```

#### `ui-designer` (Agent)
Shadcn component sistemi, POS touchscreen UX desenleri, design token yapısı, erişilebilirlik (WCAG).

```
Trigger: Component library kararı, touchscreen etkileşim tasarımı, design system revizyonu
```

#### `/frontend-design` (Plugin Skill)
Web sayfa düzeni, Next.js admin panel sayfa kompozisyonu.

```
Trigger: Admin panel sayfası veya çok bölümlü layout tasarımı
```

---

### Backend — Go

#### `golang-pro` (Agent)
Go implementasyonu: handler, service, repo, middleware, event publisher/consumer yazımı.

```
Trigger: Yeni modül iskeleti, servis katmanı, repo implementasyonu
```

#### `go-architect` (Agent — Proaktif)
Concurrent kod, interface tasarımı, production readiness review. **Her goroutine ve channel kullandıktan sonra çağır.**

```
Trigger: Dispatcher goroutine, NATS consumer, outbox worker, channel/mutex kodu
         Yeni Go interface tasarımı (FiscalAdapter, Provider, Module vb.)
         "Bu prodüksiyon'a hazır mı?" sorusu
```

#### `go-build-resolver` (Agent)
Build hatası, go vet, linter uyarıları.

```
Trigger: go build veya golangci-lint hata verdiğinde
```

---

### Veritabanı & Cache

#### `postgres-pro` (Agent)
RLS policy optimizasyonu, migration stratejisi, index tasarımı, HA replication, backup.

```
Trigger: Yeni migration yazımı, RLS policy değişikliği, slow query, backup/DR soruları
         Schema tasarım kararları (ADR-OPS-001 uyumu için)
```

#### `database-optimizer` (Agent)
Redis cache stratejisi, slow query analizi, multi-DB index optimizasyonu.

```
Trigger: Redis TTL stratejisi (authz cache, idempotency, rate limit), index önerisi
```

---

### Real-time & Messaging

#### `websocket-engineer` (Agent)
Mutfak ekranı (KDS) WebSocket bağlantısı, masa/adisyon real-time sync, NATS embedded (local server).

```
Trigger: Mutfak ekranı (KDS) implementasyonu, masa durumu real-time güncelleme,
         NATS consumer tasarımı, local server tablet senkronu
```

---

### Güvenlik & Uyumluluk

#### `security-engineer` (Agent)
Vault dynamic secrets, OPA policy implementasyonu, zero-trust, CI/CD güvenlik kontrolü.

```
Trigger: OPA policy yazımı (ADR-AUTH-001), Vault entegrasyonu,
         Cihaz pairing güvenliği (ADR-SEC-004), security middleware
```

#### `security-auditor` (Agent — Read-only)
Güvenlik açığı denetimi. Özellikle payment ve auth modülleri tamamlandığında.

```
Trigger: Modül implementasyonu bittikten sonra audit, PR öncesi güvenlik review
```

#### `compliance-auditor` (Agent — Read-only)
KVKK uyumu (ADR-OPS-002), PCI DSS (payment modülü).

```
Trigger: Tenant offboarding implementasyonu, payment/fiscal modül tamamlanması
```

---

### Ödeme & Fiscal

#### `payment-integration` (Agent)
PayTR sanal POS (Faz 2), PCI DSS akışları, fraud önleme. FiscalAdapter gerçek cihaz entegrasyonları.

```
Trigger: PayTR entegrasyonu (Faz 2), ÖKC cihaz adapter implementasyonu (Faz 2)
```

---

### API Tasarımı

#### `api-designer` (Agent)
REST endpoint tasarımı, OpenAPI spec, API versioning, authentication pattern.

```
Trigger: Yeni modülün REST API'si tasarlanırken, versioning kararı, OpenAPI spec yazımı
```

---

### Frontend

#### `react-specialist` (Agent)
Wails v2 POS UI, shadcn component optimizasyonu, state management (TanStack Query).

```
Trigger: POS React ekranı (Wails), complex state yönetimi, component performansı
```

#### `nextjs-developer` (Agent)
Admin paneli (Next.js 16 App Router), SSG/ISR, server actions, SEO.

```
Trigger: Admin panel route/layout/page implementasyonu, server component tasarımı
```

---

### Infra & DevOps

#### `docker-expert` (Agent)
`docker-compose.dev.yml` tasarımı, multi-stage Go build, container güvenliği.

```
Trigger: docker-compose değişikliği, yeni servis ekleme (Keycloak, NATS, Temporal vb.),
         production Dockerfile
```

#### `devops-engineer` (Agent)
CI/CD pipeline (lint → test → migration dry-run → build), deployment otomasyonu.

```
Trigger: GitHub Actions workflow, deployment pipeline, Vault secret injection
```

---

### Mimari & Dağıtık Sistem

#### `microservices-architect` (Agent)
Bounded context sınırları, servis ayrımı kararları (Faz 2-4), Kafka event streaming.

```
Trigger: "Bu modülü ayrı servise ayıralım mı?" sorusu, Debezium/Kafka CDC tasarımı,
         servis keşfi, API gateway kararı
```

---

### Kalite & Test

#### `qa-expert` (Agent)
E2E test stratejisi, k6 yük testi senaryoları (500 aktif POS simülasyonu), POS satış akışı testi.

```
Trigger: k6 yük testi senaryosu yazımı, E2E test stratejisi, RLS sızıntı test planı
```

#### `tdd-guide` (Agent)
Test-first geliştirme, tablo-driven testler, %80+ coverage zorunluluğu.

```
Trigger: Yeni feature veya bug fix başlamadan önce (test-first workflow)
```

#### `silent-failure-hunter` (Agent)
Sessiz hata tespiti, catch block incelemesi, hatalı fallback.

```
Trigger: Error handling kodu yazıldıktan sonra, outbox retry mantığı, NATS consumer hata yönetimi
```

---

### Code Review

#### `code-reviewer` (Agent)
Güvenlik açıkları, en iyi pratikler, kod kalitesi.

```
Trigger: PR öncesi, modül implementasyonu tamamlandıktan sonra
```

#### `/pr-review-toolkit:review-pr` (Plugin Skill)
Kapsamlı PR review (test coverage, silent failures, type design, comments).

```
Trigger: PR oluşturmadan önce kapsamlı review
```

#### `/feature-dev:feature-dev` (Plugin Skill)
Feature geliştirme workflow'u (explore → architect → implement).

```
Trigger: Yeni modül veya büyük feature implementasyonuna başlarken
```

---

### Debug

#### `debugger` (Agent)
Bug teşhisi, root cause analizi, stack trace incelemesi.

```
Trigger: Anlaşılamayan hata, production bug, test başarısızlığı
```

---

## Kod Standartları

### Komut Çalıştırma (Zorunlu)
- Her CLI komutu `task <name>` üzerinden koşturulur.
- `go run`, `npm run`, `docker compose`, `migrate` gibi komutlar doğrudan çağrılmaz.
- Taskfile'da karşılığı yoksa önce görevi ekle, sonra çalıştır.
- Mevcut görevler: `task --list`.

### Go
- `golangci-lint` + `go-arch-lint` CI'da zorunlu.
- Yorum yok (self-explaining kod); yalnızca "neden" için yorum.
- Context propagation her yerde zorunlu.
- Error wrapping: `fmt.Errorf("operation: %w", err)`.
- Table-driven testler, `testcontainers-go` integration testleri.

### Güvenlik Taraması
- `task security:scan` — gosec (Go kod) + trivy (container/dependency CVE). CI'da zorunlu.
- False-positive yönetimi `.gosec.yml` exclusion'larıyla yapılır — aynı false-positive'i her seansta kovalama.

### Frontend Monorepo
- `web/apps/admin` ve `web/apps/pos-desktop` **birbirinden import edemez**.
- Ortak kod `web/packages/*` altına çıkarılır (`@onlinemenu/ui-kit`, `@onlinemenu/types`, `@onlinemenu/config`).
- Yeni bileşen önce `packages/ui-kit`'e eklenir; uygulama spesifik ise `apps/` altında kalır.

### hurl (Ertelendi)
- Faz 1 sonuna kadar projeye **katılmaz**. Smoke test için `testcontainers-go` + `httptest` yeterli.
- Faz 1 sonunda yeniden değerlendirilir (CI smoke gate ihtiyacı doğarsa).

### Özel Lint Kuralları (delta-v2.md Bölüm 3.7)
- `pool.Query`, `pool.Exec` modül kodunda yasak (SEC-001)
- `SET` (LOCAL'siz) yasak (SEC-001)
- `ON CONFLICT DO UPDATE` outbox tablolarında yasak (DATA-002)
- `UPDATE <module>_outbox SET payload` yasak (DATA-002)
- Başka modülün `{domain,repo,http,events}` paketi import edilemez

### Dil Kuralı
- Kod ve kod içi yorumlar: **İngilizce**
- Dokümanlar (`docs/`, ADR'lar), PR description, commit message: **Türkçe**
- Event şema JSON alanları: İngilizce snake_case, description: Türkçe

### Commit Format
- Türkçe, kısa (< 72 karakter başlık)
- "AI attribution" footer'ı **asla ekleme**

---

## Faz Kısıtlamaları

| Özellik | Faz |
|---|---|
| PayTR sanal POS | 2 |
| Debezium + Kafka CDC | 2 |
| Typesense arama | 2 |
| Unleash feature flag (operasyonel) | 2 |
| MRP, PDKS | 3 |
| Flutter mobil | 4 |

Faz 1'de bunların implementasyonuna **başlama**. Interface/sözleşme tanımlanabilir, implementasyon bekler.

---

## ADR Referansları (Hızlı Erişim)

| Karar | ADR |
|---|---|
| RLS + pgBouncer transaction mode | [SEC-001](docs/adr/SEC-001-rls-transaction-scoped.md) |
| FORCE RLS + iki rol | [SEC-002](docs/adr/SEC-002-rls-force-runtime-role.md) |
| Idempotency-Key | [SEC-003](docs/adr/SEC-003-idempotency-key.md) |
| Cihaz pairing güvenliği | [SEC-004](docs/adr/SEC-004-device-pairing.md) |
| 4 katmanlı authorization | [AUTH-001](docs/adr/AUTH-001-four-layer-authorization.md) |
| Keycloak tek realm | [AUTH-002](docs/adr/AUTH-002-keycloak-single-realm.md) |
| Outbox dispatcher | [DATA-001](docs/adr/DATA-001-outbox-dispatcher.md) |
| Event immutability | [DATA-002](docs/adr/DATA-002-event-immutability.md) |
| Timezone + business day | [DATA-003](docs/adr/DATA-003-timezone-business-day.md) |
| Catalog delta sync | [DATA-004](docs/adr/DATA-004-catalog-delta-sync.md) |
| Feature flag iki katman | [ARCH-001](docs/adr/ARCH-001-feature-flags.md) |
| asynq vs Temporal sınırı | [ARCH-002](docs/adr/ARCH-002-asynq-temporal.md) |
| Typesense (Faz 2) | [ARCH-003](docs/adr/ARCH-003-search-typesense.md) |
| Fiscal adapter interface | [FISCAL-001](docs/adr/FISCAL-001-fiscal-adapter.md) |
| Backup & DR | [OPS-001](docs/adr/OPS-001-backup-dr.md) |
| Tenant offboarding (KVKK) | [OPS-002](docs/adr/OPS-002-tenant-offboarding.md) |
| Rate limiting | [OPS-003](docs/adr/OPS-003-rate-limiting.md) |
| Cost observability | [OPS-004](docs/adr/OPS-004-cost-observability.md) |
| Task runner (Taskfile) | [ARCH-004](docs/adr/ARCH-004-task-runner.md) |
| Frontend monorepo (pnpm workspaces) | [ARCH-005](docs/adr/ARCH-005-frontend-monorepo.md) |

---

## Dokümantasyon Refactor Planı

Bu dosya Faz 1 başında `docs/agent-guide.md`, `docs/tooling.md`, `docs/lint-rules.md` olarak bölünecek. İçerik kaybı yok, sadece organizasyon değişir. Faz 0 sırasında CLAUDE.md büyümeye devam edebilir; **500 satırı geçmemeli**.
