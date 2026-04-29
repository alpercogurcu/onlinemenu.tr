# Online Menu — Mimari Delta Paketi v2

> **Bu doküman nedir?**
> Mevcut `docs/` altındaki dört baseline dosyasının (roadmap, architecture, schema, offline-sync) üzerine uygulanacak mimari kararları ve ADR'ları içerir.
>
> **Nasıl okunur?**
> 1. **Bölüm 1** — Güncellenmiş delta direktifleri (v2). Hangi baseline değişiklikleri, hangi önceliklerle yapılacak.
> 2. **Bölüm 2** — ADR Paketi. 17 mimari karar kaydı, kategori kodlu.
> 3. **Bölüm 3** — AI implementasyon ajanı için çalışma kuralları.
>
> **AI implementasyon ajanı için:**
> Bu dosya baseline dokümanları **değiştirmez**, üzerine **delta** ekler. Baseline'daki spesifik cümleler dışında dokunulmaz.
> Her PR tek bir delta'yı uygular ve ilgili ADR'ı referans verir. Sıra: P0 → P1 → P2.
>
> **Versiyonlama notu:** Bu v2'dir. v1'den farklar: (a) Fiscal adapter P1'den P0'a yükseltildi, (b) 4 açık soru operatör tarafından karara bağlandı: tek Keycloak realm, asynq/Temporal sınırları, Typesense, Unleash.

---

## İçindekiler

**Bölüm 1 — Güncellenmiş Delta Direktifleri**
- 7 × P0 (Faz 0'da bitmek zorunda)
- 4 × P1 (Faz 0-1 penceresi)
- 5 × P2 (Faz 1-2 penceresi)

**Bölüm 2 — ADR Paketi**
- 17 ADR, 6 kategori
- Güvenlik (SEC), Yetkilendirme (AUTH), Veri/Event (DATA), Mimari (ARCH), Mali (FISCAL), Operasyonel (OPS)

**Bölüm 3 — AI İçin Uygulama Kuralları**
- PR akışı
- Çelişki çözümü
- ADR güncelleme disiplini

---

# BÖLÜM 1 — GÜNCELLENMİŞ DELTA DİREKTİFLERİ

## Öncelik Sınıflaması

| Kod | Anlam | Açıklama |
|---|---|---|
| 🔴 **P0** | Faz 0'da bitmek zorunda | Baseline'daki güvenlik/doğruluk hatasını düzeltir veya tasarımsal olarak şimdi oturmazsa geri dönüşü pahalı. |
| 🟡 **P1** | Faz 0-1 arası | Platform katmanına oturur, modül implementasyonlarından önce hazır olmalı. |
| 🟢 **P2** | Faz 1-2 penceresi | Önemli ama modüller iskelet haline gelirken paralel yürür. ADR şimdi, uygulama sonra. |

## v1'den v2'ye Ana Değişiklikler

1. **P0-7 eklendi:** Fiscal/ÖKC adapter interface P0'a yükseltildi. Gerekçe: Payment modülü fiscal interface'e bağımlı yazılmazsa Faz 2'de yeniden yazım gerekir. Interface + mock + event şemaları P0, gerçek cihaz SDK'ları Faz 2'de kalır.
2. **Keycloak tek realm kararı kesinleşti.** → ADR-AUTH-002
3. **asynq vs Temporal sınırları netleşti.** → ADR-ARCH-002
4. **Search backend: Typesense.** → ADR-ARCH-003
5. **Operasyonel feature flag: Unleash.** → ADR-ARCH-001
6. **OPA decision sözleşmesi daraltıldı:** OPA yalnızca `Allow` + `Scope` döner; permission listesi/field görünürlüğü projection katmanında çözülür. → ADR-AUTH-001

## Delta Özet Tablosu

| # | Delta | Öncelik | İlgili ADR | Baseline Durumu |
|---|---|---|---|---|
| P0-1 | pgBouncer + SET LOCAL düzeltme | 🔴 | SEC-001 | Hatalı bilgi mevcut, düzeltilmeli |
| P0-2 | RLS FORCE + ayrı rol | 🔴 | SEC-002 | Eksik |
| P0-3 | Dört katmanlı authz | 🔴 | AUTH-001 | Belirsiz |
| P0-4 | Idempotency altyapısı | 🔴 | SEC-003 | Yok |
| P0-5 | Outbox dispatcher spec | 🔴 | DATA-001 | Yüzeysel |
| P0-6 | Event immutability | 🔴 | DATA-002 | İma edilmiş, kural değil |
| **P0-7** | **Fiscal adapter interface** | 🔴 | **FISCAL-001** | **Yok — v2'de yükseltildi** |
| P1-1 | Cihaz kayıt güvenliği | 🟡 | SEC-004 | Güvenlik açığı var |
| P1-2 | Feature flag iki katman | 🟡 | ARCH-001 | Tek katman, eksik |
| P1-3 | Timezone/business day | 🟡 | DATA-003 | Yüzeysel |
| P1-4 | Yol haritası düzeltmeleri | 🟡 | — | Ufak tutarsızlıklar |
| P2-1 | Backup/DR planı | 🟢 | OPS-001 | Yok |
| P2-2 | Tenant offboarding | 🟢 | OPS-002 | Yok |
| P2-3 | Rate limiting | 🟢 | OPS-003 | Yok |
| P2-4 | Cost observability | 🟢 | OPS-004 | Yok |
| P2-5 | Catalog delta sync | 🟢 | DATA-004 | Faz 2 için hazırlık |

> **Not:** Ek olarak sabit tasarım kararları için 3 destekleyici ADR var: AUTH-002 (Keycloak), ARCH-002 (asynq/Temporal), ARCH-003 (search backend).

## Kapanmış Açık Sorular (v1'de Bekleyen)

| Soru | Karar | ADR |
|---|---|---|
| Keycloak realm stratejisi | Tek realm + `tenant_id` claim + grup hiyerarşisi | AUTH-002 |
| asynq vs Temporal çakışması | Net sınır: fire-and-forget < 5dk → asynq; stateful uzun süreli → Temporal | ARCH-002 |
| Search backend | Typesense (collection-per-tenant) | ARCH-003 |
| Operasyonel feature flag | Unleash (Faz 2'de aktif) | ARCH-001 |

## Hâlâ Açık (Faz 1 Sprint Kararları)

- Tenant-specific OPA policy override: Faz 2+ değerlendirilecek, şimdilik **yok**.
- Kubernetes geçişi: Faz 2 başı.
- Mobil (Flutter): Faz 4.

---

# BÖLÜM 2 — ADR PAKETİ

## ADR Okuma Kılavuzu

Her ADR şu yapıda:

- **Durum:** Kabul Edildi | Önerildi | Taslak (skeleton) | Reddedildi | Değiştirildi
- **Bağlam:** Bu kararı neden vermek zorunda kaldık?
- **Karar:** Ne yapmaya karar verdik?
- **Sonuçlar:** Bu kararın iyi/kötü/riskli sonuçları
- **Değerlendirilen Alternatifler:** Neyi reddettik, neden?

**Durum tanımları:**
- **Kabul Edildi** — Tam ADR, karar kesinleşmiş, uygulamaya geçilebilir.
- **Taslak** — İskelet ADR. Karar özü yazılı ama ayrıntılar implementasyon sırasında dolacak. Uygulamadan önce "Kabul Edildi"ye çevrilmeli.

## ADR İndeksi

### Güvenlik (SEC)
- **SEC-001** — RLS İçin Transaction-Scoped Tenant İzolasyonu (P0-1) ✅
- **SEC-002** — RLS FORCE ve Ayrı Runtime Rolü (P0-2) ✅
- **SEC-003** — Idempotency-Key Altyapısı (P0-4) ✅
- **SEC-004** — Cihaz Kayıt ve Pairing Code Akışı (P1-1) 📝

### Yetkilendirme (AUTH)
- **AUTH-001** — Dört Katmanlı Authorization Mimarisi (P0-3) ✅
- **AUTH-002** — Keycloak Tek Realm Stratejisi ✅

### Veri / Event (DATA)
- **DATA-001** — Outbox Dispatcher Mimarisi (P0-5) ✅
- **DATA-002** — Event Immutability Kuralı (P0-6) ✅
- **DATA-003** — Timezone ve Business Day Hesaplama (P1-3) 📝
- **DATA-004** — Catalog Delta Sync (P2-5) 📝

### Mimari (ARCH)
- **ARCH-001** — İki Katmanlı Feature Flag (P1-2) 📝
- **ARCH-002** — asynq ve Temporal Sorumluluk Ayrımı ✅
- **ARCH-003** — Arama Backend Seçimi (Typesense) ✅

### Mali (FISCAL)
- **FISCAL-001** — Fiscal Device Adapter Interface (P0-7) ✅

### Operasyonel (OPS)
- **OPS-001** — Backup ve Disaster Recovery (P2-1) 📝
- **OPS-002** — Tenant Offboarding ve KVKK Uyumu (P2-2) 📝
- **OPS-003** — Rate Limiting Stratejisi (P2-3) 📝
- **OPS-004** — Cost Observability (P2-4) 📝

**Sembol anlamı:** ✅ Kabul Edildi (tam) · 📝 Taslak (iskelet)

---

## Kategori: SEC (Güvenlik)

### ADR-SEC-001: RLS İçin Transaction-Scoped Tenant İzolasyonu

**Durum:** Kabul Edildi
**Tarih:** 2026-04-19
**İlgili delta:** P0-1

#### Bağlam

Multi-tenant POS platformunda shared schema + PostgreSQL Row-Level Security (RLS) kullanıyoruz. RLS policy'leri hangi tenant'a ait satırları göstereceğine karar vermek için bir session variable okuyacak. Bu variable'ı her request için doğru şekilde set etmek ve request bittiğinde temizlemek kritik — aksi halde bir request'in tenant context'i başka request'e sızar ve tüm izolasyon çöker.

Baseline dokümanda (`docs/architecture.md`) şu ifade bulunuyor:

> "Transaction mode (default) çalışmaz — `SET LOCAL` transaction kapanınca sıfırlanır."

Bu ifade **teknik olarak hatalıdır** ve AI implementasyon kararlarını yanlış yönlendirir. `SET LOCAL` tam olarak transaction scope'a bağlıdır — sıfırlanması istenen davranıştır, bug değildir. pgBouncer transaction mode ile `SET LOCAL` mükemmel uyumludur.

#### Karar

1. **Her HTTP request bir transaction içinde çalışır.** Transaction başında `SET LOCAL app.tenant_id = '<uuid>'` çalıştırılır.
2. **RLS policy'leri `current_setting('app.tenant_id', false)::uuid`** ile tenant'ı okur. İkinci argüman `false` olduğu için variable set edilmemişse PostgreSQL exception fırlatır — sessiz sızıntı yerine gürültülü hata.
3. **pgBouncer transaction mode** kullanılır (Faz 2+). Faz 0-1'de pgBouncer yok, pgxpool doğrudan.
4. **pgx exec mode:** Transaction mode ile uyumluluk için `pgxpool.Config.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeExec` kullanılır. Bu değişiklik sqlc üretilen kodu etkilemez.
5. **Platform helper:** `internal/platform/db/tenant_tx.go` içinde tek izinli desen:
   ```go
   func (p *Pool) WithTenantTx(ctx context.Context, tenantID uuid.UUID, fn func(tx pgx.Tx) error) error
   ```
6. **Yasak:** `SET` komutu (LOCAL olmadan) uygulama kodunda kullanılmaz — session leak riski. golangci-lint custom rule veya `depguard` ile bu pattern yasaklanır.
7. **Yasak:** `pool.Query`, `pool.Exec` gibi doğrudan çağrılar modül kodunda kullanılmaz — yalnızca `WithTenantTx` üzerinden. Lint ile zorlanır.

#### Sonuçlar

**İyi:**
- Transaction bittiğinde tenant context otomatik temizlenir; cross-request sızıntı yapısal olarak imkânsız.
- pgBouncer transaction mode kullanılabilir; connection pooling verimi korunur.
- RLS variable set edilmeden query çalışırsa PostgreSQL exception atar; bug erken görünür, saklanmaz.
- Her DB çağrısı zaten transaction'da olduğu için ek runtime maliyet minimum.

**Kötü / Dikkat:**
- Geliştiricinin (veya AI'ın) `WithTenantTx` dışında DB çağrısı yapma olasılığı var. Lint + code review + RLS sızıntı testi bunu yakalar.
- pgx exec mode değişikliği bazı advanced pgx feature'larını (statement cache optimization) devre dışı bırakır. Pratik performansa etkisi ihmal edilebilir.

**Risk:**
- `SET LOCAL` değil `SET` kullanımı yasak — birisi kopyalayıp gelirse sessiz sızıntı olur. Lint kuralı ve PR review şart.

#### Değerlendirilen Alternatifler

- **pgBouncer session mode:** Reddedildi. Connection pooling faydası büyük ölçüde kaybolur; ölçeklenebilirlik sınırı erken gelir.
- **Database-per-tenant:** Reddedildi. Binlerce tenant'ta operasyonel cehennem: migration yönetimi, backup, connection limit.
- **Schema-per-tenant:** Reddedildi. Migration'ı her schema'da tekrar çalıştırmak gerekir, PostgreSQL'in `search_path` davranışı karmaşıklaşır.
- **Uygulama katmanında filter (RLS yok):** Reddedildi. Tek unutulan `WHERE tenant_id = ?` = tenant sızıntısı. RLS "defense in depth" sağlar.

---

### ADR-SEC-002: RLS FORCE ve Ayrı Runtime Rolü

**Durum:** Kabul Edildi
**Tarih:** 2026-04-19
**İlgili delta:** P0-2

#### Bağlam

Baseline'daki RLS örneği yalnızca `ENABLE ROW LEVEL SECURITY` kullanıyor. Bu **yetersizdir**: PostgreSQL'de tablo sahibi (owner) RLS'yi default olarak **bypass eder**. Eğer uygulama bağlantısı migration'ı çalıştıran rol ile aynıysa, RLS sessizce etkisiz kalır ve tüm tenant izolasyonu illüzyondan ibaret olur.

Ayrıca baseline'da RLS policy'sinin `WITH CHECK` kısmı her yerde tutarlı yazılmamış; INSERT'te yanlış tenant_id ile satır yazılabilmesi riski var.

#### Karar

1. **İki ayrı PostgreSQL rolü:**
   - `app_migrator` — migration koşar, tablo yaratır, policy tanımlar. Tablo sahibi (`OWNER`) bu rol olur.
   - `app_runtime` — uygulama RUNTIME bağlantıları yalnızca bu rolle yapılır. Tablo sahibi değildir, RLS **zorunludur**.
2. **Her RLS tablosunda FORCE:**
   ```sql
   ALTER TABLE <table> ENABLE ROW LEVEL SECURITY;
   ALTER TABLE <table> FORCE ROW LEVEL SECURITY;
   ```
   `FORCE` sayesinde tablo sahibi bile RLS'yi atlayamaz. Operatör hataları bile izolasyonu kıramaz.
3. **Policy şablonu:**
   ```sql
   CREATE POLICY tenant_read ON <table>
       FOR SELECT USING (tenant_id = current_setting('app.tenant_id', false)::uuid);
   
   CREATE POLICY tenant_write ON <table>
       FOR ALL USING (tenant_id = current_setting('app.tenant_id', false)::uuid)
                 WITH CHECK (tenant_id = current_setting('app.tenant_id', false)::uuid);
   ```
   Hem okuma (`USING`) hem yazma (`WITH CHECK`) aynı tenant_id'ye pinlenir.
4. **Migration şablonu zorunluluğu:** Yeni tenant-scoped tablo oluşturan her migration `_rls_enable` fragment'ı içermek zorunda. CI lint (bkz: `scripts/lint_rls.sh`) `information_schema` + `pg_catalog` sorgularıyla hangi tabloda RLS ve FORCE ayarlı olduğunu kontrol eder; eksik varsa CI fail.
5. **Runtime bağlantı konfigürasyonu:** `DATABASE_URL` env variable'ı `app_runtime` rolünü kullanır. `MIGRATION_DATABASE_URL` (ayrı) `app_migrator` rolünü kullanır. Migration CLI hariç hiçbir binary migrator rolüyle bağlanmaz.

#### Sonuçlar

**İyi:**
- Operatör hatası veya owner ayrıcalığı ile RLS bypass edilmesi yapısal olarak imkânsız.
- INSERT/UPDATE ile yanlış tenant_id'ye veri yazma policy engellemesiyle bloke edilir.
- Migration rolü minimum ayrıcalıkla sınırlı, runtime rolü minimum ayrıcalıkla sınırlı — least privilege.

**Kötü / Dikkat:**
- İki rol yönetmek operasyonel küçük yük (credential rotation iki kat). Vault bunu otomatikleştirir.
- Migration'ı runtime ile koşma cazibesi var (dev ortamında), ama bu alışkanlığa dönüşürse prod'da kaza olur. Docker Compose'da bile iki ayrı rol tanımlanmalı.

**Risk:**
- Yeni eklenen tablolarda RLS unutulursa sızıntı. CI lint bunun karşısında tek savunma. Lint test coverage'ını ihmal etme.

#### Değerlendirilen Alternatifler

- **Tek rol + güvenim var mantığı:** Reddedildi. "Migration'da owner ile gelir, sonra runtime'da dikkat ederiz" yaklaşımı human error'a açık.
- **RLS yerine application-layer filter:** Reddedildi (SEC-001'deki gerekçeler).
- **SECURITY DEFINER fonksiyonlar:** Reddedildi. Her DB erişimini fonksiyon üzerinden yapmak sqlc + pgx avantajlarını kaybetmeye neden olur.

---

### ADR-SEC-003: Idempotency-Key Altyapısı

**Durum:** Kabul Edildi
**Tarih:** 2026-04-19
**İlgili delta:** P0-4

#### Bağlam

POS + Payment akışında ağ kesintisi + kullanıcı tekrar tıklama = çift ödeme, çift adisyon, çift fatura. Kasiyer "ödeme al" butonuna basar, terminal onayı gelir, ama cloud yanıtı dönerken network kesilir; kasiyer tekrar basar. Sonuç: müşteri iki kez ödeme kaydı, bir kez para ödedi. Bu tür bug'lar şirket öldürür.

Idempotency, HTTP katmanında bir konvansiyondur (Stripe, Shopify standardı): client her yazma isteğinde unique bir `Idempotency-Key` gönderir. Aynı key ile gelen ikinci istek, ilk isteğin cached cevabını döner — handler'a ulaşmaz.

Baseline'da bu yok. Her modülün kendi ad-hoc çözümünü yazması anti-pattern; platform katmanında olmalı.

#### Karar

1. **Platform middleware:** `internal/platform/httpx/idempotency.go` içinde HTTP middleware olarak.
2. **Header:** `Idempotency-Key: <uuid-v4>`. Client tarafında üretilir, retry'da aynı kalır.
3. **Zorunluluk matrisi:**
   - **Zorunlu** (eksikse 400): `POST /v1/payments/*`, `POST /v1/invoices/*`, `POST /v1/checks/{id}/close`, `POST /v1/orders`
   - **Opsiyonel ama önerilir:** Diğer POST/PUT/PATCH endpoint'leri
   - **Uygulanmaz:** GET, DELETE, HEAD
4. **Storage:** Redis. Key: `idempotency:{tenant_id}:{idempotency_key}`. TTL 24 saat.
5. **Saklanan değer:**
   - Request payload'unun SHA-256 hash'i (path + body + tenant_id)
   - Response (HTTP status + body + content-type)
6. **Çakışma davranışı:**
   - İlk istek: handler çalışır, response Redis'e yazılır, 24 saat cache.
   - Aynı key + aynı hash: cached response döner, handler **çağrılmaz**.
   - Aynı key + farklı hash: 422 Unprocessable Entity + "idempotency key reused with different payload" — client bug.
7. **Race condition:** Redis'te `SET NX` ile "işleniyor" marker'ı konur; paralel ikinci istek 409 Conflict + `Retry-After: 2` döner. İlk istek tamamlanınca marker response ile değiştirilir.
8. **Response snapshot'ı:** Handler'ın tüm yan etkileri (DB, NATS publish) tamamlandıktan **sonra** cache'lenir. Handler hata verirse cache yazılmaz; client retry edebilir.

#### Sonuçlar

**İyi:**
- Ağ kesintisi + retry kombinasyonu artık güvenli; "çift ödeme" sınıfı bug'lar yapısal olarak önlenir.
- Single middleware, tek platform mekanizması; modüller kendi idempotency çözümünü yazmaz.
- Client tarafı kolay: UUID üret, retry'larda aynı UUID'yi kullan.

**Kötü / Dikkat:**
- 24 saat cache = Redis'te storage büyür. 1M transaction/gün × 2 KB = ~2 GB/gün. TTL + Redis eviction bunu yönetir ama boyutlama hesaba katılmalı.
- Handler uzun sürerse ikinci istek 409 + retry döngüsüne girer. Uzun işlemler Temporal'a taşınmalı; idempotency middleware kısa handler için.

**Risk:**
- Client UUID yerine sabit string gönderirse (bug) — aynı key farklı içerikle → 422. Bu iyi, ama client dev bunu fark etmezse UX bozulur. API dokümantasyonunda UUID-per-attempt kuralı net yazılmalı.
- Redis down → idempotency middleware fail-open mu, fail-close mu? **Karar: fail-close.** Redis yoksa yazma endpoint'leri 503 döner. Idempotency'den ödün verilmez.

#### Değerlendirilen Alternatifler

- **DB'de idempotency tablosu (Postgres):** Reddedildi Faz 0-1 için. Her yazma endpoint'inde ek DB roundtrip + DB yükü artışı. Faz 2'de denetim kaydı gerekliliği doğarsa ek tablo eklenebilir (Redis + DB birlikte).
- **Handler'ın kendisinin idempotent olması (conditional insert, ON CONFLICT):** Reddedildi tek başına. Bazı işlemler (ör: harici API çağrısı + DB write) kendi içinde idempotent yapılamaz; middleware gerekli.
- **Client UUID yerine request body hash key olsun:** Reddedildi. Aynı body = aynı key = retry fark edilemez. Client intention (retry mi, yeni istek mi) net olmalı.

---

### ADR-SEC-004: Cihaz Kayıt ve Pairing Code Akışı

**Durum:** Taslak
**Tarih:** 2026-04-19
**İlgili delta:** P1-1

#### Bağlam

Baseline akışı:
> Yeni cihaz → Local Server'a bağlanır → Cloud'a cihaz kayıt isteği gönderir → Keycloak'ta client-credentials oluşturur.

Bu akışta **herhangi bir cihaz** kendini bir tenant'a kaydedebilir. POS tabletleri açık restoranlarda durur, çalınabilir. Fingerprint'in kolay sahtelenememe garantisi yok.

#### Karar (Özet)

1. **Pairing code akışı:** Admin panelden (yetkili kullanıcı) "yeni cihaz ekle" → sistem 10 dakika ömürlü, 6-8 karakter alfanumerik code üretir → cihaz ilk açılışta bu code ile cloud'a başvurur → doğrulanırsa Keycloak client-credentials + cihaz token'ı alır.
2. **Hardware fingerprint:** Platform-native güvenlik modülü kullanılır — Windows TPM, macOS Secure Enclave, Android Keystore, Linux machine-id + MAC.
3. **Token rotation:** Access 1 saat, refresh 30 gün + her kullanımda rotate.
4. **Revocation:** Admin panel → Keycloak disable + NATS `device.wipe` komutu → cihaz localStorage/SQLite kritik verilerini temizler.
5. **Şema:** `devices` tablosuna `pairing_code_hash`, `pairing_expires_at`, `fingerprint_method`, `last_token_rotated_at`, `revoked_at`, `revoke_reason` kolonları eklenir.

#### İmplementasyon Detayları (Dolacak)

- Her platform için fingerprint extraction kodunun teknik detayları
- Code formatı (okunabilirlik + güvenlik dengesi — QR code vs alfanumerik)
- Wipe komutunun kapsamı (hangi veri silinir, hangi kalır)
- Lost-stolen senaryosu operasyonel runbook

#### Değerlendirilen Alternatifler (Özet)

- **Otomatik cihaz kayıt (kod yok, ilk bağlantıda kaydet):** Reddedildi — güvenlik açığı.
- **Admin paneli yerine SMS OTP ile cihaz onay:** Değerlendirilecek (Faz 2), ticari yük (SMS maliyeti) fayda-maliyet hesabıyla.

---

## Kategori: AUTH (Yetkilendirme)

### ADR-AUTH-001: Dört Katmanlı Authorization Mimarisi

**Durum:** Kabul Edildi
**Tarih:** 2026-04-19
**İlgili delta:** P0-3

#### Bağlam

Baseline'da OPA var ama "hangi yetki kontrolü hangi katmanda yapılır" belirsiz. Bu belirsizlik iki tehlikeli uca çeker:

1. **Her şey RLS'e:** "Kasiyer kendi satışlarını görür, müdür şubesini görür" mantığı RLS policy'sine yazılır. Sonuç: 5 rol × 10 tablo = 50 karmaşık policy, debug'ı cehennem, değişiklik her seferinde 10 policy güncellemesi.
2. **Her şey handler'a:** Authz check'leri her handler'ın başında ad-hoc. Sonuç: audit'lenemez, tutarsız, bir endpoint'te unutulursa sızıntı.

Ayrıca **field-level görünürlük** (kasiyer `cost_price` görmesin) RLS ile çözülemez (satır bazlı, kolon değil); OPA ile de çözülmemeli (policy şişer).

#### Karar: Dört Katmanlı Model

| Katman | Sorumluluk | Nerede | Neden orada |
|---|---|---|---|
| **1. RLS (DB)** | **Yalnızca `tenant_id` izolasyonu.** Cross-tenant sızıntı son savunma hattı. | PostgreSQL policy | Bug = şirket batar. En katı kontrata kilitli. |
| **2. OPA (Policy)** | **"Bu action'a izin var mı" + Scope** (`ScopeOwn`/`ScopeBranch`/`ScopeTenant`). | Embedded (in-process) | Versiyonlanabilir, test edilebilir, değişkenliği yüksek. Latency sıfır (mikrosaniye). |
| **3. Service (Scope)** | OPA scope'unu query WHERE clause'una çevirir: "kendi satışları", "kendi şubesi". | Go service layer | Debuggable, loggable, unit-testable. Normal kod. |
| **4. DTO Projection** | **Field-level filtreleme.** Kasiyer `cost_price`/`profit` görmez. | Response DTO | RLS/OPA yapamaz; tip güvenli tek yer. |

#### OPA Decision Sözleşmesi

OPA'dan dönen tek yapı:

```go
type Decision struct {
    Allow bool
    Scope Scope // enum: ScopeOwn | ScopeBranch | ScopeTenant
}
```

**OPA permission listesi döndürmez.** Field-level görünürlük, rol → permission mapping'i **service/projection katmanında** (hard-coded tablolar) çözülür. Sebep: Rego policy'lerinin 500 satır + her field için flag taşımak.

#### Örnek Uçtan Uca Akış

Senaryo: Kasiyer "bugünün satışları" ister.

```
1. HTTP: GET /v1/sales?scope=today, Authorization: Bearer <JWT>

2. [Middleware: auth] JWT doğrula → Principal{UserID, TenantID, BranchIDs[], Roles[]} → ctx

3. [Middleware: tenant] BEGIN + SET LOCAL app.tenant_id = principal.tenant_id
   (RLS aktif — cross-tenant sızıntı yapısal olarak imkânsız)

4. [Service] decision := authz.Decide(ctx, "sales.list", principal)
   → Decision{Allow: true, Scope: ScopeOwn}
   (OPA policy kasiyer için "own" döndü)

5. [Service] Query builder + scope uygulanır:
   WHERE cashier_id = principal.UserID
     AND created_at >= business_day_start(branch_tz)
   (sqlc ile execute; RLS zaten tenant filter'ı koyuyor)

6. [DTO] perms := permsForRoles(principal.Roles)
   sales.Map(s => ProjectSale(s, perms))
   (projection: cost_price, profit field'ları kasiyer için düşürülür)

7. JSON response
```

#### Karar Detayları

1. **Domain model rolleri bilmez.** `domain.Sale` struct'ı `CostPrice` ve `Profit`'i **her zaman** tutar. Rol bazlı filtreleme yalnızca DTO projection'da.
2. **Platform katmanı:** `internal/platform/authz/`
   - `authz.Principal` — JWT'den parse
   - `authz.Decider` interface — embedded OPA implementation
   - `authz.Scope` enum (`ScopeOwn | ScopeBranch | ScopeTenant`) — tip güvenli, string değil
   - `authz.PermSet` — projection için; rol listesinden türetilen permission kümesi
3. **OPA embedded mode:** Sidecar değil, in-process. Policy bundle'ları `configs/opa/bundles/`. Faz 2'de bundle server (hot-reload).
4. **Decision cache:** Redis `authz:{user_id}:{action}:{resource_type}`, TTL 60s. Keycloak `user.updated` event'i cache invalidate.
5. **JWT claim şeması:**
   ```json
   {
     "sub": "user-uuid",
     "tenant_id": "tenant-uuid",
     "branch_ids": ["branch-uuid-1", "branch-uuid-2"],
     "roles": ["cashier"]
   }
   ```
   `branch_ids` **array** — DB'deki `branch_users` M:N ilişkisiyle uyumlu.
6. **Projection helper:** `internal/platform/projection/projector.go`
   ```go
   type Projector[D any, V any] interface {
       Project(d D, perms authz.PermSet) V
   }
   ```
7. **Permission tablosu:** `internal/platform/authz/permissions.go`
   ```go
   var rolePerms = map[string]PermSet{
       "cashier":      {"sale.view", "order.create"},
       "manager":      {"sale.view", "sale.view_financials", "staff.manage"},
       "owner":        {"*"},
       "accountant":   {"sale.view", "sale.view_financials", "invoice.view"},
   }
   ```
   Tenant-specific override: Faz 2+ (başlangıçta tek tablo, tüm tenant'lara uyar).
8. **Fail-closed:** OPA cevap vermezse → Deny. `Allow` default değeri `false`.

#### Sonuçlar

**İyi:**
- Her authz sorusu tek bir katmanın sorumluluğu; karışıklık ortadan kalkar.
- OPA policy'leri basit kalır (sadece scope kararı); Rego 50 satırı geçmez.
- Field-level görünürlük tip sistemi ile zorlanır (DTO'da field yok → yazılımsal olarak leak imkânsız).
- Debug kolay: her katman ayrı loglanır, ayrı test edilir.

**Kötü / Dikkat:**
- Dört katmanın her biri ayrı kod + test. Başlangıç maliyeti var.
- Projection katmanını atlama ayartması güçlü ("aynı struct'ı dönsek yeter" → field sızıntısı). Code review disiplini şart.

**Risk:**
- `PermSet` tablosu hard-coded — tenant-specific varyant Faz 2'de gelir. O zamana kadar "müşteri talep ediyor" kararı operatöre iletilmeli, acil implementasyona geçilmemeli.

#### Değerlendirilen Alternatifler

- **Her şey RLS:** Reddedildi. Debug imkânsız, policy patlar, field-level yapamaz.
- **Her şey OPA (permission listesi dahil):** Reddedildi. Rego bilgi kaynağı olarak kötü; policy 500+ satır olur.
- **Casbin:** Reddedildi. OPA daha olgun, bundle distribution ekosistemi var, Rego Cloud Native standardı.
- **Handler-bazlı ad-hoc:** Reddedildi. Audit edilemez, tutarsız.

---

### ADR-AUTH-002: Keycloak Tek Realm Stratejisi

**Durum:** Kabul Edildi
**Tarih:** 2026-04-19
**İlgili delta:** v1'den kapanan açık soru

#### Bağlam

Multi-tenant Keycloak'ta iki ana strateji var:
1. **Realm-per-tenant:** Her tenant kendi realm'ı. Tenant'lar arası izolasyon sert, tenant kendi IDP/SSO'sunu getirebilir.
2. **Tek realm + tenant_id claim:** Tüm tenant'lar tek realm'da. İzolasyon claim + application layer'da.

Türkiye pazarı (zincir/franchise restoran, market) büyük çoğunlukla kendi SSO'sunu getirmeyecek. Google/Apple/phone OTP tipik login metodları. 1000+ tenant ölçeklendiğinde realm-per-tenant Keycloak'ı diz çöktürür.

#### Karar

**Tek realm stratejisi kullanılır.**

1. **Realm:** `onlinemenu` (tek realm, tüm tenant'lar).
2. **Custom claim:** `tenant_id`, `branch_ids[]`, `roles[]` JWT claim olarak mapper ile basılır.
3. **Grup hiyerarşisi:**
   ```
   /tenants/{tenant_id}
           /branches/{branch_id}
                    /roles/{role}   (cashier, manager, ...)
   ```
   Bir kullanıcı birden fazla şubede farklı rol alabilir (zincir yönetici + kendi şubesinde cashier, vb.).
4. **JWT mapper:** Grupları düz array'lere çözer:
   - `tenant_id` = kullanıcının ait olduğu tenant (tek değer)
   - `branch_ids[]` = erişim yetkisi olan tüm şubeler
   - `roles[]` = tüm rollerin birleşimi
5. **Identity provider:** Email/password + Google + Apple + phone OTP (Faz 2). Tenant-specific IDP Faz 3-4.
6. **Admin API kullanımı:** Platform backend, tenant oluşturulduğunda otomatik grup yaratır. Kullanıcı davet akışı Keycloak'ın built-in invite flow'unu kullanır.

#### Sonuçlar

**İyi:**
- Ölçeklenebilir: 10k+ tenant, 100k+ kullanıcı tek realm'da rahat.
- Tek JWKS endpoint, tek token validation middleware.
- Keycloak admin operasyonları hızlı (realm başına cache overhead yok).
- Grup hiyerarşisi değişiklikleri anlık, hot-reload gerektirmez.

**Kötü / Dikkat:**
- Tenant kendi SSO'sunu getiremez (başlangıçta). Enterprise müşteri talep ederse Faz 3-4'te realm-per-tenant veya broker pattern değerlendirilir.
- Kullanıcı email'i global unique — bir kullanıcı birden fazla tenant'ta olmak isterse (franchise değiştiren kasiyer) farklı email gerekir veya multi-tenant user desteklenmeli (Faz 3).

**Risk:**
- Custom claim mapper bozulursa tüm auth bozulur. Bu mapper'lar version kontrollü ve deploy süreci ile yönetilmeli (Keycloak config as code — `keycloak-config-cli` veya Terraform provider).

#### Değerlendirilen Alternatifler

- **Realm-per-tenant:** Reddedildi. Keycloak admin API performansı 100+ realm'da ciddi düşer; cross-realm token validation karmaşık; migration cehennem.
- **Her tenant için ayrı Keycloak instance:** Reddedildi. Operasyonel maliyet × tenant sayısı.
- **Auth0/Clerk gibi SaaS:** Reddedildi. Maliyet (kullanıcı başına ücret), veri egemenliği (KVKK), tenant özelleştirme esnekliği sınırlı.

---

## Kategori: DATA (Veri / Event)

### ADR-DATA-001: Outbox Dispatcher Mimarisi

**Durum:** Kabul Edildi
**Tarih:** 2026-04-19
**İlgili delta:** P0-5

#### Bağlam

Baseline'da outbox pattern var ama dispatcher'ın nasıl çalışacağı belirsiz. "DB'ye yaz + NATS'a publish" dual-write sorununu çözen outbox, yanlış implement edilirse:
- Naif polling (`SELECT WHERE is_synced=false ORDER BY id LIMIT N`) paralel dispatcher'da sıralama bozar.
- Dispatcher crash olursa event'ler stuck kalır veya çift publish olur.
- Retry stratejisi yoksa poison message tüm queue'yu tıkar.

Faz 2'de Debezium + Kafka CDC gelecek, ama o güne kadar doğru çalışan bir dispatcher şart.

#### Karar

**Faz 0-1 dispatcher:** Postgres `LISTEN/NOTIFY` + `FOR UPDATE SKIP LOCKED` polling kombinasyonu.

1. **Outbox tablosu (her modülde):**
   ```sql
   CREATE TABLE <module>_outbox (
       id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
       tenant_id UUID NOT NULL,
       aggregate_type TEXT NOT NULL,
       aggregate_id UUID NOT NULL,
       event_type TEXT NOT NULL,
       event_version INT NOT NULL,
       payload JSONB NOT NULL,
       subject TEXT NOT NULL,             -- NATS subject
       is_synced BOOLEAN NOT NULL DEFAULT FALSE,
       is_dead BOOLEAN NOT NULL DEFAULT FALSE,
       retry_count INT NOT NULL DEFAULT 0,
       next_retry_at TIMESTAMPTZ,
       last_error TEXT,
       created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
       synced_at TIMESTAMPTZ
   );
   CREATE INDEX ON <module>_outbox (is_synced, next_retry_at) WHERE is_synced = FALSE AND is_dead = FALSE;
   CREATE INDEX ON <module>_outbox (aggregate_id, id) WHERE is_synced = FALSE;
   ```

2. **Write deseni:** Application transaction'ında hem domain tablo hem outbox'a yazım aynı `BEGIN...COMMIT` içinde. COMMIT sonrası trigger:
   ```sql
   CREATE OR REPLACE FUNCTION notify_outbox() RETURNS TRIGGER AS $$
   BEGIN
       PERFORM pg_notify('outbox_new', TG_TABLE_NAME);
       RETURN NEW;
   END;
   $$ LANGUAGE plpgsql;
   
   CREATE TRIGGER <module>_outbox_notify
       AFTER INSERT ON <module>_outbox
       FOR EACH ROW EXECUTE FUNCTION notify_outbox();
   ```

3. **Dispatcher goroutine:**
   - Uygulama binary'si içinde çalışır (ayrı worker binary değil — hayatı app ile aynı).
   - LISTEN `outbox_new` kanalında bekler.
   - Notify geldiğinde tick'i tetikler; ayrıca 5 saniye interval fallback polling.
   - Her tick'te:
     ```sql
     SELECT * FROM <module>_outbox
     WHERE is_synced = FALSE
       AND is_dead = FALSE
       AND (next_retry_at IS NULL OR next_retry_at <= now())
     ORDER BY aggregate_id, id
     FOR UPDATE SKIP LOCKED
     LIMIT 100;
     ```

4. **Aggregate-based sıralama:** `aggregate_id` hash'i ile worker partition. Aynı aggregate için event'ler aynı worker goroutine'unda, sıralı işlenir. Farklı aggregate'ler paralel.

5. **Publish:** NATS JetStream'e `Nats-Msg-Id: <outbox.id>` header ile. NATS dedupe window (2 dakika) cross-crash koruma sağlar.

6. **Başarı:** `UPDATE <module>_outbox SET is_synced = TRUE, synced_at = now() WHERE id = $1`.

7. **Başarısızlık (retry):**
   ```sql
   UPDATE <module>_outbox SET
       retry_count = retry_count + 1,
       next_retry_at = now() + backoff(retry_count),
       last_error = $2
   WHERE id = $1;
   ```
   Backoff: `min(60s, (2 ^ retry_count) + random(0,1000ms))`.

8. **Poison message:** `retry_count > 10` → `is_dead = TRUE`, event `dlq_events` tablosuna kopyalanır, monitoring alarmı tetiklenir (Prometheus `outbox_dead_total` metric).

9. **Dead-letter inceleme:** Admin API endpoint'i (`GET /v1/ops/dlq`) — dead event'leri listeler, manuel replay tetikleyebilir.

10. **Metrics (OpenTelemetry):**
    - `outbox_pending_total{module}` — bekleyen event sayısı
    - `outbox_dispatch_duration_seconds` — publish latency
    - `outbox_dispatch_failures_total{module, reason}`
    - `outbox_dead_total{module}`

11. **Edge (local server) için aynı pattern, farklı backend:**
    - SQLite — `LISTEN/NOTIFY` yok, sadece polling (500ms interval).
    - `FOR UPDATE SKIP LOCKED` yerine `UPDATE ... WHERE id IN (SELECT ... LIMIT N)` + retry.
    - NATS upstream bağlantısı üzerinden cloud'a publish.

12. **Faz 2 Debezium geçişi:** Outbox tablosu CDC kaynağı olur. Uygulama kodu değişmez; dispatcher Debezium'a taşınır. Sadece `_outbox` tabloları REPLICA IDENTITY FULL ayarlanır.

#### Sonuçlar

**İyi:**
- Crash-safe: dispatcher crash olursa event kalıcı DB'de, yeniden başladığında devam eder.
- Sıralama garantisi aggregate-başına korunur; paralel throughput elde edilir.
- Back-pressure: retry backoff downstream yük korur.
- Poison message isolation + alarming.

**Kötü / Dikkat:**
- Her modülün kendi outbox tablosu var → migration + index yönetimi modül başına. Bu aslında iyi (bounded context), ama tekrar var.
- LISTEN/NOTIFY bağlantı bazlı; dispatcher'ın uzun ömürlü bir connection tutması gerekir (ayrı bağlantı, pool'dan değil).
- Edge SQLite 500ms polling → cloud sync latency'si için alt sınır 500ms. Kabul edilebilir.

**Risk:**
- `REPLICA IDENTITY FULL` (Faz 2 Debezium) tablo write amplification'ı artırır. Outbox tabloları append-only olduğu için etki sınırlı.
- Dispatcher ile application aynı binary'de → CPU spike'ı app'ı etkiler. Ölçüm + HPA ayarı Faz 1'de yapılır.

#### Değerlendirilen Alternatifler

- **Direct publish (outbox yok):** Reddedildi. DB commit + NATS publish dual-write, atomic değil; event kaybı/duplicate riski.
- **Debezium Faz 0'da:** Reddedildi. Operasyonel yük (Kafka + Connect + monitoring) MVP için fazla. Faz 2'de gelir.
- **pg_cron + polling:** Reddedildi. LISTEN/NOTIFY düşük latency; cron minimum 1dk.
- **Temporal workflow olarak dispatcher:** Reddedildi. Temporal'ın kendisi event sourcing için overkill; outbox Temporal'dan önce gelmeli (Temporal'ın event store'u bile bu pattern'e benzer).

---

### ADR-DATA-002: Event Immutability Kuralı

**Durum:** Kabul Edildi
**Tarih:** 2026-04-19
**İlgili delta:** P0-6

#### Bağlam

Baseline'da "Consumer `ON CONFLICT (id) DO NOTHING`" var. Bu iyi bir başlangıç ama eksik: **bir event publish edildikten sonra payload'u değişebilir mi?** Cevap belirsiz kalırsa:

- Edge tarafında "aynı event ID ile düzeltilmiş payload" republish edilirse?
- Consumer `ON CONFLICT DO NOTHING` → eski kalır; `ON CONFLICT DO UPDATE` → last-write-wins (tehlikeli — sıraya bağımlı).
- Event sourcing doktrininde event'ler **immutable** olmalı; değişiklik = yeni event.

Bu kural netleşmezse farklı modüller farklı pattern benimser, sistem tutarsızlaşır.

#### Karar

**Event'ler tamamen immutable'dır. Publish edilmiş bir event'in `event_id`, `payload`, `event_type`, `event_version` alanları asla değişmez.**

1. **Güncelleme = yeni event:**
   - ❌ Yanlış: `check.updated.v1` aynı `event_id` ile republish
   - ✅ Doğru: Ayrı event tipleri — `check.item_added.v1`, `check.item_voided.v1`, `check.discount_applied.v1`, `check.closed.v1`. Her biri farklı `event_id`, farklı timestamp.

2. **Consumer deseni (tüm modüllerde sabit):**
   ```sql
   INSERT INTO <projection_table> ...
   ON CONFLICT (event_id) DO NOTHING;
   ```
   `ON CONFLICT DO UPDATE` **yasaktır**. Idempotency event_id ile korunur, payload değişmez.

3. **Event şeması isimlendirme kuralı:**
   - Fiil + state change: `order.created.v1`, `order.paid.v1`, `shipment.received.v1`
   - Terminal event işareti: şema dokümantasyonunda "bu event'ten sonra aggregate için başka event üretilmez" notu (ör: `check.closed.v1` terminal).
   - Ara event işareti: "bu event aggregate lifecycle'ının ortasında" (ör: `order.item_added.v1`).

4. **Consumer side-effects test:**
   - Aynı event iki kez deliver edildiğinde side-effect bir kez çalışır.
   - Platform testi: `internal/platform/eventbus/idempotent_consumer_test.go` — tüm modül consumer'ları için kontrak test.

5. **Edge senaryoları:**
   - Edge'de bir check oluşturulup, sonra iptal edilirse: `check.opened.v1` + `check.voided.v1` iki ayrı event. İlkinin payload'ı "düzeltilmez".
   - Edge offline'dayken check açılır, online olunca sync edilir: event ID aynıdır, içerik aynıdır, timestamp aynıdır (event oluşturulma anı). `ON CONFLICT DO NOTHING` zaten idempotent.

6. **Event şema evrimi:**
   - Minor değişiklik (yeni opsiyonel alan): aynı `vN` güncellenir, mevcut event'ler etkilenmez.
   - Breaking değişiklik: `vN+1` yeni dosya oluşturulur, `vN` event'leri olduğu gibi saklanır, consumer'lar migrate olana kadar her iki versiyonu tüketir.

#### Sonuçlar

**İyi:**
- Event stream gerçek bir "append-only log" — rewind, replay, audit edilebilir.
- Consumer idempotency tek pattern — tüm modüllerde `ON CONFLICT DO NOTHING`.
- Debezium Faz 2'de gelince CDC kaynağı zaten immutable, uyumlu.
- Edge ↔ cloud sync conflict-free: aynı event ID = aynı içerik.

**Kötü / Dikkat:**
- "Düzeltme" ihtiyacı varsa yeni event tipi gerekir; bazen event taxonomy şişer. Disciplined design ile önlenir.
- İlk tasarımda event sayısı fazla görünebilir; ama bu doğrudur — state change granular olmalı.

**Risk:**
- Geliştirici (veya AI) "hızlı fix" olarak payload'u güncellemek isteyebilir. Platform seviyesinde `UPDATE <module>_outbox SET payload = ...` **yasak** — lint ile engellenir; outbox tablosu write-once.

#### Değerlendirilen Alternatifler

- **Mutable event'ler (last-write-wins):** Reddedildi. Sıraya bağımlılık, replay imkânsız, CDC bozulur.
- **Event versioning per payload (aynı ID, farklı version):** Reddedildi. ID + version compound key karmaşası, consumer deduplication zorlaşır.
- **Snapshot-only:** Reddedildi. Event sourcing'in audit/replay avantajları kaybedilir.

---

### ADR-DATA-003: Timezone ve Business Day Hesaplama

**Durum:** Taslak
**Tarih:** 2026-04-19
**İlgili delta:** P1-3

#### Bağlam

"Bugünün satışları", "gün sonu raporu", "kasa kapatma" — hepsi "işletme günü" kavramına bağlı. Türkiye default `Europe/Istanbul` ama:
- Franchise zincir yurtdışı şube açabilir (Kapadokya → Dubai).
- Bar/club sabah 04:00'e kadar açık — gece 03:00'daki satış "dün" mü "bugün" mü?
- Tenant-level rapor farklı timezone'daki şubeleri nasıl gruplar?

Baseline'da `timezone: Europe/Istanbul` var ama business_day_cutoff kavramı yok.

#### Karar (Özet)

1. **Saklama:** Tüm DB zamanları `TIMESTAMPTZ` + UTC.
2. **Hesaplama:** Business day daima **branch timezone'una göre**. Tenant-level agregasyon şubelerin kendi business day'ini hesaplar, sonra tenant rapor timezone'unda gruplar.
3. **Cutoff desteği:** `branch_settings.business_day_cutoff INTERVAL` — bar/club için `'05:00:00'` gibi. Default `'00:00:00'`.
4. **Platform helper:** `internal/platform/timex/business_day.go`
   ```go
   func BranchBusinessDay(t time.Time, branch BranchRef) civil.Date
   func BranchBusinessDayRange(day civil.Date, branch BranchRef) (start, end time.Time)
   ```
5. **Test:** DST geçişleri (Türkiye şu an DST kullanmıyor, ama yasa değişebilir), cutoff dönüşü, zincir farklı timezone senaryoları için unit testler Faz 0'da yazılır.

#### İmplementasyon Detayları (Dolacak)

- `civil.Date` mi `time.Time` mi — UTC confusion'dan kaçınmak için Google'ın `cloud.google.com/go/civil` paketi.
- Raporların zaman parametresi API contract: client UTC mi, branch-local mi gönderir? (Önerilen: client branch-local, backend çevirir.)
- Temporal workflow'larda business_day kullanımı (gün kapatma retry'ları).

---

### ADR-DATA-004: Catalog Delta Sync (Faz 2 Hazırlığı)

**Durum:** Taslak
**Tarih:** 2026-04-19
**İlgili delta:** P2-5

#### Bağlam

Baseline edge'e tam catalog snapshot gönderiyor. 10k SKU'lu market zinciri için her fiyat güncellemesinde tam snapshot:
- Gereksiz network trafiği (büyük payload)
- Edge'de I/O (SQLite tam rewrite)
- Birden fazla şube aynı anda güncellenirken bandwidth saturation

Faz 1'de tam snapshot sorunsuz çalışır; Faz 2'de 100+ şube × günde birkaç güncelleme çarpımında sorun olur.

#### Karar (Özet)

**Faz 1:** Tam snapshot + `catalog_version` sayacı. Edge son snapshot version'ını saklar; değiştiyse çeker.

**Faz 2:** Event-based incremental delta.
- Event tipleri: `catalog.product.created.v1`, `catalog.product.updated.v1`, `catalog.product.deactivated.v1`, `catalog.price.changed.v1`, `catalog.variant.added.v1`, vs.
- Edge son işlediği `catalog_version`'dan sonrasını event replay ile günceller.
- Drift detection: edge periyodik olarak catalog checksum gönderir; cloud mismatch tespit ederse tam snapshot tetikler (corrective action).

#### İmplementasyon Detayları (Dolacak)

- Checksum algoritması (MD5 yeterli mi, SHA-256 mı?)
- Event ordering — aggregate (product) başına sıralı garanti
- Back-fill: yeni şube eklendiğinde tam snapshot + sonrası delta

---

## Kategori: ARCH (Mimari)

### ADR-ARCH-001: İki Katmanlı Feature Flag

**Durum:** Taslak
**Tarih:** 2026-04-19
**İlgili delta:** P1-2

#### Bağlam

Baseline'da iki yerde flag mantığı var:
1. `tenants.enabled_modules JSONB` (Faz 0, billing/entitlement kararı)
2. Unleash/GrowthBook planlandı (Faz 2, operasyonel flag)

İkisi **aynı** araçla yönetilirse karışıklık olur: "bu flag kapalı, müşteri satın almadı mı, yoksa biz mi yavaş rollout yapıyoruz?" sorusu cevapsız kalır.

#### Karar (Özet)

**Katman A — Billing / Entitlement (Faz 0):**
- `tenants.enabled_modules JSONB` — hangi modüller satın alındı
- Platform helper: `ModuleGate(ctx, moduleName) bool`
- Modül kayıt anında kontrol: flag kapalıysa HTTP route register edilmez, event subscribe edilmez, migration yine de çalışır (veri bütünlüğü için).
- Değişiklik: admin paneli → tenant update → cache invalidate + running instance'lara NATS bildirimi.

**Katman B — Operasyonel Rollout (Faz 2+):**
- **Unleash** kullanılır (ADR-ARCH-001-karar)
- Canary, gradual percentage, A/B değil sadece on/off-per-tenant için
- `FeatureFlag(ctx, flagName, defaultValue) bool` helper — `tenant_id`, `branch_id`, `plan` context'i ile evaluate edilir.

**Kesin kural:** Unleash **entitlement için KULLANILMAZ**. Entitlement = revenue-driving, audit-required, customer-facing. Unleash = dev/ops kararı.

#### İmplementasyon Detayları (Dolacak)

- Module registry pattern: `Module` interface + `ModuleGate` → register döngüsünde kontrol
- Unleash Go SDK entegrasyonu (Faz 2 başı)
- Cache stratejisi (TTL, invalidation)
- Admin panel UI'ında iki katmanın görsel ayrımı

---

### ADR-ARCH-002: asynq ve Temporal Sorumluluk Ayrımı

**Durum:** Kabul Edildi
**Tarih:** 2026-04-19
**İlgili delta:** v1'den kapanan açık soru

#### Bağlam

Stack'te hem asynq (Redis-backed, kısa süreli jobs) hem Temporal (workflow engine, Faz 3'te aktif) var. İki araç ne zaman ne için kullanılacağı net değilse:
- Geliştirici/AI her iş için hangisini kullanacağına kafa yorar.
- Sınır bulanıklaşır; bazı işler her ikisinde de çalışır → tutarsızlık.
- Operasyonel yük iki kat (iki ayrı monitoring, iki ayrı dead-letter yönetimi).

#### Karar

**Net sınır:**

| Araç | Kullanım | Örnekler |
|---|---|---|
| **asynq** | Fire-and-forget, < 5 dakika, dış servis bağımlılığı düşük, retry basit | Email gönderme, webhook retry, push notification, thumbnail generation, CSV export |
| **Temporal** | State-ful, uzun süreli (dakikalar-saatler-günler), karmaşık retry, compensation, human task | MRP planlama, gün sonu ÖKC akışı, fatura provider retry zinciri, çoklu adım sipariş orchestration, sevkiyat kararı |

**Kritik istisna:** Outbox dispatcher **asynq'e konmaz** (ADR-DATA-001). Outbox = ayrı goroutine, app binary'si içinde, lifecycle app ile aynı.

#### Faz Dağılımı

- **Faz 0-1:** asynq aktif. Temporal docker-compose'da ayakta ama worker binary (`cmd/worker`) başlatılmaz. Temporal UI erişilebilir (sonraki fazlarda hazırlık için).
- **Faz 3:** Temporal worker'ları devreye girer; MRP, gün sonu, fatura retry akışları Temporal workflow olarak yazılır.

#### Karar Ağacı (AI İçin)

Bir iş geldiğinde AI şu soruları sırayla sorar:
1. **"Bu işin süresi 5 dakikayı geçer mi?"** — Evet → Temporal. Hayır → 2'ye geç.
2. **"Bu iş dış servis hatasında saatlerce retry gerektirir mi?"** — Evet → Temporal. Hayır → 3'e geç.
3. **"Bu iş çok adımlı ve ara durumda konfirmasyon/compensation gerektirir mi?"** — Evet → Temporal. Hayır → 4'e geç.
4. **"Bu bir domain event dispatch mi?"** — Evet → **Outbox dispatcher** (asynq değil). Hayır → 5'e geç.
5. **Varsayılan:** asynq.

#### Sonuçlar

**İyi:**
- Net karar kuralı; "hangisini kullanayım?" sorusu 10 saniyede çözülür.
- Operasyonel ayrım: asynq için Redis + kısa retry + basit monitoring; Temporal için kendi UI + history.
- Faz 3'e kadar Temporal operasyonel yükü yok (worker kapalı).

**Kötü / Dikkat:**
- Faz 2-3 geçişinde bazı asynq job'ları Temporal'a taşınabilir. Bu kaçınılmaz (MRP gibi işler zamanla büyür); net sınır bu taşımayı kolaylaştırır.
- Temporal infra docker-compose'da ayakta ama kullanılmıyor → küçük storage + CPU overhead. Kabul edilebilir.

**Risk:**
- "Biraz uzun, ama 5 dakikayı geçmez" tipi gri alan işler (örn: büyük rapor üretimi) — gri kalıyorsa Temporal tercih edilir (kaçak büyüme riskine karşı).

#### Değerlendirilen Alternatifler

- **Yalnız asynq:** Reddedildi. MRP, gün sonu ÖKC akışı gibi stateful workflow'lar asynq'te cehennem.
- **Yalnız Temporal:** Reddedildi. Email gönderme gibi basit iş için Temporal workflow overhead.
- **River (Go-native job queue):** Değerlendirildi, reddedildi. asynq daha olgun, daha geniş community, Redis ekosistemi ile uyumlu.

---

### ADR-ARCH-003: Arama Backend Seçimi

**Durum:** Kabul Edildi
**Tarih:** 2026-04-19
**İlgili delta:** v1'den kapanan açık soru

#### Bağlam

Faz 2'de search backend gerekli: menü/ürün arama (POS hot path), müşteri arama (CRM), fatura arama (admin). Multi-tenant senaryoda ana soru: **tenant izolasyonu nasıl yapılır?**

İki aday:
- **Meilisearch** — filter-based izolasyon (`filter: tenant_id = X`).
- **Typesense** — collection-per-tenant veya alias ile izolasyon.

Postgres full-text search değerlendirildi ama yetersiz: Türkçe lemmatization zayıf, autocomplete < 50ms gerekli POS için, fuzzy matching sınırlı.

#### Karar

**Typesense kullanılır.**

Gerekçeler:
1. **Collection-per-tenant native izolasyon:** Query'ye filter eklemek zorunda değilsin; yanlışlıkla tenant sızıntısı yapısal olarak daha zor.
2. **Autocomplete performansı:** Typesense benchmark'larda Meilisearch'ten 1.5-2x hızlı; POS hot path'te anlamlı.
3. **Alias yönetimi:** Collection migration (re-index) için alias swap — downtime sıfır.
4. **Go client olgun:** Resmi `typesense/typesense-go` var, sürekli güncelleniyor.
5. **Türkçe dil desteği:** `locale: "tr"` + custom synonym dosyası ile makul. Hiçbiri mükemmel değil, ikisi de yeterli.

#### Faz Dağılımı

- **Faz 0-1:** Kullanılmaz. Menü araması Postgres trigram index (`pg_trgm`) ile — basit, yeterli, ek operasyonel yük yok.
- **Faz 2:** Typesense cluster (3 node HA) docker-compose'a eklenir. Module-by-module migration:
  - İlk: Catalog arama (menü, ürün)
  - Sonra: Party (müşteri/tedarikçi arama)
  - En son: Invoice (fatura arama)

#### Collection Stratejisi

- Naming: `{tenant_id}_catalog`, `{tenant_id}_customers`
- Alias: `current_catalog_{tenant_id}` → collection'a pointer (re-index için)
- Synonym dosyaları: tenant-specific JSON, `configs/typesense/synonyms/{tenant_id}.json`

#### Türkçe Karakter Normalizasyonu

Test edilecek senaryolar (Faz 2 başı spike):
- "İstanbul" → "istanbul" araması sonuç döndürmeli mi? (Evet, case-insensitive + diacritic-insensitive)
- "Ğ/ğ", "Ş/ş", "Ç/ç" normalizasyonu
- Turkish stop words (`ve`, `ile`, `için`, ...)
- Synonym örneği: "cola" ↔ "kola" ↔ "coca cola"

#### Sonuçlar

**İyi:**
- Native tenant izolasyonu.
- POS hot path performansı.
- Zero-downtime re-index.

**Kötü / Dikkat:**
- Collection başına overhead — 10k tenant × collection = Typesense metadata yükü. Cluster sizing Faz 2'de planlanır.
- Türkçe için custom synonym yönetimi manuel (şu an).

**Risk:**
- Typesense ekibi küçük (Meilisearch'e göre). Ticari sürdürülebilirlik izlenmeli; alternatif plan: Meilisearch'e fallback.

#### Değerlendirilen Alternatifler

- **Meilisearch:** Reddedildi — filter-based izolasyon daha kırılgan; autocomplete benchmark farkı.
- **Elasticsearch:** Reddedildi — operasyonel yük yüksek, overkill.
- **Postgres pg_trgm + tsvector:** Faz 0-1 için kabul; Faz 2+ için yetersiz (Türkçe lemma, autocomplete latency).
- **Algolia (SaaS):** Reddedildi — maliyet + veri egemenliği (KVKK).

---

## Kategori: FISCAL (Mali / Yasal)

### ADR-FISCAL-001: Fiscal Device Adapter Interface

**Durum:** Kabul Edildi
**Tarih:** 2026-04-19
**İlgili delta:** P0-7 (v2'de P1'den P0'a yükseltildi)

#### Bağlam

Türkiye'de restoran, bar, market, food truck için perakende satışlar **YN ÖKC (Yeni Nesil Ödeme Kaydedici Cihaz)** üzerinden kayıt edilmek zorunda. Gelir İdaresi Başkanlığı (GİB) bu zorunluluğu 2015'ten beri aşamalı olarak tüm perakende işletmelere yaydı. ÖKC'den geçmemiş satış yasal değildir; işletme ceza + ruhsat askısı riski altındadır.

ÖKC cihazlarının markaları farklı (Hugin, Profilo, Beko, Ingenico YN, Verifone YN) ve her birinin SDK'sı/protokolü birbirinden bağımsız. Payment modülü fiscal entegrasyon düşünülmeden yazılırsa, Faz 2'de her marka için modülü yarı yarıya yeniden yazmak gerekir.

**v2 kararı:** Gerçek cihaz SDK entegrasyonları Faz 2'de kalır; ancak **interface + event şemaları + mock adapter + şema değişiklikleri Faz 0'da yapılır**. Bu Payment modülünün baştan doğru temelle yazılmasını sağlar.

#### Karar

1. **Fiscal Device Adapter Interface:** `internal/modules/payment/public/fiscal.go`
   ```go
   type FiscalDeviceAdapter interface {
       OpenDay(ctx context.Context, branch BranchRef) (FiscalDaySession, error)
       RegisterSale(ctx context.Context, session FiscalDaySession, sale FiscalSale) (FiscalReceipt, error)
       VoidSale(ctx context.Context, session FiscalDaySession, receipt FiscalReceipt) error
       CloseDay(ctx context.Context, session FiscalDaySession) (ZReport, error)
       GetStatus(ctx context.Context) (DeviceStatus, error)
   }
   
   type FiscalDaySession struct {
       SessionID   string
       DeviceID    string
       BranchID    uuid.UUID
       OpenedAt    time.Time
       OpeningCashAmount decimal.Decimal
   }
   
   type FiscalSale struct {
       SaleID      uuid.UUID
       Items       []FiscalSaleItem
       PaymentType string // cash | card | mixed
       TotalAmount decimal.Decimal
       TaxBreakdown []FiscalTaxLine
       Customer    *FiscalCustomer // opsiyonel, e-fatura için
   }
   
   type FiscalReceipt struct {
       ReceiptNo    string // cihazdan gelen fiş numarası
       FiscalMemory string // cihaz mali hafıza referansı
       PrintedAt    time.Time
       QRCode       string // GIB QR (yasal zorunluluk)
   }
   
   type ZReport struct {
       ReportNo     string
       BranchID     uuid.UUID
       DayOpenedAt  time.Time
       DayClosedAt  time.Time
       TotalSales   decimal.Decimal
       TotalTax     decimal.Decimal
       SaleCount    int
       RawData      []byte // cihaz formatı, denetim için saklanır
   }
   ```

2. **Event şemaları** (`contracts/events/fiscal/`):
   - `fiscal.day.opened.v1`
   - `fiscal.sale.registered.v1`
   - `fiscal.sale.voided.v1`
   - `fiscal.day.closed.v1`
   - `fiscal.zreport.generated.v1`

   Her event tenant_id + branch_id + device_id'yi taşır.

3. **Mock adapter** (Faz 0-1'de test ortamında kullanılır):
   ```go
   type MockFiscalAdapter struct {
       // fake receipt numbers, in-memory session state
   }
   ```
   Mock deterministik ama test senaryolarında hata simüle edebilir (`WithError(ErrDeviceOffline)`).

4. **Şema değişikliği:** `branch_settings` tablosuna:
   ```sql
   ALTER TABLE branch_settings ADD COLUMN fiscal_device_type TEXT NOT NULL DEFAULT 'none';
   -- 'none' | 'mock' | 'hugin' | 'profilo' | 'beko' | 'ingenico_yn' | 'verifone_yn'
   ALTER TABLE branch_settings ADD COLUMN fiscal_device_config JSONB NOT NULL DEFAULT '{}';
   ```

5. **Payment modülü kuralı:** Payment modülü **her ödeme işleminde** FiscalAdapter çağırır. `fiscal_device_type = 'none'` ise MockAdapter no-op döner (ama interface çağrısı her zaman yapılır).
   ```go
   func (s *PaymentService) ProcessPayment(ctx context.Context, req PaymentRequest) (*Payment, error) {
       // ... kart/nakit işlemi
       
       // Fiscal registration
       session := s.fiscalSession.GetOrOpen(ctx, req.BranchID)
       receipt, err := s.fiscalAdapter.RegisterSale(ctx, session, buildFiscalSale(req))
       if err != nil { /* handle */ }
       
       // Payment kaydına receipt bağla
       payment.FiscalReceiptNo = receipt.ReceiptNo
       payment.FiscalQRCode = receipt.QRCode
       
       // Event publish
       s.events.Publish("fiscal.sale.registered.v1", ...)
   }
   ```

6. **Faz 2 gerçek adapter'lar:** Her marka için ayrı package:
   ```
   internal/modules/payment/fiscal/
   ├── mock/
   ├── hugin/
   ├── profilo/
   ├── beko/
   ├── ingenico_yn/
   └── verifone_yn/
   ```
   Her birinde `NewAdapter(config) FiscalDeviceAdapter` constructor. Runtime'da `branch_settings.fiscal_device_type`'a göre factory seçim yapar.

7. **Temporal workflow entegrasyonu (Faz 3):**
   - Gün sonu kapatma akışı Temporal workflow olarak yazılır.
   - Cihaz offline'sa saatlerce retry-resume gerekli → Temporal'ın doğal use-case'i.
   - Z raporu eksikse alarm + manuel müdahale.

#### Sonuçlar

**İyi:**
- Payment modülü Faz 0'da doğru temelde yazılır; Faz 2'de gerçek cihaz entegrasyonu **sadece adapter ekleme** olur.
- Mock adapter ile test ortamı kolay; CI'da fiscal integration test koşabiliriz.
- Event stream Faz 1'den itibaren fiscal event'leri üretir; Faz 2'de consumer'lar zaten kuruluyken gelir.
- Yasal uyum roadmap'i net: Faz 0 interface, Faz 2 gerçek cihaz, Faz 3 workflow.

**Kötü / Dikkat:**
- Payment modülü MockAdapter ile "çalışıyor görünür" ama yasal değildir (fiscal_device_type = 'none'). Production checklist: hiçbir tenant 'none' ile satışa çıkamaz. Bu admin panel validation'ı ile zorlanır.
- Her FiscalAdapter çağrısı Payment hot path'inde ek latency. Mock ≈ 0ms; gerçek cihaz ≈ 100-500ms. Timeout + retry stratejisi adapter-specific.

**Risk:**
- ÖKC regülasyonları değişebilir (GIB yeni TSM protokolü çıkarır, vs). Adapter interface değişebilir; v2 interface Faz 2'de düşünülmeli (backward compat plan).
- SDK lisansları: bazı markalar ücretli SDK. Lisans maliyeti Faz 2 bütçesine yazılmalı.

#### Değerlendirilen Alternatifler

- **Faz 2'de interface + implementation birlikte:** Reddedildi. Payment modülü fiscal-aware yazılmazsa yeniden yazım.
- **Direct SDK integration (interface yok):** Reddedildi. Marka değişimi = tüm Payment kodunu değiştirmek.
- **Harici fiscal proxy servisi:** Değerlendirilecek Faz 3+. PCI/mali scope daraltma için payment mikroservisi ayrıldığında fiscal ayrı servis olabilir.

---

## Kategori: OPS (Operasyonel)

### ADR-OPS-001: Backup ve Disaster Recovery

**Durum:** Taslak
**Tarih:** 2026-04-19
**İlgili delta:** P2-1

#### Bağlam

Baseline'da backup/DR planı yok. Shared-schema multi-tenant'ta "tek tenant'ı geri al" ihtiyacı non-trivial:
- Cluster-level PITR mümkün ama tek tenant'ı etkilemez (tüm veriyi geri alır).
- Tenant-level recovery: soft delete + audit log + event replay kombinasyonu gerekir.

#### Karar (Özet)

1. **Cluster-level backup:** pgBackRest veya WAL-G. Prod'da saatlik full + sürekli WAL shipping. RPO < 15dk, RTO < 1 saat hedef.
2. **PITR:** Cluster bazında, major incident senaryolarında.
3. **Tenant-level logical recovery:** Event sourcing-lite. Audit log + outbox event replay + soft delete + 30 gün restore window.
4. **Mali kayıt istisnası:** Payment, invoice, fiscal receipt **hard delete yasak** (ADR-FISCAL-001 gereği de). 10 yıl saklama.
5. **Backup verification:** Haftalık otomatik staging restore + smoke test.

#### İmplementasyon Detayları (Dolacak)

- pgBackRest vs WAL-G seçimi
- S3/MinIO backup hedefi
- Encryption at rest (backup'ların şifrelenmesi)
- Restore playbook (runbook)

---

### ADR-OPS-002: Tenant Offboarding ve KVKK Uyumu

**Durum:** Taslak
**Tarih:** 2026-04-19
**İlgili delta:** P2-2

#### Bağlam

KVKK m.7 ve GDPR right to erasure: müşteri ayrılırsa veri silme hakkı. Shared schema'da bu non-trivial + denetlenebilir olmalı.

#### Karar (Özet)

1. **İki fazlı silme:**
   - Soft disable (Gün 0): tenant devre dışı, veri erişimi durur, 90 gün grace period.
   - Hard delete (Gün 90): tüm tablolarda `DELETE WHERE tenant_id = ?` + audit log'a silme sertifikası.
2. **Cascade sırası:** Yaprak tablolardan başla (order_items, payments'in PII alanları) → aggregate'ler → tenant satırı.
3. **Silme sertifikası:** JSON imzalı, hash + timestamp + satır sayıları. Minio'da 10 yıl.
4. **Mali kayıt istisnası:** PII alanları anonymize edilir (müşteri adı → `[REDACTED-{tenant_hash}]`), mali kayıt metadata'sı 10 yıl kalır.
5. **Keycloak:** Realm kullanıcıları silinir/anonymize edilir.
6. **Backup'lardaki veri:** Restore edilirse silme sertifikası ile birlikte yeniden silinir (re-apply).

#### İmplementasyon Detayları (Dolacak)

- Offboarding workflow Temporal'da mı (uzun süreli, 90 gün bekleme)?
- Anonymization fonksiyonu (hash algoritması, reversible değil)
- Customer-facing "verilerimi sil" endpoint'i
- Legal hold (dava durumunda silmeme) mekanizması

---

### ADR-OPS-003: Rate Limiting Stratejisi

**Durum:** Taslak
**Tarih:** 2026-04-19
**İlgili delta:** P2-3

#### Bağlam

Multi-tenant'ta "noisy neighbor" kaçınılmaz. Baseline'da Redis var ama platform katmanında rate limiter yok.

#### Karar (Özet)

**Dört seviyeli rate limit:**

| Seviye | Kapsam | Nerede |
|---|---|---|
| **Global** | IP bazlı DDoS koruma | Traefik/edge proxy |
| **Tenant** | Plan'a göre (starter/pro/enterprise) | Platform middleware |
| **Endpoint** | Expensive endpoint'ler (rapor, export) | Platform middleware |
| **Device** | POS tablet başına burst limit | Platform middleware |

**Algoritma:** Sliding window log (go-redis + Lua script).
**Response:** 429 + RFC 7807 + `Retry-After` header.

#### İmplementasyon Detayları (Dolacak)

- Plan limits config kaynağı (DB vs config file)
- Burst vs sustained rate
- Rate limit header'ları (`X-RateLimit-Remaining` vs)
- Exemption (internal tools, webhook receiver)

---

### ADR-OPS-004: Cost Observability

**Durum:** Taslak
**Tarih:** 2026-04-19
**İlgili delta:** P2-4

#### Bağlam

Modül-modül satış için her tenant'ın gerçek kaynak maliyeti bilinmeli. Yanlış fiyatlama = zarar. Tenant-tagged metric'ler baştan planlanmazsa sonradan eklemek cost attribution'ı geriye dönük mümkün değil.

#### Karar (Özet)

1. **Tenant-tagged metrics:** Tüm OpenTelemetry metric'leri `tenant_id` label'ı taşır.
2. **Kardinalite yönetimi:** 10k+ tenant'ta tenant_id hash bucket'lama (üst 100 tenant: tam label, alt: bucket).
3. **Maliyet kalemleri:**
   - **DB:** `pg_stat_user_tables` + `pg_total_relation_size` tenant bazlı (partition Faz 3).
   - **NATS:** subject bazlı throughput (tenant_id subject'in parçası).
   - **MinIO:** prefix-per-tenant usage API.
   - **Compute:** Kubernetes pod tenant label (Faz 2+).
4. **Aylık tenant usage report:** Admin panel + opsiyonel customer self-service.
5. **Fiyatlama feedback:** Actual cost > plan fiyatı olan tenant'lar için alert.

#### İmplementasyon Detayları (Dolacak)

- Prometheus kardinalite limitleri ve bucketting stratejisi
- Cost attribution rapor formatı
- Alerting threshold'ları (margin < %20 ise uyarı)

---

# BÖLÜM 3 — AI İÇİN UYGULAMA KURALLARI

## 1. Okuma Sırası

AI implementasyon ajanı bu dokümanı ilk kez okuduğunda:

1. **Bölüm 1'i bütünüyle oku.** Delta tablosunu kafana yerleştir — hangi delta hangi öncelikte, hangi ADR ile ilişkili.
2. **Bölüm 2'de P0 ADR'larını (✅ işaretli) önce oku:** SEC-001, SEC-002, AUTH-001, AUTH-002, SEC-003, DATA-001, DATA-002, FISCAL-001, ARCH-002, ARCH-003 (10 tam ADR). Bunlar "kabul edildi" durumunda, implementasyona başlamadan önce bağlam sağlarlar.
3. **P1/P2 taslak ADR'larını (📝 işaretli) sadece ilgili delta'ya sıra geldiğinde** oku. Taslak durumunda oldukları için implementasyon sırasında "Kabul Edildi"ye çevrilecekler.
4. **Baseline dokümanları** (`docs/roadmap.md`, `docs/architecture.md`, `docs/schema.md`, `docs/offline-sync.md`) referans olarak elinde olsun, ama bu delta dokümanı onların **üstüne** gelir — çelişki varsa delta kazanır (Bölüm 3.5'e bakınız).

## 2. PR Akışı

**Her delta = bir PR.** 16 delta = 16 PR (ayrıca 3 destekleyici ADR'ın dokümantasyon PR'ları + baseline düzeltme PR'ları eklenebilir).

### PR Şablonu

Her PR açıklamasında şu bölümler **zorunlu**:

```markdown
## Delta Referansı
- Delta: P0-X (örn: P0-3)
- ADR: docs/adr/AUTH-001-four-layer-authorization.md
- Durum: Bu PR ADR'daki hangi "Karar" maddelerini uyguluyor?

## Baseline Değişiklikleri
- Hangi baseline doküman(lar)ı güncellendi?
- Kısa fark özeti:

## Test Durumu
- Yeni testler: [liste]
- Mevcut testler: hepsi geçti / değiştirdiklerim: [liste]

## Sapma
- ADR'dan sapan bir şey var mı? Varsa ADR güncellenmeli mi?
- Evetse: ADR'ın hangi bölümü + gerekçe
```

PR bu şablonu doldurmadan açılmaz; CI bunu kontrol eder.

### Sıralama

- **P0 → P1 → P2 sırasında ilerle.** P0 bitmeden P1'e geçme.
- **Kategori içinde bağımlılık sırası:** SEC-001 → SEC-002 (DB rolleri önce). AUTH-001 → SEC-003 (authz katmanları idempotency middleware'ini şekillendirir). DATA-002 → DATA-001 (immutability kuralı outbox pattern'i şekillendirir).
- **Paralel yürütülebilir:** FISCAL-001 diğer P0'lardan bağımsız; ARCH-002, ARCH-003 her aşamada paralel.

### PR Boyutu

- Tek PR'da en fazla **bir modülün tek yönünü** değiştir. RLS policy'leri + module wiring + test iskeleti farklı PR'lar.
- **PR başına ideal 300-600 satır diff.** Bunu aşıyorsa alt PR'lara böl.
- **ADR ve implementation ayrı PR olabilir:** Önce ADR PR'ı (sen + operatör review), sonra implementation PR'ı (ADR'a refer).

## 3. ADR Güncelleme Disiplini

### Ne Zaman ADR Güncellenir?

1. **Taslak ADR → Kabul Edildi:** İlgili delta implementasyonu başlarken ADR'ın `Durum`u `Kabul Edildi`ye çevrilir. "İmplementasyon Detayları (Dolacak)" bölümü doldurulur.
2. **Implementation'dan dönen öğrenmeler:** Implementasyon sırasında keşfedilen şeyler "Sonuçlar" bölümüne eklenir. "Kötü/Dikkat" ve "Risk" kısımları en çok bu adımda zenginleşir.
3. **Kararın değişmesi:** Eğer bir ADR kararı ciddi şekilde değişecekse, **eski ADR'ı Durum: Değiştirildi** yap, yeni ADR oluştur (ör: `SEC-001-v2`), eski ADR'a "replaced by SEC-001-v2" linki bırak.

### ADR Değiştirme Kuralı

- ADR **asla silinmez.** Sadece `Durum: Değiştirildi` veya `Durum: Reddedildi`ye çevrilir.
- Git history yeterli değil — ADR dosyasının kendisi karar tarihini ve durumunu gösterir.

## 4. Çelişki Çözümü

### Baseline ↔ Delta Çelişkisi

Eğer baseline doküman ile bu delta dokümanı çelişiyorsa:
- **Delta kazanır.**
- Baseline doküman delta'ya göre güncellenir (implementasyon PR'ının parçası olarak).
- AI ajanı sessizce seçim yapmaz; PR description'da çelişkiyi belirtir.

### ADR ↔ Baseline Çelişkisi

Bir ADR baseline'daki ifadeyi açıkça çürütüyorsa (örn: SEC-001 pgBouncer bilgisini düzeltiyor):
- ADR kazanır.
- Baseline düzeltilir, ADR'a referans verilir.

### ADR ↔ ADR Çelişkisi

Eğer iki ADR arasında çelişki fark edersen:
- **DUR.** Implementasyonu durdur.
- PR description'da veya issue'da operatöre sor.
- Operatör çelişkiyi çözer, ADR'lardan biri güncellenir.

**Sessizce birini seçmek yasaktır.**

## 5. Gri Alan Soruları

Aşağıdaki durumlarda AI **implementasyonu durdurmalı** ve operatöre sormalı:

1. **Yeni bir ADR gerekip gerekmediği belirsiz:** "Bu karar ADR-worthy mi, yoksa implementation detail mi?"
2. **Alternatif teknoloji seçimi:** "X paketi yerine Y paketi kullanmak istiyorum, bu ADR'da geçmiyor."
3. **Şema değişikliği baseline'ı etkiler:** Yeni migration baseline `docs/schema.md`'de olmayan tablo/kolon ekliyorsa.
4. **Event şeması baseline'ı etkiler:** Yeni event tipi baseline'da listelenmemiş.
5. **Güvenlik kararı:** Herhangi bir auth/authz/crypto kararı — şüphen varsa sor.

## 6. Test Disiplini

Her delta için şu test katmanları:

1. **Unit test:** Fonksiyon seviyesi, mock dependency'ler.
2. **Integration test:** testcontainers-go ile gerçek Postgres + NATS + Redis.
3. **RLS sızıntı testi (SEC-001/002 kapsamında):** Her yeni tablo için otomatik.
4. **Contract test (event'ler için):** `contracts/events/*.json` → Go struct → publish/consume round-trip.
5. **E2E test (kritik akışlar):** POS satış akışı, fiscal register, check close.

Test-first değil test-parallel: implementation ve test aynı PR'da.

## 7. Lint ve Kod Kalitesi

- `golangci-lint run` CI'da zorunlu, PR için geçmek şart.
- `go-arch-lint check` modül bağımlılıklarını kontrol eder.
- **Özel lint kuralları (custom):**
  - `pool.Query`, `pool.Exec` dışarıda kullanımı yasak (SEC-001)
  - `SET` komutu (LOCAL'siz) yasak (SEC-001)
  - `ON CONFLICT DO UPDATE` outbox tablolarında yasak (DATA-002)
  - `internal/modules/X/{domain,repo,http,events}` paketlerini başka modülden import etmek yasak (baseline architecture)
  - `UPDATE <module>_outbox SET payload` yasak (DATA-002)

## 8. Dokümantasyon Dili

- **Kod ve kod içi yorumlar:** İngilizce.
- **ADR'lar, `docs/` dosyaları, PR description'ları, commit message'ları:** Türkçe.
- **Event şema dosyaları (JSON Schema):** Alan isimleri İngilizce snake_case, description alanları Türkçe.

## 9. Operatör Etkileşimi

### Operatörün Sorumluluğu

- Gri alan sorularını cevaplama
- ADR review ve onay (Taslak → Kabul Edildi geçişleri)
- Stratejik karar değişikliği
- Scope değişikliği onayı

### AI'ın Sorumluluğu

- Bu delta dokümanını + baseline'ı takip etmek
- Her PR'da ADR referansı vermek
- Çelişki tespit edince durmak
- Test disiplinini korumak
- Kod kalitesi standartlarını tutmak

### Hangi Durumlarda AI Kendi Başına Karar Verebilir?

- Implementation detail'ları (değişken isimleri, dosya organizasyonu)
- Test senaryoları seçimi
- Küçük refactoring'ler (ADR'ı etkilemeyen)
- Dependency güncellemeleri (minor/patch)

### Hangi Durumlarda Sormalı?

- Yeni dependency eklemek (major versiyon, veya stack'te olmayan paket)
- ADR'da belirtilmemiş teknoloji/desen seçimi
- Scope'u genişleten kararlar
- Breaking change potansiyeli olan değişiklikler

---

## Sonuç

Bu doküman 17 ADR + 16 delta ile baseline'ı tamamlar. Hepsi birlikte okunduğunda proje için:
- **Güvenlik modeli** net (SEC + AUTH kategorileri)
- **Veri akışı kuralları** net (DATA kategorisi)
- **Teknoloji seçimleri** net (ARCH kategorisi)
- **Yasal uyum** planı net (FISCAL kategorisi)
- **Operasyonel hazırlık** iskelet var (OPS kategorisi)

**Implementasyon başladığında sırayla ilerlenir:**

1. P0 ADR'ları üzerine baseline düzeltme PR'ları (SEC-001'e göre `docs/architecture.md` pgBouncer bölümü düzelt, vs.) — ilk sprint
2. P0 implementation PR'ları — 2-4 sprint
3. P1 implementation + ADR dolumu — sonraki sprint'ler
4. P2 ADR'ları Faz 1 sonu / Faz 2 başı doldurulur

**Operatör her PR'ı review eder; AI her sorusu olduğunda durur ve sorar.**

İyi implementasyonlar.

---

## Revizyon Geçmişi

| Versiyon | Tarih | Açıklama |
|---|---|---|
| v1 | 2026-04-19 | İlk delta direktifleri (16 delta, ADR'sız) |
| v2 | 2026-04-19 | ADR paketi eklendi (17 ADR). P0-7 fiscal yükseltmesi. 4 açık soru karara bağlandı (Keycloak, asynq/Temporal, Typesense, Unleash). OPA decision sözleşmesi daraltıldı. |