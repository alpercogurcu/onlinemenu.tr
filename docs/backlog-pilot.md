# Pilot Takip Listesi — İlk Satışa Kadar

> Oluşturma: 2026-07-31 · Kapsam: **online-only pilot** (tek şube, sabit internet, edge-sync yok)
> Repo'da Jira yok; kalıcı kayıt burada tutulur. Bir madde tamamlanınca bu dosyadan silinir.
> Kardeş listeler: [backlog-fiscal.md](backlog-fiscal.md) · Referans: [lessons-from-odoo.md](lessons-from-odoo.md)

**Ürün kararı (2026-07-31):** offline-first ilk satışa girmiyor. Pilot müşteri sabit internetli,
tek şubeli bir işletme olacak. `edge-sync` Faz 2'ye kalır; ROADMAP Faz 1'in "satılabilir MVP"
tanımı bu pilottan sonra tamamlanır.

### Pilot müşteri seçim kriterleri (bağlayıcı)

| Kriter | Neden |
|---|---|
| Sabit, güvenilir internet | `edge-sync` yok; bağlantı koparsa satış durur |
| Tek şube | Zincir-geneli senaryolar test edilmedi |
| **Tek kasa (tek para çekmecesi)** | ADR-DATA-008: kasa oturumu şube başına. İki çekmece tek sayıma inerse mutabakat anlamsızlaşır — birindeki fazla diğerindeki açığı gizler |
| Marş kullanmayan segment tercih edilir | Üst segment oturarak servis marşı gerektirir (bkz. aşağıda); fast-food/kafe/paket servis gerektirmez |

**Halihazırda çalışan:** satış omurgası uçtan uca test edilmiş
(`internal/e2e/spine_test.go`: adisyon aç → sipariş → ödeme → ÖKC mali kayıt → settle → kapat),
Token/Beko X30TR entegrasyonu gerçek, admin panelinde 29 sayfa, Wails POS istemcisi.

---

## Sıra ve bağımlılıklar

```
1 WIP yeşil ──► 2 onboarding ──► 3 kasa oturumu ──► 4 gün sonu raporu
                                        │
                                        └──► (izin sözlüğü: madde 7'nin testi önce)
5 deploy hedefi  ·  6 tenant test açığı  ·  7 izin wiring testi   → paralel, bağımsız
8 Token sertifikasyon → dış bağımlılık, takvimi bizde değil
```

Madde 4 madde 3'e **bağımlı**: gün sonu raporunun nakit satırı kasa oturumunun açılış bakiyesini
kullanır (`final_count = total + açılış + nakit_hareketi`). Paralel planlanamaz.

---

## 1. WIP'i yeşile çek ve commit'le

Çalışan ağaçta commit edilmemiş iş var ve derlenmiyor.

- [ ] `role_tenant_guard_test.go` eksik import'ları (`path/filepath`, `golang-migrate/migrate/v4`)
- [ ] `fiscalStatus.test.ts` bayat beklentisi — kod doğru (`fiscalStatus.ts:462-463` bilinçli yorum),
      test "başka istasyonda" bekliyor, kod "başka işlemde" üretiyor
- [x] ADR-SEC-005'e "000013 eki" bölümü yazıldı
- [ ] `task backend:lint` yeşil — arch-lint refactor'ünün bittiğini bu doğrular
- [ ] Alanlara göre gruplu commit (`main`, Jira kodsuz — kullanıcı onayı 2026-07-31)

Bittiğinde `backlog-fiscal.md`'den şu iki madde silinir: *"memberships.tenant_id ↔ roles.tenant_id"*
ve *"Custom rol API'sinde branch_scoped taşınmıyor"*.

## 2. Personel onboarding zinciri — kopuk

**Bu satışa çıkmayı tek başına engelliyor: bugün sisteme yeni kasiyer eklenemiyor.**

Doğrulanan durum:
- `persons` tablosuna INSERT eden tek yol `PersonService.Create`; yalnızca **devre dışı**
  `POST /persons`'tan çağrılıyor (`routes.go:64-67`, yorum satırı)
- İlk Keycloak girişinde otomatik provizyon **yok** — `ListContexts` → `GetByKeycloakSub` →
  `ErrNotFound` → hata. Yeni kullanıcı boş bağlam listesi değil, hata alıyor
- `hr-core.CreateEmployee` `person_id` şart koşuyor: *"The person must already exist in the
  identity module"*; membership de öyle

> **Not — önceki değerlendirme düzeltmesi:** bu maddeyi başta "kısıtsız cross-tenant yüzey"
> diye güvenlik açığı olarak işaretlemiştim. Yanlıştı; uçlar mount edilmiyor, açık delik yok.
> Sorun güvenlik değil, **eksik yol**.

- [x] **Davranış hatası düzeltildi** (`cf5066f`): kaydı olmayan özne artık 404 değil, boş liste + 200
      alıyor. `ErrNotFound`'u yutmak burada güvenli — `GetByKeycloakSub` `WithAllTenantsReadTx`
      altında koşuyor ve `persons_select`'in `all_tenants` dalı var, yani ErrNotFound gerçekten
      "böyle kişi yok" demek, RLS'in gizlemesi değil.

### 🔴 Otomatik provizyon bloklu — karar bekliyor

Onaylanan çözüm (`/me/contexts` doğrulanmış claim'lerden person upsert'i) **uygulanabilir değil**:

**Keycloak token'ı yalnızca `sub` taşıyor.** `platform/auth/keycloak_verifier.go` `Verify`'ın son
satırı `return &KeycloakClaims{Sub: sub}, nil`; `KeycloakClaims` ve `Principal`'da Email/Name alanı
yok. Bu unutulmuş değil, ADR-AUTH-001'de böyle yazılı, ve `deploy/keycloak/README.md` realm'in
`profile`/`email` scope'larını bilinçli olarak tanımlamadığını söylüyor.
Buna karşılık `persons.email` **NOT NULL** ve düz **UNIQUE** (`identity/000001`). Hiç görülmemiş bir
özne için oraya yazılacak meşru bir değer yok; placeholder üretmek ikinci kayıtta unique çakışması
verir ve ileride bildirim kodunun ulaşacağı sahte adresler doğurur.

> **Elenen bir yol:** `persons_update` RLS'inde `all_tenants` dalı yok (`000008`, bilinçli).
> Ama bu **engel değil**: `INSERT ... ON CONFLICT (keycloak_sub) DO NOTHING` + `SELECT` kullanılırsa
> UPDATE hiç devreye girmez. `persons_insert` zaten `WITH CHECK (true)`, `persons_select`'in de
> `all_tenants` dalı var. Yani **RLS'e dokunmaya gerek yok** — tek gerçek engel e-posta claim'i.

**Karar (2026-08-01): admin panelden davet.** Elenen yollar:

| | Yol | Neden elendi |
|---|---|---|
| ~~A~~ | ~~Realm'e `email`/`profile` scope'u; `Principal` claim'leri taşısın~~ | Personel önce bir kez girip boş ekran görmek, sonra tekrar girmek zorunda kalırdı. Ayrıca ADR-AUTH-001'in token şeklini değiştirmek gerekirdi |
| ~~C~~ | ~~`persons.email` nullable + `persons_update` RLS'e all_tenants dalı~~ | RLS invaryantını (SEC-002) gereksiz yere zayıflatıyor; `DO NOTHING` yolu zaten çözüyordu |

> **Önemli:** seçilen yol e-posta claim'ine **ihtiyaç duymuyor.** E-postayı davet formunda yönetici
> giriyor; Keycloak kullanıcısını Admin API yaratıp `sub`'ı geri döndürüyor ve bağ davet anında
> kuruluyor. Yani **token şekli değişmiyor, ADR-AUTH-001 dokunulmadan kalıyor.** A'nın işi B'nin ön
> koşulu değildi — B onu tamamen atlıyor.

**Akış:** yönetici ad + e-posta + şube + rol girer → backend Keycloak kullanıcısını yaratır (veya
e-postayla mevcut olanı bulur) → dönen kullanıcı id'si `persons.keycloak_sub` olur → `persons` +
`memberships` yazılır → Keycloak parola belirleme e-postasını gönderir → personel ilk girişinde
doğrudan çalışır.

**Kapsam:**
- [x] **ADR-AUTH-003** — backend'in Keycloak'a yazma yetkisi, servis hesabı, sır yönetimi
- [x] `platform/keycloak` (`8bd4138`) — client_credentials, token önbellekli/singleflight,
      `AdminAPI` arayüzü üzerinden tüketiliyor (testler canlı Keycloak istemiyor).
      **Silme metodu yok** — "DB hatasında Keycloak kullanıcısı asla silinmez" kuralı yapısal
      olarak imkânsız kılındı, uygulanmamış değil
- [x] `POST /v1/identity/{tenantID}/staff` — davet ucu
- [x] Kısmi başarısızlık: `ON CONFLICT DO NOTHING` + aynı transaction'da yeniden SELECT.
      Unique ihlalini yakalayıp yeniden okumak transaction'ı abort ederdi (25P02)
- [x] İzin kararı: **yeni izin eklenmedi.** Seed'de hiç `identity` kaynağı yok; `identity.*`
      rotaları zaten yalnız manager wildcard'ıyla erişilebiliyor. Doğrulandı, varsayılmadı
- [x] Servis hesabı kurulumu, Vault yolu ve env değişkenleri belgelendi (`1d50fca`,
      `deploy/keycloak/README.md`)
- [ ] `deploy/keycloak/realm-onlinemenu.json`'a confidential client'ın **eklenmesi**
      (README anlatıyor, realm dosyası henüz taşımıyor — dev ortamı elle kurulum gerektiriyor)
- [ ] Admin panelinde "personel ekle → rol ata" ekranı — uç hazır, arayüz yok
- [x] `POST /persons` ve `GET /persons/{id}` devre dışı kalmaya devam ediyor

### Bu iş sırasında ortaya çıkan, kapatılmamış açıklar

- [ ] **`persons.email` Keycloak ile senkron değil.** Realm yöneticisi bir kullanıcının e-postasını
      doğrudan değiştirirse `persons.email` sessizce kayar. Sonraki davet o adresle Keycloak'ta
      kullanıcı bulamaz → ikinci kullanıcı yaratır → person yazımında `persons_email_idx`
      çakışır ve 500'e düşer. Gün-2 Keycloak operasyonlarında gerçekçi
- [ ] **Şube varlığı doğrulanmıyor.** `identity/000011` modül izolasyonu için
      `memberships.branch_id → branches` FK'sını kaldırmış; davet, var olmayan bir şubeye
      membership yazabilir ve DB seviyesinde hiçbir şey yakalamaz. Servis de doğrulayamıyor
      (`tenant/public`'e geçmeden). Önceden var olan boşluk, ama bu uç ona yeni bir yol açıyor
- [ ] **SMTP olmadan davet yarım kalıyor.** Parola e-postası düşerse davet başarılı sayılıyor
      (doğru — geri alınacak bir şey yok) ve `notification_sent: false` dönüyor, ama personel
      giriş yapamıyor. Üretimde SMTP fiilen zorunlu

## 3. Kasa oturumu + kasiyer kimlik doğrulama (tek ADR)

Karar verildi: **[ADR-DATA-008](adr/DATA-008-cash-session-cashier-identity.md)** — oturum **şube**
başına (POS istasyon kimliği yok; ADR-SEC-004 hâlâ Taslak, `devices` tablosu yok, `fiscal_terminals`
uygun değil çünkü `basket_mode: list` ile bir ÖKC şubedeki her kasaya hizmet ediyor). Kabul edilen
kısıt: **çok kasalı şube desteklenmiyor**. SEC-004 gelince nullable `station_id` + backfill ile
yükseltilir — düşük pişmanlıklı.

Tasarım referansı ve tam gerekçe: [lessons-from-odoo.md § Tier 1 madde 1 ve 4](lessons-from-odoo.md).

- [x] **ADR:** oturum sahipliği + PIN doğrulama modeli → ADR-DATA-008
- [x] `cash_sessions` + `cash_movements` + 4 durumlu makine (`3778a53`). Girdiler saklanır, beklenen
      ve fark **hiçbir yerde saklanmaz** — her okumada hesaplanır. Şubede tek açık oturum kısmi
      unique index'le (uygulama mantığıyla değil)
- [x] Vardiya içi nakit giriş/çıkış kaydı — `Idempotency-Key` zorunlu (yeniden denenen bir çıkış,
      fiilen bir kez alınmış parayı iki kez kaydederdi)
- [x] Kapatılamama guard'ı tek fonksiyonda: şubede bekleyen mali kayıt varken kapanmaz
- [x] `shifts:*` sözlüğü kullanıldı; kasiyere `create`+`update` verildi (`22e54ad`, identity/000014)
      çünkü kasiyer kendi çekmecesini sayar — her açılış/kapanış için müdür beklemek özelliği
      tek kasalı restoranda kullanılamaz kılıyordu
- [x] Kupür dökümlü sayım ekranı + açılış/durum/kapanış POS ekranları (`da2341d`)
- [x] Doğrulama hataları 500 değil 422 (`d451cb2`) — POS ekranı görevinden çıkan gerçek backend açığı
- [ ] Kuruş yuvarlama — yalnız nakitte, fark kayıt altında
- [ ] Eski oturum **uyarı** job'ı (otomatik kapatma değil — sayımı imkânsız kılar)
- [ ] Kurtarma oturumu (`rescue`) — stranded submission runbook'uyla birleştir
- [ ] Kasa hareketleri geçmiş listesi — şu an yalnız net toplam görünüyor, defter satırları değil
- [x] **PIN akışı backend'i** (`6ca7656`) — katılım, PIN'le geçiş, yönetici sıfırlaması.
      Kullanıcı sayımına karşı tek sentinel + her durumda argon2id hesabı; kilit (session, person)
      başına Redis'te, açılışı yalnız yeniden katılım
- [x] **PIN akışının POS arayüzü + katılımcı listesi ucu** (`4cd02cd`). PIN input'tan çıkmıyor,
      her gönderimden sonra koşulsuz temizleniyor, istemcide karşılaştırılmıyor
- [ ] **`Join` sırasında iki farklı durum aynı 403'ü dönüyor.** Hâlihazırda PIN'le geçmiş bir
      kasiyer "vardiyaya katıl" derse backend `ErrSessionScopedPrincipal` dönüyor, ama bu
      `ErrBranchForbidden` ile aynı gövdeye düşüyor; ekran "yetkiniz yok" diyor, oysa doğru mesaj
      "önce Keycloak ile yeniden giriş yapın". Ayırt edilebilir bir sentinel gerekiyor
- [ ] **Vardiyaya katılım otomatik değil.** Kasiyer Keycloak ile giriş yaptıktan sonra ayrıca
      "Vardiyaya Katıl" demek zorunda. Bilinçli seçim (otomatik katılım için spec yoktu) ama
      ürün tarafında bakılmalı — kasiyer katılmayı unutursa PIN'le seçilemez hale geliyor

**⚠️ Ölçülmesi gereken:** `RequireOpenSession`, oturum-kapsamlı token taşıyan **her istekte**
`IsOpen` çağırıyor ve bu düz bir SELECT değil — `WithTenantReadTx` tam bir transaction açıyor
(pool checkout + `SET LOCAL app.tenant_id` + sorgu + commit). Yani PIN'le geçmiş her kasiyerin her
isteği POS sıcak yolunda bir DB transaction'ı daha ekliyor.

Önbelleklememek bilinçli ve doğru: kapanmış bir oturumun birkaç saniye daha istek kabul etmesi,
az önce kapattığımız görünmez-para hatasının aynısını geri getirirdi. Ama maliyet ROADMAP'in
500 aktif POS hedefinde ölçülmeli (`task backend:loadtest:smoke`/`full` mevcut). Gerekirse doğru
çözüm kısa TTL'li önbellek **değil**, kapanışta açık invalidasyon (kapanış tek bir nokta).

**Bilinen davranış (hata değil):** sayım gönderildikten sonra bekleyen bir mali kayıt çözülürse
beklenen kapanış kayar ve sayım bayatlar. Backend doğru davranıyor, POS ekranı da bunu açıkça
gösterip yeniden saymaya zorluyor (dondurulmuş snapshot + `stale` bayrağı). Sessizce değişen bir
fark rakamı kasiyeri kendisine ait olmayan bir açıktan sorumlu tutardı.

## 4. Gün sonu satış özeti

Spec: Odoo `report_sale_details.py`. Admin dashboard bugün `mockSalesData` ile çalışıyor,
iki kart "Yakında" yazıyor.

- [ ] `GET /api/v1/pos/reports/sale-details` — tarih aralığı + şube + oturum filtresi
- [ ] Satış ve iade **ayrı akümülatörlerde**; birleştirmek iade oranını görünmez yapar
- [ ] Vergi kırılımı taban + oran ayrı (`TaxRateBPS` bunu üretmeye yeterli)
- [ ] Ödeme yöntemi başına toplam; nakitte kasa oturumu bakiyesiyle birleşik
- [ ] Admin dashboard bu uçtan beslensin

## 5. Prod deploy hedefi

`deploy/` altında yalnız `docker-compose.dev.yml` var. K8s ADR'de Faz 2'ye ertelenmiş.

- [ ] Prod compose veya K8s kararı
- [ ] Vault prod yapılandırması (bootstrap ≠ runtime)
- [ ] Yedekleme/DR'ın fiilen kurulması (ADR-OPS-001 yazılı, uygulanmamış)
- [ ] Ölçüm/alarm: reconciler overdue, outbox birikmesi

## 6. `tenant` modülü test açığı

2963 satır kaynak / 323 satır test — repodaki en düşük oran, üstelik kiracı izolasyonunu tutan
modülde. Karşılaştırma: payment 6082/7570, pos 3914/4077.

- [ ] RLS sızıntı ve cross-tenant yazma testleri, `lessons-from-b2b` invariant listesine göre

## 7. İzin wiring testi — seed'li ama bağlanmamış izinler

`000006_seed_system_roles.up.sql` şunları veriyor ama kodda karşılığı **yok**:
- `shift_manager` → `checks:approve`, `orders:approve` — `approve` yalnız `inventory`'de bağlı,
  `pos`'ta 0 çağrı yeri
- `shifts` read/create/update — `backend/internal/` altında hiç geçmiyor

`docs/lessons-from-b2b.md`'nin tam olarak önlemek için yazıldığı hata: *"Casbin RBAC init
ediliyordu → middleware 0 route'a bağlıydı."*

- [x] **CI testi yazıldı** (`fced7bc`, `platform/auth/permission_wiring_test.go`). Doğrulama grep
      değil, gerçek OPA motoru + derlenmiş rego bundle'ı. Üç yönde kırılıyor: sınıflandırılmamış yeni
      seed izni, kapandığı hâlde baseline'da kalan boşluk, ve zorlandığı iddia edilip OPA'da
      reddedilen izin. 13 çift "zorlanıyor", 10 çift gerekçeli baseline'da
- [ ] Yönetici onayı akışı (`checks:approve`) — ikram/iskonto/iptal, sunucuda zorlanan

### Testin ilk gününde bulduğu iki gerçek sapma

Kasa oturumu ve yönetici onayı gibi "henüz yapılmadı" boşluklarından **farklı** bir sınıf: burada
kod ve policy var, ama seed'in verdiği rolle OPA'nın izin verdiği rol **uyuşmuyor**. İkisi de
doğrulandı, ikisi de pilotu bloklamıyor.

- [ ] ⏸️ **Ertelendi (2026-08-01 ürün kararı: şoför/teslimat kapsam dışı).**
      **`driver` rolü hiçbir şey yapamıyor.** Seed `orders:read` + `orders:update` veriyor
      (`000006`, satır 72-73) ama `authz.rego`'da **hiçbir `allow` kuralı** `driver` içermiyor —
      `pos_counter_actions` `{cashier, shift_manager}`, `pos_kitchen_actions` `{kitchen, bar}`.
      Rego'da `driver` yalnız **scope** kuralında (satır 277) geçiyor, o da ancak bir allow
      tetiklendikten sonra devreye giriyor; yani şoför için ölü kod.
      Karar: teslimat akışı geldiğinde allow yazılacak mı, yoksa seed satırları mı düşecek?
- [ ] **`kitchen`/`bar` `inventory:read` alıyor ama kullanamıyor.** Seed veriyor (`000006`,
      satır 81 ve 89); `inventory.level.read` ise `inventory_management_actions` içinde ve
      ADR-DATA-005 İlke 4 gereği manager/warehouse'a kapalı. Burada **seed yanlış görünüyor** —
      ADR bilinçli olarak dar tutuyor. Muhtemel çözüm: seed satırlarını kaldırmak.

Bu madde 3'ten **önce** yapılmalı: kasa oturumu `shifts:*` izinlerini kullanacak, test önce
konursa yanlış izin adı uydurulması engellenir.

## 8. Token gerçek cihaz / sertifikasyon testi

Dış bağımlılık — takvimi bizde değil, şimdiden temas kurulmalı.
Açık teknik sorular `backlog-fiscal.md`'de (tokenx 401 re-auth akışı, `operationDate` timezone).

---

## Kararlar (2026-08-03)

Üç açık ürün sorusu karara bağlandı. Gerekçeler burada; katılmıyorsanız tartışılacak yer burası.

**1. Keycloak davet entegrasyonu → başlıyor.** Pilotun önündeki tek yapısal engel; personel
eklenemeden diğer hiçbir işin anlamı yok. POS/mutfak odağı özellik kapsamına dairdi, altyapı ön
koşulunu düşürmeye değil. ADR-AUTH-003 uygulanıyor.

**2. Kasa açılmadan satış → engelleniyor (yalnız nakit, backend'de).** ✅ Uygulandı (`b1c5691`).
Bu bir tercih değil **doğruluk sorunu**: beklenen kapanış
`opening + SumCompletedCashPayments(branch, session.OpenedAt, session.ClosedAt) + movements`
ile hesaplanıyor. Oturum yokken alınan nakit hiçbir oturum penceresine düşmüyor, yani mutabakata
**hiç girmiyor** — çekmece yapısal olarak tutmuyor.

Kapsam dar tutuldu: kart/ÖKC, yemek kartı, ikram, ödemesiz ve açık hesap çekmeceye dokunmadığı için
engellenmiyor; adisyon açma, sipariş girme, adisyon kapatma da engellenmiyor. Zorlama backend'de
çünkü istemcide bloklamak, sunucuda olmayan bir kuralı uydurmak olur ve ikisi zamanla ayrışır.

Guard, ödeme yazımıyla aynı transaction'da ve idempotency okumasından **sonra** duruyor: öncesinde
olsaydı, oturum açıkken başarılı olmuş bir ödemenin ağ hatası sonrası yeniden denenmesi yanlışlıkla
reddedilirdi. Oturum okuması `FOR SHARE` — düz `SELECT` ile eşzamanlı bir kapanış, guard'ın okuması
ile ödemenin yazımı arasına girip parayı yine görünmez yapabiliyordu. Bu yarış ampirik olarak
üretildi (25 iterasyonun 3-4'ünde) ve düzeltmeden sonra kayboldu; regresyon testi mevcut.

**3. Fark onayı (açık/fazla) → ertelendi, denetim izi yeterli.** Tek kasalı pilotta kasiyer çoğu
zaman işletmecinin kendisi. Dört-göz kuralı kapanışta ikinci bir kişinin hazır bulunmasını şart
koşar; küçük restoranda bu kişi yok ve kural fiilen kasayı kapatılamaz hale getirir. Bugünkü
kontrol: kim açtı, kim saydı, kim kapattı, her hareket aktör ve zaman damgasıyla.
Çok kişili vardiyası olan bir müşteri geldiğinde yeniden bakılır; aday sözlük seed'li ama hâlâ
bağlanmamış `checks:approve`.

---

## Kapsam dışı (2026-08-01 ürün kararı)

Odak **POS + mutfak ekranı**. Aşağıdakiler bilinçli olarak ertelendi; hiçbiri unutulmuş değil.

**Marş sistemi** ([lessons-from-odoo.md § Tier 2 madde 7](lessons-from-odoo.md)) — marşla çalışan
bir restoranla anlaşıldığında yapılacak. Üst segment oturarak servis dışında hiç gerekmiyor ve şube
düzeyinde flag'lenebilir, dolayısıyla o müşteri gelene kadar bekletmenin maliyeti yok.

> Yapılacağı zaman kaçırılmaması gereken para riski: **tetiklenmemiş marş** adisyon toplamına,
> ÖKC sepetine ve stok düşümüne girmemeli. `TestPOSSpine_ClosePaysOnlyForActiveOrders`'ın koruduğu
> hatanın kardeşi — müşteri yemediği tatlıyı ödemek zorunda kalır.

**Şoför / teslimat** — `driver` rolünün OPA sapması dahil (yukarıda madde 7 altında).

**İmalat (`manufacturing`)** — modül 32 satırlık iskelet, ROADMAP'te Faz 3. Parti/SKT takibi
(madde 11'deki `product_expiry` boşluğu) de bu kapsamda bekliyor.

### Mutfak ekranı — mevcut durum

KDS **çalışıyor**, eksik değil: `admin/(main)/pos/kitchen` 4 sütunlu kanban
(`pending → accepted → preparing → ready`), `pos/ws` hub'ı üzerinden canlı WebSocket akışı,
bağlantı durumu rozeti, accept/advance aksiyonları. Marş dışında bilinen bir işlevsel boşluğu yok.
