# Monorepo Yeniden Yapılandırma & POS Ölçekleme Tasarımı

**Tarih:** 2026-05-12  
**Durum:** Onaylandı  
**Konu:** Go backend'i `backend/` altına taşıma, flat modül yapısı, deployment grubu bazlı ölçekleme

---

## 1. Motivasyon

Mevcut yapıda Go kodu repo kökünde yer alıyor; frontend (`web/`), mobile (Faz 4) ve desktop (Wails) ile görsel ayrım yok. Hedef: her teknoloji katmanının repo kökünde açıkça görünmesi ve POS modülünün yoğun trafik dönemlerinde bağımsız ölçeklenebilmesi.

---

## 2. Hedef Repo Yapısı

```
onlinemenu.tr/
├── backend/                    ← Go monolith (taşınıyor)
│   ├── cmd/
│   │   ├── api/                ← tam monolit (dev + küçük tenant)
│   │   ├── api-pos/            ← pos + catalog + edge_sync
│   │   ├── api-core/           ← identity + tenant + hr + party
│   │   ├── api-finance/        ← payment + billing + notification
│   │   ├── worker/             ← asynq tüketicileri
│   │   ├── edge/               ← şube local server
│   │   └── migrate/            ← migration koşucusu
│   ├── internal/
│   │   ├── modules/            ← flat liste, alfabetik sıra
│   │   │   ├── billing/
│   │   │   ├── catalog/
│   │   │   ├── edge_sync/
│   │   │   ├── hr/
│   │   │   ├── identity/
│   │   │   ├── inventory/
│   │   │   ├── manufacturing/
│   │   │   ├── notification/
│   │   │   ├── party/
│   │   │   ├── payment/
│   │   │   ├── pos/
│   │   │   └── tenant/
│   │   └── platform/
│   ├── migrations/
│   ├── contracts/
│   ├── configs/
│   ├── go.mod                  ← module: onlinemenu.tr (değişmez)
│   └── go.work
├── web/                        ← mevcut (değişmez)
│   ├── apps/admin/
│   └── apps/pos-desktop/
├── mobile/                     ← Flutter, Faz 4 (boş klasör)
├── deploy/
├── docs/
└── Taskfile.yml                ← root'ta kalır, tüm projeyi yönetir
```

### 2.1 Go Module Adı

`go.mod` içindeki `module onlinemenu.tr` satırı **değişmez**. Dosya `backend/go.mod`'a taşınır ancak module adı aynı kalır; bu nedenle tüm import path'ler (`onlinemenu.tr/internal/...`) herhangi bir değişiklik gerektirmez.

---

## 3. Modül Organizasyonu

Modüller `backend/internal/modules/` altında **flat** tutulur. Domain gruplaması (core/, finance/ vb.) uygulanmaz.

**Gerekçe:**
- Toplam modül sayısı Faz 1–4 sonunda ~12–15; domain gruplamak bu ölçekte net fayda sağlamaz.
- Derin import path'ler (`onlinemenu.tr/internal/modules/finance/payment`) okunabilirliği düşürür.
- `go-arch-lint` kuralları flat yapıda daha basit tanımlanır.
- Yeni modül eklerken "hangi domain?" tartışması olmaz.

Her modül mevcut kapalı kutu prensibini korur:

```
backend/internal/modules/<name>/
├── public/     ← dışarıya açık: interface + DTO
├── domain/     ← kapalı: iş mantığı
├── repo/       ← kapalı: DB erişimi (sqlc)
├── http/       ← kapalı: HTTP handler'lar
├── events/     ← kapalı: event publisher/subscriber
└── module.go   ← fx.Module tanımı
```

---

## 4. Deployment Grubu Bazlı Ölçekleme (Yol 2)

### 4.1 Strateji

Tek codebase, tek `go.mod`, birden fazla `cmd/` binary. Her binary `uber-go/fx` ile yalnızca ilgili modül grubunu yükler.

| Binary | Yüklenen Modüller | Ölçek Baskısı |
|---|---|---|
| `api` | tümü | Dev / küçük tenant |
| `api-pos` | pos, catalog, edge_sync | Yüksek (anlık sipariş) |
| `api-core` | identity, tenant, hr, party | Düşük |
| `api-finance` | payment, billing, notification | Orta |
| `worker` | asynq tüketicileri | Ayrı ölçek |

### 4.2 Docker Compose Yapısı

```yaml
services:
  api-pos:
    image: onlinemenu.tr/api-pos
    deploy:
      replicas: 5

  api-core:
    image: onlinemenu.tr/api-core
    deploy:
      replicas: 1

  api-finance:
    image: onlinemenu.tr/api-finance
    deploy:
      replicas: 2

  api:
    image: onlinemenu.tr/api   # dev ortamı — tek container
    profiles: ["dev"]
```

### 4.3 Modüller Arası İletişim

Deployment grubu ayrımı iletişim kalıbını değiştirmez:

- **Aynı binary içi:** `public/` interface üzerinden in-process çağrı
- **Farklı binary'ler arası:** NATS JetStream event (zaten mevcut kalıp)

`api-pos` bir `identity` bilgisine ihtiyaç duyuyorsa **NATS event birincildir**; senkron sorgu zorunluysa `api-core`'un HTTP endpoint'i çağrılır (istisnai durum). Doğrudan DB erişimi yasak kuralı tüm gruplarda geçerlidir.

### 4.4 WebSocket / KDS Sticky Session

POS mutfak ekranı (KDS) WebSocket bağlantıları `api-pos` replikalarına dağılır. NATS pub/sub relay bağlantı durumunu replikalar arasında senkronize eder; uygulama katmanında sticky session gerekmez.

### 4.5 Evrim Yolu

```
Faz 1:   cmd/api (tek binary, dev)
Faz 1+:  cmd/api-pos, cmd/api-core, cmd/api-finance eklenir
Faz 2+:  Kafka CDC ile tam mikroservis çıkarımı (gerekirse)
```

---

## 5. Taskfile Stratejisi

Root `Taskfile.yml` katman bazlı görevleri birleştirir:

```yaml
includes:
  backend: ./backend/Taskfile.yml
  web:     ./web/Taskfile.yml
  mobile:  ./mobile/Taskfile.yml  # Faz 4

tasks:
  dev:
    desc: "Tüm stack'i başlat"
    cmds:
      - task: backend:dev
      - task: web:dev
```

`backend/Taskfile.yml` mevcut Go görevlerini (`dev`, `test`, `lint`, `migrate`, `build`) içerir.

---

## 6. Kapsam Dışı

- `web/` frontend monorepo yapısı değişmez (ADR-ARCH-005 geçerli).
- `mobile/` Faz 4'e kadar boş klasör olarak kalır; içerik eklenmez.
- `go.mod` module adı (`onlinemenu.tr`) değişmez; import path refactor yapılmaz.
- Kafka CDC / tam mikroservis çıkarımı bu tasarımın kapsamı dışında (Faz 2+).

---

## 7. Riskler & Azaltma

| Risk | Azaltma |
|---|---|
| `go-arch-lint` kuralları `backend/` taşımasıyla bozulabilir | `.go-arch-lint.yml` path prefix'leri güncellenir |
| CI pipeline `go build ./...` referansları kırılır | GitHub Actions workflow'ları `backend/` prefix ile güncellenir |
| `ko` build config güncellenmeli | `ko` `main` package path'leri `backend/cmd/...` olarak güncellenir |
| `air` hot-reload config | `backend/.air.toml` çalışma dizini ayarlanır |
