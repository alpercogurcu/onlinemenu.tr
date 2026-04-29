# Online Menu — Ürün Yol Haritası

## Ürün Vizyonu

Türkiye pazarı için modüler, çok-kiracılı (multi-tenant) bir POS & işletme yönetim platformu. Zincir/franchise işletmeler, food truck, restoran, bar, market, imalat ve depo şubelerini aynı çatı altında yönetir. Ürün **modül modül satılır** — her müşteri yalnızca ihtiyaç duyduğu paketler için ücret öder. POS **offline-first**'tir; internet kopmalarında şube local server üzerinde çalışmaya devam eder.

---

## Tenant & Şube Modeli

```
İşletme (Tenant)
└── Şube
    ├── Sahiplik Tipi: şube | franchise
    └── İşleyiş Tipi: restoran | bar | market | food_truck | imalat | depo
```

- Her işletme bir tenant oluşturur (tek veya zincir).
- Tek şubeli işletmeler de şube katmanını kullanır.
- Depolar ve imalathaneler belirli şubelere hizmet verir; öncelik/kural tabanlı sevkiyat yönlendirmesi tanımlanabilir.

---

## Modül Paketleri (Satış Birimleri)

| Paket | Modüller | Hedef müşteri |
|---|---|---|
| **Temel** (zorunlu) | identity, tenant, hr-core | Tüm kullanıcılar |
| **POS** | catalog, pos, payment, edge-sync | Restoran/bar/food truck |
| **Stok** | inventory, party | Stok takibi yapan şubeler |
| **Billing** | billing (provider seçimi) | E-fatura gerektiren şubeler |
| **PDKS** *(Faz 3)* | pdks, hr-advanced | Personel yoğun işletmeler |
| **Analitik** *(Faz 2+)* | reporting | Zincir yönetimi |

---

## Faz 0 — Proje İskeleti (Hafta 1–2)

- [ ] Repo yapısı, `go-arch-lint`, `golangci-lint`, CI pipeline
- [ ] `deploy/docker-compose.dev.yml` — tüm bağımlılıklar ayağa
- [ ] SOPS + age bootstrap secret şifrelemesi (`.env.sops`)
- [ ] `uber-go/fx` ile modül wiring iskeleti (`cmd/api/main.go`)
- [ ] Graceful shutdown: `signal.NotifyContext` + `errgroup` (`cmd/api`, `cmd/edge`)
- [ ] `internal/platform/{db, eventbus, auth, otel, vault}` cross-cutting katmanlar
- [ ] Postgres RLS altyapısı + sızıntı testi
- [ ] `goleak` test suite entegrasyonu (`TestMain`)
- [ ] `identity` + `tenant` modülleri (CRUD + Keycloak sync)
- [ ] İlk event sözleşmeleri (`tenant.created.v1`, `branch.created.v1`)
- [ ] `ko` ile CI container build pipeline
- [ ] `docs/` — bu dört dosya

---

## Faz 1 — Satılabilir MVP (Hafta 3–10)

- [ ] `party` — tedarikçi & müşteri (cari)
- [ ] `catalog` — menü, ürün, varyant/modifier, fiyat listesi, şube görünürlüğü
- [ ] `hr-core` — personel özlüğü, şube ataması
- [ ] `pos` — masa planı, adisyon, sipariş, mutfak ekranı (WebSocket), kasa kapatma
- [ ] `payment` — nakit + POS terminali (Ingenico/Verifone)
- [ ] `inventory` — depo, stok seviyeleri, sevkiyat, şube→depo yönlendirme
- [ ] `billing` — EDM provider (mevcut Go kodu), Paraşüt/Mikro/Logo stub'lar
- [ ] `edge-sync` — local server binary, outbox/inbox, sync protokolü
- [ ] Admin paneli (Next.js 16) — temel yönetim ekranları
- [ ] POS istemcisi (Wails v2 + React) — masa, sipariş, kasa akışı
- [ ] k6 yük testi — 500 aktif POS simülasyonu

---

## Faz 2 — Ödeme & Ölçek

- [ ] PayTR sanal POS entegrasyonu
- [ ] Debezium + Kafka — analytics pipeline + ilerideki mikroservis split'i için CDC
- [ ] Meilisearch — menü & müşteri full-text arama
- [ ] Unleash / GrowthBook — modül bazlı feature flag (tenant/şube düzeyinde)
- [ ] Paraşüt, Mikro, Logo billing provider'larını production-grade'e çıkarma
- [ ] Raporlama modülü (satış, stok, cari bakiye)

---

## Faz 3 — Derinleşme

- [ ] **MRP** — Temporal.io workflow'ları ile talep planlaması
- [ ] **PDKS** — QR ile giriş/çıkış, shift yönetimi, mesai hesaplama, hakediş
- [ ] HR gelişmiş: izin, puantaj, bordro entegrasyonu
- [ ] CRM derinleşme — müşteri segmentleri, sadakat puanı
- [ ] E-fatura arşiv & denetim raporları

---

## Faz 4 — Mobil & Mikroservis Ayırma

- [ ] Flutter mobil — müşteri self-service, yönetici dashboard
- [ ] İlk ayrılacak servisler: **payment** (PCI scope daraltma), **billing**, **identity**, **reporting**
- [ ] Her ayrılan servis kendi veritabanına taşınır (RLS pattern sabit kalır)
- [ ] API Gateway (Kong / Traefik) — servis keşfi + rate limit

---

## Kapsam Dışı (Şimdilik)

| Özellik | Erteleme sebebi |
|---|---|
| PayTR sanal POS | Faz 2 |
| MRP, PDKS | Faz 3 |
| Flutter mobil | Faz 4 |
| Kafka/Debezium | Mikroservis ayırma sinyali gelince |
| Keycloak realm-per-tenant | Sprint 1'de kararlaştırılacak |

---

## Ödeme Yöntemleri

| Yöntem | Faz | Notlar |
|---|---|---|
| Nakit | 1 | Kasa kapatma akışı |
| Kredi/banka kartı (terminal) | 1 | Ingenico / Verifone cihaz entegrasyonu |
| PayTR sanal POS | 2 | Web ödeme linki + webhook |
| Havale / EFT | 2 | Manuel eşleştirme + cari takip |

---

## Tech Stack Özeti

| Katman | Teknoloji |
|---|---|
| Backend | Go 1.26+, chi v5, sqlc 1.30, golang-migrate, asynq 0.26+ |
| Veritabanı | PostgreSQL 18 (RLS), Redis 8 |
| Messaging | NATS JetStream 2.12+ (monolith), Kafka (Faz 2+ CDC) |
| Workflow | Temporal.io 1.30+ (Faz 3) |
| Kimlik | Keycloak 26.x + OPA 0.68+ |
| Gözlemlenebilirlik | OpenTelemetry + Grafana/Loki/Tempo/Prometheus |
| Secrets | HashiCorp Vault |
| Nesne depolama | MinIO (fatura PDF, özlük, menü görseli) |
| Admin frontend | Next.js 16 App Router + shadcn/ui (CLI v4) + TanStack Query |
| POS desktop | Wails v2 (stable) + React + shadcn — v3 alpha izleniyor |
| Mobil (Faz 4) | Flutter |
| Infra | Docker Compose (dev), Kubernetes (Faz 2+) |
