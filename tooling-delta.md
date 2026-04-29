# Tooling Delta — AI İçin Talimat

> **Bu doküman nedir?**
> CLAUDE.md'ye eklenecek yeni araçlar + mevcut araçlarla ilgili netleştirmeler. Operatör ile danışman arasındaki değerlendirme sonrası karara bağlandı.
>
> **Bu doküman ne yapar?**
> - CLAUDE.md + tech stack'e **ekleyeceğin** araçları listeler
> - İki yeni mini ADR talep eder
> - CLAUDE.md'nin bölme planını başlatır (Faz 1 sonuna kadar)
> - Ertelenen bir aracın **neden** ertelendiğini belgeler

---

## 1. Kabul Edilen Araçlar

### 1.1 Taskfile (Faz 0 — Merkezi Komut Çalıştırıcı)

**Kazanım:** Tüm CLI komutlarının tek, AI-dostu, cross-platform giriş noktası.

**Kural:** AI **her komut çalıştırmak istediğinde** `task <name>` kullanacak. `go run`, `npm run`, `docker compose`, `migrate`, `sqlc generate` gibi komutlar doğrudan çağrılmaz; Taskfile üzerinden koşar. Make yerine Taskfile tercih edildi çünkü (a) YAML format AI için daha okunabilir, (b) cross-platform Windows/macOS/Linux tutarlı, (c) bağımlılık grafiği (`deps:`) net.

**İlk Taskfile iskeleti (`Taskfile.yml`):**

```yaml
version: '3'

vars:
  GO_MODULE: onlinemenu.tr

tasks:
  # --- Development ---
  dev:
    desc: "Dev ortamı — docker-compose + air ile hot-reload API"
    deps: [compose:up]
    cmds:
      - air -c .air.toml

  compose:up:
    desc: "docker-compose.dev.yml'deki servisleri başlat"
    cmds:
      - docker compose -f deploy/docker-compose.dev.yml up -d

  compose:down:
    desc: "Dev servislerini durdur"
    cmds:
      - docker compose -f deploy/docker-compose.dev.yml down

  # --- Build & Run ---
  build:
    desc: "Tüm Go binary'lerini build et (ko ile)"
    cmds:
      - ko build ./cmd/...

  build:local:
    desc: "Lokal Go build (ko olmadan)"
    cmds:
      - go build -o bin/api ./cmd/api
      - go build -o bin/edge ./cmd/edge
      - go build -o bin/worker ./cmd/worker

  # --- Test ---
  test:
    desc: "Tüm testler"
    cmds:
      - go test ./... -race -count=1

  test:unit:
    desc: "Sadece unit testler (integration hariç)"
    cmds:
      - go test ./... -race -short

  test:integration:
    desc: "testcontainers integration testler"
    cmds:
      - go test ./... -race -run Integration -timeout 10m

  test:rls-leak:
    desc: "RLS sızıntı testleri (SEC-001, SEC-002)"
    cmds:
      - go test ./internal/platform/db -run TestRLS -v

  # --- Code Quality ---
  lint:
    desc: "golangci-lint + go-arch-lint"
    cmds:
      - golangci-lint run
      - go-arch-lint check

  fmt:
    desc: "Go kod formatla"
    cmds:
      - gofmt -w .
      - goimports -w .

  security:scan:
    desc: "gosec + trivy güvenlik taraması"
    cmds:
      - gosec -exclude-generated ./...
      - trivy fs --severity HIGH,CRITICAL .

  # --- Code Generation ---
  gen:
    desc: "Tüm kod üretimi (sqlc, events, openapi)"
    deps: [gen:sqlc, gen:events]

  gen:sqlc:
    desc: "sqlc ile DB kodu üret"
    cmds:
      - sqlc generate

  gen:events:
    desc: "Event JSON Schema → Go struct"
    cmds:
      - go run ./cmd/eventgen contracts/events internal/platform/events/generated

  # --- Database ---
  migrate:up:
    desc: "Tüm modül migration'larını çalıştır"
    cmds:
      - go run ./cmd/migrate up

  migrate:down:
    desc: "Son migration'ı geri al"
    cmds:
      - go run ./cmd/migrate down 1

  migrate:new:
    desc: "Yeni migration oluştur (task migrate:new MODULE=pos NAME=add_table)"
    cmds:
      - migrate create -ext sql -dir migrations/{{.MODULE}} {{.NAME}}

  # --- Secrets ---
  sops:decrypt:
    desc: ".env.sops → .env (lokal dev için)"
    cmds:
      - sops -d .env.sops > .env

  # --- Frontend ---
  web:dev:
    desc: "Admin paneli dev server (Next.js)"
    dir: web/admin
    cmds:
      - pnpm dev

  pos:dev:
    desc: "POS desktop dev (Wails)"
    dir: web/pos
    cmds:
      - wails dev

  # --- CI ---
  ci:
    desc: "CI pipeline lokalde simüle et"
    cmds:
      - task: lint
      - task: test
      - task: security:scan
      - task: build

  # --- Cleanup ---
  clean:
    desc: "Build artifact'larını sil"
    cmds:
      - rm -rf bin/ dist/ tmp/
```

**CLAUDE.md'ye eklenecek satır** (Kod Standartları bölümüne):

> **Komut çalıştırma:** Her CLI komutu `task <name>` üzerinden koşturulur. `go run`, `npm run`, `docker compose`, `migrate` gibi komutları doğrudan çağırma; Taskfile'da karşılığı yoksa önce ekle. Task listesi: `task --list`.

**CLAUDE.md'ye eklenecek satır** (Tech Stack tablosuna):

| Katman | Teknoloji |
|---|---|
| Komut çalıştırıcı | Taskfile (Makefile yerine) |

---

### 1.2 air (Faz 0 — Hot Reload)

**Kazanım:** Go kod değişiminde otomatik rebuild + restart. Manuel `go build && ./bin/api` döngüsünden kurtarır.

**Kural:** AI `air` komutunu **doğrudan çağırmaz**; dev ortamı her zaman `task dev` ile başlatılır (arka planda air koşar).

**`.air.toml` dosyası** (proje kökünde):

```toml
root = "."
tmp_dir = "tmp"

[build]
  cmd = "go build -o ./tmp/api ./cmd/api"
  bin = "./tmp/api"
  include_ext = ["go", "tpl", "tmpl", "html"]
  exclude_dir = ["tmp", "bin", "web", "vendor", "node_modules", ".git"]
  exclude_regex = ["_test\\.go"]
  delay = 1000 # ms
  stop_on_error = true
  send_interrupt = true
  kill_delay = 500 # ms

[log]
  time = true

[color]
  main = "magenta"
  watcher = "cyan"
  build = "yellow"
  runner = "green"
```

**CLAUDE.md'ye eklenecek satır** (Tech Stack tablosuna):

| Katman | Teknoloji |
|---|---|
| Dev hot-reload | air (`task dev` ile başlatılır) |

---

### 1.3 gosec + trivy (Faz 0 — CI Güvenlik Taraması)

**Kazanım:** Otomatik güvenlik açığı tespiti. gosec Go kod pattern'lerini, trivy container + dependency CVE'lerini tarar.

**Kural:**
- CI'da zorunlu (`task security:scan` CI pipeline'ın parçası).
- AI lokal çağırmak zorunda **değil**; PR öncesi `task ci` ile simüle eder.
- **False-positive yönetimi:** `gosec` bazı pattern'lerde hatalı uyarı verir (özellikle SQL injection G202). `.gosec.yml` ile exclusion tanımlanır — AI her seansta aynı false-positive'leri kovalamaz.

**`.gosec.yml` başlangıç konfigürasyonu:**

```yaml
# Dosya üretilirken AI şu kurallar hakkında farkında olsun:
# - G202 (SQL injection via string concatenation): sqlc üretilen kod
#   ve `SET LOCAL app.tenant_id = $1` pattern'inde false-positive verir.
#   Sqlc generated kodlar exclude edilir. SET LOCAL için parametrize
#   edilmiş pattern kullanıldığı için güvenlidir.
# - G104 (Errors unhandled): defer'li close'larda görmezden gelinir.

global:
  nosec: false
  audit: true

rules:
  # Generated kodlar taramadan hariç
  exclude-generated: true

  # Dosya pattern exclusions
  exclude-paths:
    - "internal/platform/db/generated/.*"
    - ".*_mock\\.go$"
    - ".*\\.pb\\.go$"

  # Kural bazlı exclusions
  exclude-rules:
    - G104 # errors unhandled — linter (errcheck) zaten bakıyor

# Severity threshold
severity: medium
confidence: medium
```

**CLAUDE.md'ye eklenecek satır** (Kod Standartları bölümüne):

> **Güvenlik taraması:** `task security:scan` — gosec (Go kod) + trivy (container/dependency CVE). CI'da zorunlu. Lokal debug için çağırılır; her seans aynı false-positive'i kovalama — `.gosec.yml` exclusion'ları güncelle.

---

### 1.4 pnpm workspaces (Faz 0 — Frontend Monorepo)

**Durum:** Baseline'da zaten vardı, **netleştiriliyor**.

**Kazanım:** Admin (Next.js) + POS (Wails + React) arasında `packages/ui-kit` ve `packages/types` paylaşımı.

**Dizin yapısı:**

```
web/
├── pnpm-workspace.yaml
├── package.json              (root — devDependencies ortak)
│
├── apps/
│   ├── admin/                (Next.js 16 App Router)
│   │   ├── package.json
│   │   └── ...
│   │
│   └── pos-desktop/          (Wails v2 + React frontend)
│       ├── package.json
│       ├── frontend/
│       └── ...
│
└── packages/
    ├── ui-kit/               (shadcn wrapper + ortak komponentler)
    │   ├── package.json      (name: @onlinemenu/ui-kit)
    │   ├── src/
    │   │   ├── button.tsx
    │   │   ├── card.tsx
    │   │   └── pos/          (POS-özel komponentler)
    │   └── ...
    │
    ├── types/                (TypeScript tipler — event şemalarından üretilir)
    │   ├── package.json      (name: @onlinemenu/types)
    │   └── src/
    │       ├── events/
    │       └── api/
    │
    └── config/               (eslint, prettier, tsconfig ortak)
        ├── eslint-config/
        ├── tsconfig/
        └── tailwind-config/
```

**`web/pnpm-workspace.yaml`:**

```yaml
packages:
  - 'apps/*'
  - 'packages/*'
```

**CLAUDE.md'ye eklenecek bölüm** (yeni başlık):

> **Frontend Monorepo:** `web/` altında pnpm workspaces. `apps/admin` ve `apps/pos-desktop` `packages/ui-kit`, `packages/types`, `packages/config`'ten yararlanır. Yeni paket eklerken `pnpm-workspace.yaml` otomatik picks up eder. İçe import: `import { Button } from '@onlinemenu/ui-kit'`.

**Kural:** `apps/admin` ve `apps/pos-desktop` **birbirinden import edemez**. Ortak kod `packages/*` altına çıkarılır. Bu kural `.eslintrc`'de `no-restricted-imports` ile zorlanır.

---

## 2. Talep Edilen Mini ADR'lar

Bu ikisi yeni karar niteliğinde, ADR hak ediyor:

### 2.1 ADR-ARCH-004: Task Runner Seçimi (Taskfile)

**Durum:** Kabul Edildi
**Tarih:** 2026-04-19

**Bağlam:** Ekip AI. Birden fazla AI seansı, farklı bağlamlarda aynı projeye dokunur. "Bu komut nasıl çalıştırılır?" sorusunun tek ve tutarlı bir cevabı olmalı. Make tab duyarlılığı, shell syntax farklılıkları (Bash vs POSIX sh), Windows uyumsuzluğu AI'ı şaşırtır.

**Karar:** Taskfile (go-task) kullanılır. Makefile projeye girmez.

**Sonuçlar:**
- İyi: Cross-platform (Windows dev makineler dahil). YAML formatı AI için okunaklı. Bağımlılık grafiği açık (`deps:`).
- İyi: `task --list` ile keşif kolay; AI yeni bir komut ihtiyacı olduğunda önce bu listeye bakar.
- Kötü: Ekip üyeleri Make'e alışkınsa küçük öğrenme eğrisi (AI için önemsiz).
- Risk: Taskfile içeriği şişebilir. 50+ task sonrası dosyayı parçalamak gerekebilir (`Taskfile.yml` + `tasks/*.yml` include).

**Değerlendirilen alternatifler:**
- **Make:** Reddedildi. Tab hataları AI-friendly değil, Windows yok.
- **npm scripts:** Reddedildi. Go-ağırlıklı projede tuhaf.
- **just:** Değerlendirildi. Taskfile ile benzer ama community + Go ecosystem entegrasyonu daha zayıf.
- **Mage:** Reddedildi. Go kodu olarak task yazımı overhead.

**Dosya:** `docs/adr/ARCH-004-task-runner.md`

---

### 2.2 ADR-ARCH-005: Frontend Monorepo Yapısı (pnpm workspaces)

**Durum:** Kabul Edildi
**Tarih:** 2026-04-19

**Bağlam:** Admin paneli (Next.js 16) ve POS desktop (Wails v2 + React) arasında UI komponent, tip, konfigürasyon paylaşımı gerekli. Aynı "Button" komponentini iki ayrı yerde maintain etmek inkar edilmez bir anti-pattern.

**Karar:** `web/` altında pnpm workspaces. `apps/admin`, `apps/pos-desktop` uygulamalar; `packages/ui-kit`, `packages/types`, `packages/config` paylaşılan paketler.

**Sonuçlar:**
- İyi: %90 kod paylaşımı hedefi (baseline roadmap'te belirtilmiş) yapısal olarak mümkün.
- İyi: shadcn komponent tek yerde tutulur; tema/design token değişiklikleri iki uygulamaya aynı anda yayılır.
- İyi: Event şemalarından üretilen TypeScript tipleri (`packages/types`) backend event sözleşmeleriyle uyumlu; drift önlenir.
- Kötü: Workspace bağımlılık çözümü (hoisting) bazen beklenmedik sonuç verir; `pnpm install --frozen-lockfile` CI'da zorunlu.
- Risk: `apps` arası import kolayca yapılabilir (eslint zorlaması şart).

**Değerlendirilen alternatifler:**
- **Nx / Turborepo:** Reddedildi (şimdilik). Overkill — 2 uygulama + 3 paket için build cache gerekli değil. Projeyi büyüdüğünde (mobil Flutter'den sonra ek web app eklenirse) Turborepo değerlendirilir.
- **npm/yarn workspaces:** Reddedildi. pnpm disk + install hızı avantajı net.
- **Tek repo, copy-paste paylaşım:** Reddedildi. Drift garantili.

**Dosya:** `docs/adr/ARCH-005-frontend-monorepo.md`

---

## 3. Ertelenen Araç — hurl

### 3.1 Neden Erteleniyor?

**Kazanım gerçek:** Git'te versiyonlu, human-readable API test dosyaları.

**Ama:** Test stratejisinde zaten şunlar var:
- `testcontainers-go` — integration test (Go kodu, gerçek Postgres/NATS/Redis)
- `k6` — yük testi
- OpenAPI spec — endpoint contract
- `stretchr/testify` + `httptest` — unit + HTTP test

Hurl'un dolduracağı boşluk: manuel smoke testing + CI acceptance test. Bu boşluk testcontainers + httptest + golden file pattern ile de doldurulabilir. Üçüncü bir test frameworkü eklemek **test coverage'ı artırmaz**, **operasyonel yükü artırır**:
- Test üç ayrı yere dağılır (Go testleri + k6 + hurl)
- Coverage ölçümü karışır (hurl Go coverage'a girmez)
- CI pipeline uzar

**Karar:** **Faz 1 sonunda** yeniden değerlendirilir. Eğer o noktada:
1. AI'ın manuel smoke test yapması gereken senaryolar çoksa
2. CI'da "deploy sonrası smoke" adımı ihtiyacı doğmuşsa
3. Non-Go dev araçları API test yazmak istiyorsa (admin paneli için)

— o zaman hurl eklenir.

### 3.2 CLAUDE.md'de Kayıt

**Kural:** AI hurl'u proje kapsamına **katmaz**. Test yazarken Go-native araçları kullanır (testcontainers + httptest). "Smoke test gerekli" önerisi gelirse, Taskfile'a `test:smoke` görevi eklenir — içerik testcontainers + curl kombinasyonu olabilir.

---

## 4. CLAUDE.md Bölme Planı (Faz 1 Başı İçin Hazırlık)

### 4.1 Şu Anki Durum

CLAUDE.md ~300 satır. Bu güncelleme ile ~330'a çıkar. Her eklemede büyüyor; AI her seansta baştan okuyor. 500+ satır olursa:
- Önemli kurallar uzun listede kaybolur
- Ufak değişiklikler için diff şişer
- AI "hangi kural hangi bağlamda" ayrımında hata yapmaya başlar

### 4.2 Önerilen Yeni Yapı (Faz 1 Başında Uygulanır)

```
CLAUDE.md                     (ana giriş, ~150 satır, keskin)
├── Tech stack özeti tablosu
├── Zorunlu mimari kurallar (5-7 madde, ADR linkli)
├── Dil/commit standartları
├── Faz kısıtlamaları özet
└── "Detay için:" bölümü linkleri

docs/agent-guide.md           (~200 satır — detaylı agent kılavuzu)
└── Hangi agent ne zaman çağrılır, örneklerle

docs/tooling.md               (~150 satır — tüm araçlar tek yerde)
└── air, Taskfile, ko, gosec, trivy, SOPS, vb.

docs/lint-rules.md            (~100 satır — özel lint kuralları)
└── Yasaklar + gerekçe + örnekler

docs/adr/INDEX.md             (ADR listesi + okuma sırası)
```

**CLAUDE.md kısa kaldığı sürece her seansta okunur; detaylar gerektiğinde linklerden çekilir.** Bu aynı zamanda Claude Code'un lazy-loading davranışıyla uyumlu.

### 4.3 Şimdi Ne Yapılacak?

**Şimdi:** Bu tooling delta'sı uygulandığında CLAUDE.md aynı dosyada kalır, 330 satıra çıkar. Kabul.

**Faz 1 başında:** AI'a ayrı bir "CLAUDE.md refactor" görevi verilir — yukarıdaki yapıya bölünür. Bu bir PR'dır, içerik kaybı olmaz, sadece organizasyon değişir.

**CLAUDE.md'ye şimdi eklenecek satır** (en sona, ayrı başlık):

> **Dokümantasyon Refactor Planı:** Bu dosya Faz 1 başında `docs/agent-guide.md`, `docs/tooling.md`, `docs/lint-rules.md` olarak bölünecek. İçerik kaybı yok, sadece organizasyon. Faz 0 sırasında CLAUDE.md büyümeye devam edebilir ama 500 satırı geçmemeli.

---

## 5. Uygulama Sırası

AI bu delta'yı şu sırada uygular:

1. **PR 1:** `Taskfile.yml` + `.air.toml` + `.gosec.yml` dosyalarını kökte oluştur. İlk iskeleti yukarıdaki şablonlardan al.
2. **PR 2:** `docs/adr/ARCH-004-task-runner.md` ve `docs/adr/ARCH-005-frontend-monorepo.md` ADR dosyalarını oluştur. Yukarıdaki içerikleri tam ADR formatında yaz.
3. **PR 3:** CLAUDE.md güncellemesi — bu delta'daki "CLAUDE.md'ye eklenecek satır" bloklarını ilgili bölümlere işle. Tek PR.
4. **PR 4:** `web/` dizin yapısını `pnpm-workspace.yaml` ile kur. `packages/ui-kit`, `packages/types`, `packages/config` iskeletleri oluştur (içi boş — Faz 0 ilerleyişinde dolacak).
5. **PR 5:** CI pipeline'a `task ci` adımı ekle — lint + test + security:scan + build.

Her PR bağımsız; sıra önemli ama paralel olmayabilir.

---

## 6. AI İçin Not

- **hurl eklemeye çalışma.** Bu kararla ertelendi.
- **Make eklemeye çalışma.** Taskfile seçildi.
- **Bu delta v2 delta dokümanıyla çelişmiyor.** İkisi birlikte uygulanır.
- **Yeni bir araç önerisi geldiğinde** önce CLAUDE.md'de karşılığı var mı kontrol et; yoksa bu delta gibi yapılandırılmış bir öneri oluştur — operatöre sor.
- **Taskfile'a yeni task eklemek** kod değişikliği seviyesinde PR açar; doğrudan ekle, ADR gerektirmez.
- **Taskfile komut isimleri** yukarıdaki konvansiyona uyar: `<domain>:<action>` (ör: `migrate:up`, `test:integration`). İsimlendirme tutarlılığı AI'ın keşif kolaylığı için önemli.

---

## Revizyon Geçmişi

| Versiyon | Tarih | Açıklama |
|---|---|---|
| v1 | 2026-04-19 | Tooling delta: Taskfile, air, gosec+trivy, pnpm workspaces netleştirme. hurl ertelendi. ARCH-004, ARCH-005 ADR'ları talep edildi. CLAUDE.md bölme planı Faz 1'e kaydedildi. |