# Online Menu — Offline-First POS & Sync Mimarisi

## Motivasyon

Şube internet bağlantısı kesildiğinde POS çalışmaya devam etmelidir. Adisyon açma, sipariş alma, kasa kapatma ve ödeme alma (nakit + terminal) işlemleri internet gerektirmez. Bağlantı döndüğünde biriken veriler çakışmasız ve kayıpsız şekilde cloud'a aktarılır.

---

## Topoloji

```
┌─────────────────────────────────────────────────────────────────┐
│ Şube (LAN)                                                      │
│                                                                 │
│  ┌──────────────┐   ┌──────────────┐   ┌──────────────────┐    │
│  │ POS Tablet 1 │   │ POS Tablet 2 │   │  Mutfak Ekranı   │    │
│  │  (Wails/Web) │   │  (Wails/Web) │   │   (KDS Ekranı)   │    │
│  └──────┬───────┘   └──────┬───────┘   └────────┬─────────┘    │
│         │                  │                     │              │
│         └──────────────────┴─────────────────────┘              │
│                            │ LAN (HTTP + NATS embedded)         │
│                   ┌────────▼────────┐                           │
│                   │  Local Server   │                           │
│                   │  (Go binary +   │                           │
│                   │   SQLite + NATS)│                           │
│                   └────────┬────────┘                           │
└────────────────────────────┼────────────────────────────────────┘
                             │ HTTPS + NATS (internet varsa)
                    ┌────────▼────────┐
                    │   Cloud API     │
                    │  (PostgreSQL)   │
                    └─────────────────┘
```

---

## Local Server

### Nedir?

Her şubede çalışan küçük bir Go binary'si. POS tabletleri cloud yerine local server'a bağlanır. Cloud erişilemez olsa bile local server bağımsız çalışır.

### Bileşenleri

| Bileşen | Teknoloji | Görev |
|---|---|---|
| HTTP API | chi | POS tabletlerine REST endpoint |
| Event Bus | NATS (embedded) | Tabletler arası real-time sync (mutfak, masa durumu) |
| Yerel DB | SQLite (WAL mode) | Offline veri kalıcılığı |
| Sync Engine | outbox/inbox | Cloud ile çift yönlü veri akışı |

### Hangi Veriyi Saklar?

```
Local Server SQLite:
├── catalog_snapshot      — menü, ürün, fiyat listesi (cloud'dan push edilir)
├── branch_config         — şube ayarları, cihaz listesi
├── tables                — masa durumları
├── checks                — açık adisyonlar
├── orders + order_items  — aktif siparişler
├── payments              — son 30 gün (mutabakat için)
├── outbox_events         — cloud'a gönderilmemiş olaylar
└── inbox_events          — cloud'dan alınan ama henüz uygulanmamış olaylar
```

---

## Veri Otoritesi

Conflict-free çalışmanın temeli: **hangi taraf hangi veriyi yönetir?**

| Veri | Otorite | Gerekçe |
|---|---|---|
| catalog, prices, branch_config | **Cloud** | Şube yetkisizdir, merkezi yönetim |
| user/role | **Cloud (Keycloak)** | Güvenlik kritik |
| stock on_hand | **Cloud** | Envanter doğruluğu cloud'da |
| order, check, payment | **Edge (şube)** | Çevrimdışı üretilir, cloud import eder |
| table status | **Edge** | Sadece o şubede anlamlı |
| device state | **Edge** | Yerel varlık |

---

## Outbox Pattern (Edge → Cloud)

Her yerel yazma işlemi **iki adımda** gerçekleşir:

```
1. SQLite transaction:
   INSERT INTO checks ...;
   INSERT INTO outbox_events (aggregate_type='check', event_type='check.opened.v1', payload=..., is_synced=false);
   COMMIT;

2. Sync worker (arka planda):
   outbox_events'ten is_synced=false kayıtları oku
   → NATS subject'e yayınla (internet varsa)
   → Cloud onaylarsa is_synced=true, synced_at=now()
   → Başarısızsa retry_count++ (max 10, sonra dead-letter)
```

### NATS Subject Yapısı

```
sync.<branch_id>.out.<event_type>.<v>
sync.<branch_id>.in.<event_type>.<v>

Örnekler:
  sync.abc123.out.check.opened.v1
  sync.abc123.in.catalog.updated.v1
```

### İdempotens

Cloud consumer her event'i `id` ile deduplicate eder:

```sql
INSERT INTO checks ... ON CONFLICT (id) DO NOTHING;
UPDATE outbox_events SET is_synced=true WHERE id=$1;
```

---

## Inbox Pattern (Cloud → Edge)

Cloud'dan gelen güncellemeler (katalog değişikliği, fiyat güncellemesi, şube config):

```
1. Cloud yayınlar → NATS subject: sync.<branch_id>.in.*
2. Local Server alır → inbox_events tablosuna ekler (is_applied=false)
3. Inbox worker → inbox_events'i sırayla işler → SQLite'a uygular
4. is_applied=true, applied_at=now()
```

### Inbox İşleme Kuralları

- Her inbox event tipi için idempotent `apply` fonksiyonu zorunlu.
- Hatalı event: `is_applied=false`, hata logu — manuel inceleme.
- `catalog_snapshot` delta değil tam snapshot; `received_at > mevcut_snapshot_at` ise uygula.

---

## Sync Durum Makinesi

```
Local Server durumu:
  ONLINE  → Cloud bağlantısı var, outbox anlık flush edilir
  DEGRADED → Cloud yavaş/aralıklı, outbox birikmeye başlar (uyarı: UI'da "Bağlantı Zayıf")
  OFFLINE → Cloud bağlantısı yok, POS tamamen yerel çalışır (uyarı: UI'da "Çevrimdışı" banner)
  SYNCING → Bağlantı döndü, birikmiş outbox flush ediliyor

Heartbeat:  30 sn aralıklı cloud ping (GET /health)
Timeout:    3 ping başarısız → OFFLINE
Recovery:   Başarılı ping → SYNCING → ONLINE
```

---

## POS İstemcisi Davranışı

### Bağlantı Kaybında

1. POS tablet → local server bağlantısı kesilirse → son bilinen veriyi localStorage'dan kullanır.
2. Local server → cloud bağlantısı kesilirse → POS tablet fark etmez, local server çalışmaya devam eder.
3. Hem local server hem cloud erişilemezse → "Emergency Mode":
   - Sipariş alma ve adisyon açma aktif (SQLite varsa local server üstlenir)
   - Kart ödeme devre dışı (terminal auth gerektiriyor)
   - Nakit ödeme aktif

### Sync Göstergesi

POS UI'ında:

```
┌─────────────────────────────────┐
│ 🟢 Bağlı            | Masa 7    │  ← ONLINE
│ 🟡 Bağlantı Zayıf   | Adisyon   │  ← DEGRADED
│ 🔴 Çevrimdışı (47)  | Kasa      │  ← OFFLINE, 47 = bekleyen outbox sayısı
└─────────────────────────────────┘
```

---

## Cihaz Yönetimi

### Kayıt Akışı

```
1. Yeni cihaz → Local Server'a bağlanır
2. Local Server → Cloud'a cihaz kayıt isteği gönderir
3. Cloud → Keycloak'ta client-credentials oluşturur
4. Cihaza token + branch_id atanır
5. devices tablosuna kaydedilir (fingerprint + firmware)
```

### Uzak Komutlar

NATS subject: `devices.<device_id>.command`

| Komut | Açıklama |
|---|---|
| `config.push` | Güncel branch_config gönder |
| `catalog.refresh` | Katalog snapshot'ını yenile |
| `session.revoke` | Cihaz oturumunu sonlandır |
| `device.wipe` | Yerel veriyi temizle (kayıp/çalıntı) |

---

## Veri Kaybı Önleme

### Katmanlı Dayanıklılık

```
Katman 1: POS tablet localStorage    → local server düşerse 30 dk veri
Katman 2: Local Server SQLite (WAL)  → power kesintisine dayanıklı
Katman 3: Outbox retry (max 10)      → geçici cloud hataları
Katman 4: Dead-letter queue          → manuel müdahale gereken olaylar
Katman 5: Cihaz olay logu            → local server → cloud replay
```

### SQLite WAL Ayarları

```sql
PRAGMA journal_mode=WAL;
PRAGMA synchronous=NORMAL;   -- Hız/güvenlik dengesi
PRAGMA wal_autocheckpoint=1000;
PRAGMA foreign_keys=ON;
```

---

## Ölçek Sınırları

| Senaryo | Değer |
|---|---|
| Şube başına max POS tablet | 20 |
| Local Server başına max açık adisyon | 500 |
| Offline kalınabilecek süre (outbox dolmadan) | 72 saat (tipik kullanımda) |
| Sync süresi (72h birikimi) | < 2 dakika |
| Local Server binary boyutu | < 50 MB |
| SQLite dosya boyutu (1 ay) | < 500 MB |

---

## Faz 0 Minimum Uygulaması

Faz 0'da aşağıdakiler gerçekleştirilir:

- [ ] `cmd/edge/` — local server binary iskeleti (chi + SQLite + embedded NATS)
- [ ] `internal/modules/edge_sync/` — outbox publisher, inbox consumer
- [ ] `migrations/edge_sync/0001_outbox_inbox.sql` — outbox + inbox tabloları
- [ ] Heartbeat döngüsü (30 sn)
- [ ] NATS upstream bağlantısı + reconnect logic

Gerçek sync protokolü Faz 1'de tamamlanır.
