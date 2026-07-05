# k6 Yük Testi — POS Satış Akışı + KDS WebSocket

Sprint-8, ROADMAP Faz 1 maddesi: **"k6 yük testi — 500 aktif POS simülasyonu"**.

Bu dizin, POS'un gerçek kasiyer satış döngüsünü (adisyon aç → sipariş ekle →
öde → kapat) ve mutfak ekranı (KDS) WebSocket fan-out'unu ölçen k6
senaryolarını içerir. Uygulama koduna dokunulmamıştır — bu yalnızca ölçüm
altyapısıdır.

## Dosyalar

```
backend/loadtest/
  seed.sql              -- k6 için kasiyer terminali havuzu (50 person + membership)
  k6/
    lib/common.js       -- paylaşılan sabitler, dev-login, katalog bootstrap, thresholdlar
    pos_sale_flow.js     -- tek senaryo: kasiyer satış döngüsü (HTTP)
    kds_ws.js             -- tek senaryo: KDS WebSocket bağlantısı + olay gecikmesi
    mixed_load.js         -- karma profil: ikisi birden, tek k6 koşusunda (asıl deliverable)
```

## Ön koşullar

1. Docker Desktop çalışıyor olmalı.
2. `k6` kurulu olmalı: `brew install k6` (bu sprint'te böyle kuruldu).
3. Core stack ayakta: `task compose:up:core` (postgres, redis, nats, vault —
   Keycloak/observability şart değil, k6 `/dev/login` kullanır).
4. Migration'lar uygulanmış olmalı.
5. Dev-seed + loadtest seed yüklenmiş olmalı: `task backend:loadtest:seed`.
6. `cmd/api` çalışıyor olmalı (`APP_ENV=dev` ile) — `task backend:dev` (air)
   normal yoldur. **Ayrı bir worker sürecine gerek yoktur**: outbox
   dispatcher (`platform/outbox.Register`) ve KDS'nin NATS JetStream
   consumer'ı (`pos/ws.Hub.Register`) ikisi de `cmd/api`'nin fx lifecycle
   hook'ları içinde çalışır — `cmd/worker` yalnızca asynq job'ları içindir,
   şu an outbox/KDS ile ilgisi yok. Bu, senaryo yazımından önce elle
   doğrulandı (bkz. rapor).

> **Migration notu (bilinen ortam sorunu, bu sprint'te keşfedildi):**
> `backend/.env`'deki `DATABASE_URL` `app_runtime` kimlik bilgilerini taşır
> (uygulamanın çalışması için doğru — ADR-SEC-002). Ama `cmd/migrate` da
> aynı `DATABASE_URL`'i okuyor ve DDL/BYPASSRLS gerektiriyor
> (`app_migrator` rolü). Migration'ları uygulamak için `DATABASE_URL`'i
> geçici olarak `app_migrator` DSN'iyle **ve** `pgx5://` şemasıyla (not:
> `.env.example`'daki `postgres://` şeması `cmd/migrate`'in kayıtlı
> pgx5 driver'ıyla eşleşmiyor, "unknown driver postgres" hatası verir)
> override etmek gerekti:
> ```
> DATABASE_URL="pgx5://app_migrator:migrator_dev_password@localhost:5442/onlinemenu_dev?sslmode=disable" \
>   go run ./cmd/migrate up
> ```
> Bu, k6 senaryolarının bir parçası değil — backend ekibine ayrıca
> raporlanan bir onboarding/DX bulgusu (bkz. final rapor).

## Çalıştırma

```bash
task backend:loadtest:seed          # bir kere (idempotent, tekrar koşulabilir)
task backend:loadtest:smoke         # 25 VU, 2dk — script doğrulama
task backend:loadtest:full          # 500 VU'ya ramping, ~17dk — bkz. "Ortam önerisi"
```

Doğrudan k6 ile (Taskfile dışı, hata ayıklama için):

```bash
cd backend/loadtest/k6
k6 run mixed_load.js                          # PROFILE=smoke varsayılan
k6 run -e PROFILE=full mixed_load.js
k6 run pos_sale_flow.js                       # yalnız kasiyer akışı
k6 run kds_ws.js                              # yalnız KDS WS (tek başına anlamlı değil — bkz. aşağı)
```

`kds_ws.js` **tek başına** çalıştırıldığında sistemde sipariş üreten başka
bir kaynak yoksa `kds_event_latency_ms` metriği boş kalır (yalnızca snapshot
alınır, `order.placed` olayı hiç gelmez). Gerçek gecikme ölçümü için
`mixed_load.js` kullanın — kasiyer senaryosu sipariş üretirken KDS VU'ları
aynı anda dinler.

## Ortam değişkenleri

| Değişken | Varsayılan | Açıklama |
|---|---|---|
| `BASE_URL` | `http://localhost:8081` | API adresi (`.env`'deki `HTTP_ADDR`'a göre) |
| `BRANCH_ID` | `bbbbbbbb-0000-0000-0000-000000000001` | dev-seed'in şubesi |
| `ADMIN_EMAIL` | `admin@onlinemenu.tr` | katalog bootstrap için (wildcard yetkili) |
| `CASHIER_COUNT` | `50` | token havuzu boyutu — `loadtest/seed.sql`'deki kasiyer sayısıyla **birlikte** değiştirin |
| `KDS_COUNT` | `2` | mixed_load.js'te eşzamanlı KDS WS bağlantısı sayısı |
| `PROFILE` | `smoke` | `smoke` (25 VU/2dk) veya `full` (0→500 VU 5dk, 10dk sabit, 2dk iniş) |

## Senaryo tasarımı

### Kasiyer akışı (`pos_sale_flow.js` / `mixed_load.js:cashierFlow`)

Bir VU = bir kasiyer terminali; her iterasyon:

1. `GET /api/v1/catalog/products` (okuma — menüye bakış)
2. `POST /api/v1/pos/checks` — `table_id` göndermeden yalnızca `table_label`
   (`LT-<vuID>-<iterNo>` ya da paket için `PAKET-...`) — masa entity'si
   olmadığından çakışma/409 riski yok, gerçek dünyada da her terminalin
   kendi masası/paket etiketi var.
3. 1-3 sipariş (`POST /api/v1/pos/orders`), her biri 1-3 kalem, miktar 1-3 —
   sipariş toplamları biriktirilir (tam tutar ödenecek, aksi halde
   `ErrInsufficientPayment` 409 alınır ve `http_req_failed` bozulur).
4. `POST /api/v1/payments` — biriken tam tutar, nakit/kart karışık.
5. `POST /api/v1/pos/checks/{id}/close`.

Adımlar arası `sleep`: menüye bakış 1-3s, adisyon açtıktan sonra 2-5s,
siparişler arası 3-10s, ödeme öncesi 3-10s, ödeme sonrası 1-3s — "makine
hızında" değil, gerçekçi kasiyer/müşteri tempolu bir zincir.

Her POST'ta benzersiz `Idempotency-Key` (uuidv4, k6 üzerinde elle üretilmiş
— `k6/lib/common.js`, Node `crypto`/harici paket yok).

`order_channel` %75 `dine_in`, %25 `takeaway` (pos domain sabitleriyle
birebir — `posdomain.OrderChannelDineIn`/`OrderChannelTakeaway`).

### KDS WebSocket (`kds_ws.js` / `mixed_load.js:kdsFlow`)

Şube başına `KDS_COUNT` (varsayılan 2) sabit bağlantı, test süresi boyunca
açık kalır. Ölçülen:

- `ws_connecting` (k6 yerleşik) — handshake süresi.
- `kds_event_latency_ms` (custom Trend) — `order.placed` mesajının
  `occurred_at` alanı (NATS mesaj zaman damgası) ile k6'nın mesajı aldığı an
  arasındaki fark. Aynı host saatini kullandığından (localde client+server
  aynı makine) çapraz-VU korelasyonuna gerek yok; NATS→outbox→WS yayılma
  gecikmesini temiz biçimde izole eder. Snapshot satırları (`seq=0`) dahil
  değil.
- `kds_snapshot_orders_total` (custom Counter) — bağlantı başına gelen
  snapshot boyutu, bilgi amaçlı.

## Threshold'lar ve gerekçeleri

Delta-v2/ADR dokümanlarında POS hot path için sayısal bir p95 hedefi
**tanımlanmamış** (kontrol edildi: `delta-v2.md`, `docs/ROADMAP.md`, ilgili
ADR'ler). Bu yüzden aşağıdaki değerler bu sprint için önerilen bir başlangıç
bütçesidir, ADR olarak resmileştirilmesi ayrı bir karar:

| Metrik | Eşik | Gerekçe |
|---|---|---|
| `http_req_failed` | `< %1` | 500 VU altında ara sıra ağ/timeout toleransı, ama sistemsel hata sınıfını maskelemeyecek kadar sıkı |
| `http_req_duration{type:read}` (p95) | `< 200ms` | Katalog/liste okuma — kasiyer ekranının "responsive" hissettirmesi için tipik POS UX beklentisi |
| `http_req_duration{type:write}` (p95) | `< 500ms` | check/order/payment — RLS + idempotency middleware + (mock) fiscal adapter zincirini içeren tek satırlık DB yazması için üst sınır |
| `kds_event_latency_ms` (p95) | `< 3000ms` | Outbox dispatcher `poll_interval=2s` ile sınırlı bir mimari gecikme tabanı var (bkz. rapor) — bu sınır o tabanı + NATS + WS yazma payını kapsayacak şekilde seçildi |

Her istek `{ tags: { type: 'read' | 'write' } }` ile etiketlenir; threshold
anahtarları bu etikete göre filtrelenir (`http_req_duration{type:read}` gibi)
— aksi halde k6 threshold'u tüm isteklerin toplamına uygular ve okuma/yazma
ayrımı hiçbir şey ifade etmez.

## Gerçek smoke koşusu — sonuçlar (2026-07-05, lokal Mac, core profil)

Ortam: Docker Desktop (core profile: postgres, redis, nats, vault),
`cmd/api` `go run ./cmd/api` ile (air değil — bu ortamda kurulu değildi),
k6 client + API aynı makinede (ağ gecikmesi ~0, bkz. "Bilinen sınırlar").

### `mixed_load.js` (PROFILE=smoke, 25 kasiyer VU + 2 KDS VU, 2dk) — asıl kanıt

```
✓ http_req_duration{type:read}:  p(95)=29.73ms   (eşik: <200ms)
✓ http_req_duration{type:write}: p(95)=37.9ms    (eşik: <500ms)
✓ http_req_failed:               rate=0.00%      (eşik: <1%)
✓ kds_event_latency_ms:          p(95)=80ms      (eşik: <3000ms)

checks_total: 761, %100 başarı, 0 hata
http_reqs: 810 (5.38/s)
iterations: 130 tam satış döngüsü (check aç→sipariş→öde→kapat)
kds_snapshot_orders_total: 484
ws_sessions: 2, ws_msgs_received: 458, ws_connecting p(95)=10.37ms
```

Tüm threshold'lar geçti, hiçbir 4xx/5xx yok. `kds_event_latency_ms` avg
33.6ms / max 127ms — düşük trafikte outbox dispatcher'ın 2 saniyelik
`poll_interval`inin altında kalan çok rahat bir sonuç (bkz. aşağıda
"gözlenen darboğaz" — bu rahatlık 500 VU'da garanti değil).

### `pos_sale_flow.js` tek başına (PROFILE=smoke, 25 VU, 2dk)

```
✓ tüm threshold'lar geçti — http_req_duration p(95)=38.39ms, http_req_failed=0.00%
checks_total: 744, %100 başarı
iterations: 125 tam satış döngüsü
```

## Gözlenen darboğazlar / bulgular (ölçüldü, düzeltilmedi — kapsam dışı)

1. **`billing_outbox` tablosu yok, dispatcher her 2 saniyede bir ERROR
   logluyor.** `platform/outbox/dispatcher.go`'daki `tables` listesi
   `billing_outbox`'ı hardcoded içeriyor, ama `billing` modülü ne
   `cmd/migrate`'in `moduleOrder`'ında ne de `cmd/api`'nin fx modül
   listesinde var. Sonuç: her dispatcher poll cycle'ında (`poll_interval:
   2s`) `relation "billing_outbox" does not exist` ERROR logu — ~13 dakikalık
   test oturumunda 303 satır. Prod'da bu, billing modülü gelene kadar
   sürekli log gürültüsü ve ERROR-oranlı alerting varsa yanlış alarm demek.
   Öneri: `billing` outbox tablosu migration'la gelene kadar `tables`
   listesinden çıkarılsın ya da dispatcher, "tablo yok" hatasını (özellikle)
   sessizce/WARN ile atlayıp bir dahaki pollde tekrar denesin.
2. **KDS gecikmesinin mimari tabanı: outbox `poll_interval=2s`.**
   `order.placed` → outbox satırı → dispatcher (2s polling) → NATS → WS hub
   → client zinciri, en kötü durumda ~2 saniyeye kadar gecikme ekleyebilir
   (dispatch tam poll aralığının başında kaçırılırsa). Bu smoke koşusunda
   düşük trafik altında ortalama 33ms/p95 80ms gözlendi — poll cycle'ın çoğu
   zaman "boşta" yakalandığı anlamına gelir. **500 VU tam koşuda bu metrik
   yeniden ölçülmeli**: outbox tablosunda satır birikimi artarsa (batch_size:
   100/cycle) p95 gecikme 2 saniyeye yaklaşabilir. KDS için gerçek zamanlı
   his gerekiyorsa (ki mutfak ekranı tam olarak budur), LISTEN/NOTIFY
   tetiklemeli bir "hemen dispatch" yolu + polling'i fallback olarak tutmak
   değerlendirilebilir.
3. **Migration DSN wiring'i kafa karıştırıcı** (yukarıda "Ön koşullar"da
   detaylandırıldı): `.env`'nin `DATABASE_URL`'i `app_runtime` (doğru,
   runtime için), ama `cmd/migrate` de aynı değişkeni okuyup DDL istiyor —
   `app_migrator` gerekiyor. Ayrıca `.env.example`'daki `postgres://` şeması
   `cmd/migrate`'in kayıtlı `pgx5` driver'ıyla uyuşmuyor (`unknown driver
   postgres` hatası). Yeni bir geliştirici environment'ı ilk kurduğunda bu
   iki nokta migration'ı hiç açıklama olmadan başarısız kılar.
4. **500 VU'nun tamamı bu sprint'te lokalde koşulmadı** (bkz. "Ortam
   önerisi") — smoke koşusunun düşük eşiklerle rahat geçmesi, sistemin 20x
   yükte de aynı marjinle geçeceği anlamına gelmez; bkz. madde 2 (outbox
   dispatch batching) ve aşağıdaki resource sınırları.

## Bilinen sınırlar

- **Tek terminal havuzu, tek şube.** `loadtest/seed.sql` 50 kasiyer
  oluşturur, hepsi aynı tenant + aynı şubeye bağlı. Gerçek 500-terminal bir
  zincirde onlarca şube ve rol çeşitliliği (kasiyer/vardiya müdürü/mutfak)
  olur; bu test yalnızca "N terminal, çoklu vardiya" modelini simüle eder,
  çoklu tenant/şube RLS partition etkisini ölçmez.
- **Client + server aynı makinede.** Gerçek POS tabletlerinin WiFi/4G ağ
  gecikmesi, TLS handshake maliyeti bu ölçümlerde yok — ölçülen p95'ler
  saf backend/DB/Redis/NATS işlem süresidir, uçtan uca kullanıcı deneyimi
  değil.
- **Katalog sabit, ürün sayısı az.** `ensureCatalog` yalnızca DB boşsa 6
  ürünlük bir menü oluşturur; büyük bir menüde (yüzlerce ürün) katalog
  okuma/index davranışı farklı olabilir — bu test menü boyutunu
  ölçmüyor.
- **KDS latency ölçümü tek makine saatine dayanıyor.** Prod'da API/WS ve
  client farklı makinelerde olduğundan clock skew devreye girer; bu smoke
  koşusunda client=server olduğundan skew sıfır — üretim ortamı ölçümünde
  NTP senkronizasyonu doğrulanmalı.
- **`air` bu ortamda kurulu değildi** — smoke koşusu `go run ./cmd/api` ile
  yapıldı (aynı binary, yalnızca hot-reload yok). Gerçek dev akışında
  `task backend:dev` kullanılmalı; performans farkı beklenmez (aynı derlenmiş
  kod çalışıyor).
- **Rate limiting henüz implemente değil** (ADR-OPS-003 hâlâ "Taslak" —
  implementasyon detayları dolu değil). Bu yüzden smoke koşusunda 429
  gözlenmedi; implementasyon geldiğinde threshold'lar ve senaryo pacing'i
  (özellikle `device: 120 burst/dk` limiti) yeniden gözden geçirilmeli.

## 500-VU tam koşu için ortam önerisi

Bu sprint'te `PROFILE=full` **lokalde tam parametreleriyle koşulmadı** —
görev tanımı da bunu net biçimde smoke ile sınırlamıştı ("full profili
parametrik kalsın"). Tam koşu için:

1. **Ayrı bir yük üretici host.** k6'yı API ile aynı makinede çalıştırmayın
   — 500 VU'nun HTTP/WS client yükü (goroutine, socket, JSON encode/decode)
   API/Postgres'le CPU/ağ için yarışır ve ölçümü kirletir. k6 Cloud, ayrı bir
   EC2/VM, ya da en azından ayrı bir Docker Desktop VM'i önerilir.
2. **Postgres/Redis/NATS için ayrılmış kaynak.** Dev compose'daki
   `postgres` servisi `MaxConns` sınırı yok gibi görünüyor ama `app_runtime`
   havuzu `MaxConns: 20` (bkz. `cmd/api/main.go: newDBConfig`) — 500 VU'nun
   art arda check/order/payment yazması bu 20 bağlantıyı hızla doyurabilir.
   Tam koşudan önce `MaxConns`'u ölçüm amaçlı artırıp
   (`DATABASE_URL`/`newDBConfig` şu an env'den değil sabit kodlanmış — bu da
   ayrı bir bulgu: ölçüm için geçici bir derleme-zamanı değişikliği ya da
   config'e taşıma gerekebilir) sonucu karşılaştırmak faydalı olur.
3. **Outbox dispatcher batch/poll ayarlarını izleyin** (bkz. "Gözlenen
   darboğazlar" madde 2) — `poll_interval`, `batch_size` metriklerini
   (varsa Prometheus'a) bağlayıp tam koşu sırasında outbox tablo
   derinliğini (`SELECT count(*) FROM pos_outbox WHERE dispatched_at IS
   NULL`) izleyin.
4. **Token TTL.** `CTX_TOKEN_SECRET` ile imzalanan dev-login token'ları bu
   ortamda ~8 saat TTL'e sahip görünüyor — 17 dakikalık tam koşu için sorun
   değil, ama daha uzun soak testlerinde token yenileme (401 → re-login)
   akışı senaryoya eklenmeli.
5. **Gözlemlenebilirlik profili açık koşun** (`task compose:up` — yalnız
   `core` değil) ve Grafana/Tempo üzerinden gerçek p95'leri, DB connection
   pool doygunluğunu ve NATS consumer lag'ini k6'nın kendi çıktısıyla
   çapraz doğrulayın.
