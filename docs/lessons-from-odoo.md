# Odoo 19 Community İncelemesinden Aktarılan Dersler

> Tarih: 2026-07-31 · Kaynak: `/Users/alpervural/Desktop/odoo-19.0.post20260730` (Odoo 19.0 Community nightly, 684 addon, LGPL-3)
> Odoo ağacının genel yapısı ayrı bir dokümanda: aynı dizindeki `PROJE-INCELEME.md`.
> Bu doküman yalnızca **bizim ürünümüze giren dersleri** taşır; Odoo'nun mimarisini benimseme önerisi değildir.

## Lisans uyarısı (önce bu)

Odoo Community **LGPL-3**. Bir alan listesine, durum makinesine veya "kasa mutabakatı nasıl işler"e bakmak
tasarım referansıdır ve serbesttir — bunlar muhasebe pratiğine ait olgular. Ancak:

- Kod yapısının birebir çevirisi (aynı metot sırası, aynı bölümleme) türev eser tartışması doğurur.
- **En riskli kalem `l10n_tr`'nin hesap planı ve vergi tanımı XML veri dosyalarıdır** — veri kopyalamak
  doğrudan kopyalamadır, "ilham" savunması burada işlemez. Bu dosyalara dokunmadan önce hukuki görüş alın.

Aşağıdaki maddelerin hepsi "Go'da sıfırdan yaz, tasarım kararını referans al" varsayımıyla yazılmıştır.

## Eleme: 684 addon → kısa liste

Klasör adı önekiyle elendi. **İncelenenler:** `point_of_sale`, `pos_*` (42 adet), `stock*`, `account*`,
`l10n_tr*` (6), `mrp*`, `product*`, `purchase*`, `hr_*`, `payment_*`, `uom`, `barcodes*`, `delivery*`.
**Kapsam dışı bırakılanlar:** `website*` (~50), `mass_mailing*`, `hr_recruitment*`, `website_event*`,
`survey`, `im_livechat`, `project*`, `crm*`, `fleet`, `lunch`, `maintenance`, `marketing_card`,
`repair`, `sign` — 684'ün yarısından çoğu ürün kapsamımız dışında.

---

## Tier 1 — Pilot bloklayıcılarına doğrudan girdi

### 1. `pos.session` → bizim eksik kasa/vardiya modelimiz

`point_of_sale/models/pos_session.py` (2002 satır). Pilot iş listemizdeki 3. madde (kasa mutabakatı)
için hazır bir referans tasarım.

**Alınacak yarı — mutabakat.** Dört ayrı para alanı tutuluyor, tek "kapanış tutarı" değil:

| Odoo alanı | Saklanan? | Anlamı |
|---|---|---|
| `cash_register_balance_start` | evet | Açılışta sayılan para |
| `cash_register_balance_end_real` | evet | Kasiyerin **saydığı** kapanış |
| `cash_real_transaction` | evet | Vardiya boyunca gerçekleşen nakit hareketi |
| `cash_register_balance_end` | hayır — `_compute_cash_balance` | Sistemin **beklediği** kapanış = açılış + nakit hareketleri |
| `cash_register_difference` | hayır — `_compute_cash_balance` | `saydığı − beklediği` |

Kritik ayrım: **girdi olan üç değer saklanıyor, türetilen ikisi hesaplanıyor.** "Beklenen" ile
"sayılan"ı tek alana indirirsen açık/fazla denetlenemez; ama farkı saklarsan da girdiler değişince
tutarsız kalır. Not: `_compute_cash_balance` oturum kapandığında formülü değiştiriyor
(`cash_real_transaction` kullanıyor, açıkken `statement_line_ids` topluyor) — kapanış sonrası
raporun sabit kalması için.

**Sayım ekranının veri modeli — `pos_bill.py`.** Banknot/madeni para dökümü ayrı bir kayıt
(`name` + `value`, POS başına `default_bill_ids`). Kasiyer "200'lük ×3, 100'lük ×5" girer, toplam
otomatik çıkar. Elle tek tutar yazdırmaktan hem daha hızlı hem sayım hatasına daha dayanıklı.
Türkiye için kupür listesi sabit; tenant'a bırakmaya gerek yok.

**Kuruş yuvarlama — `account.cash.rounding` + `pos_config`.** `rounding_method` (Many2one),
`cash_rounding` (bool), `only_round_cash_method` (yalnız nakitte yuvarla). Asıl modelde `rounding`
(hassasiyet), `strategy` (`biggest_tax` = vergiyi düzelt | `add_invoice_line` = yuvarlama satırı ekle)
ve **ayrı kâr/zarar hesapları** var. POS'ta yalnız `add_invoice_line` stratejisine izin veriliyor
(`_check_rounding_method_strategy`, `pos_config.py:456`).

Bu bizim fark hesabımızı doğrudan etkiliyor: nakit ödemede kuruş yuvarlaması kaydedilmezse her satış
açık/fazlaya küçük bir hata ekler ve gün sonunda kasiyer sayımı tutmaz.

**Durum makinesi** (`pos_session.py:24-27`) — bizim `allowedTransitions` desenimize birebir oturur:
`opening_control` → `opened` → `closing_control` → `closed`. Kapanış **iki adımlı**: önce sayım ekranı
açılır (`closing_control`), sonra doğrulanır (`closed`). Tek adımlı kapanış sayım hatasını geri alınamaz yapar.

**Alınacak metot davranışları:**
- `_cannot_close_session()` (satır 715) — kapanışı bloklayan koşulları **tek yerde** toplayan guard.
- `get_cash_in_out_list()` (753) — vardiya içi nakit giriş/çıkışı (kasadan para alma, bozuk para koyma).
  Bizde bu kavram hiç yok; onsuz fark hesabı yanlış çıkar.
- `post_closing_cash_details(counted_cash)` (651) — sayım girişi kapanıştan ayrı bir işlem.
- `opening_notes` / `closing_notes` — kasiyerin fark açıklaması; denetim izi için şart.
- **`_alert_old_session()` (1838)** — 7 günden uzun süre açık kalan oturum için sorumluya hatırlatma
  aktivitesi açıyor. **Kapatmıyor, yalnız uyarıyor** — kasiyerin saymadığı bir kasayı sistemin
  kendiliğinden kapatması sayımı imkânsız kılardı. Bizde karşılığı bir asynq job + bildirim olmalı,
  otomatik kapanış değil.
- **`rescue` bayrağı (84) — "Recovery Session".** Çöken/kapatılamayan oturum için kurtarma oturumu.
  Bu bizim *stranded submission + manuel expire ucu* problemimizin aynısı, upstream'de çözülmüş hali.

**Alınmayacak yarı — muhasebe.** Dosyanın kabaca yarısı (`_create_account_move` 869, `_accumulate_amounts`
896, `_create_bank_payment_moves` 1109, `_get_balancing_account` 862, `_create_balancing_line` 840)
kapanışta tek toplu yevmiye fişi üretiyor. Bizde `account` modülü yok, `billing` yalnızca e-fatura.
Bu yarıyı taşımaya kalkmak karşılayamayacağımız bir bağımlılık getirir.

**Kopyalamadan önce verilecek karar — oturum kimin?**
Odoo'da oturum `pos.config` başına, yani **terminal** başına. Bizde durum farklı:
`fiscal-pending` ucu şube geneli, `basket_mode: list` sepeti şubedeki her terminale düşürüyor,
POS istemcisi ise kasiyer başına `serverCompleted` haritası tutuyor. Yani üç aday var:
**terminal / kasiyer / şube.** Alan isimlerini kopyalamadan önce bu kararı vermek gerekiyor —
tablonun birincil anahtarını ve `fiscal_submissions` ile ilişkisini bu belirler.

- [ ] Oturum sahipliği kararı (terminal | kasiyer | şube) — ADR gerektirir
- [ ] `cash_sessions` tablosu + 4 durumlu geçiş makinesi (girdiler saklanır, beklenen/fark türetilir)
- [ ] Vardiya içi nakit giriş/çıkış kaydı
- [ ] Kupür dökümlü sayım ekranı (`pos_bill` karşılığı — TR kupür listesi sabit)
- [ ] **Kuruş yuvarlama** — yalnız nakitte, yuvarlama farkı kayıt altında; fark hesabına giriyor
- [ ] Kapatılamama guard'ı tek fonksiyonda
- [ ] Eski oturum **uyarı** job'ı (asynq) — otomatik kapatma değil
- [ ] Kurtarma oturumu (`rescue`) — stranded submission runbook'uyla birleştir

### 2. `report_sale_details.py` → gün sonu satış özeti

507 satır; pilot listemizdeki 4. madde (dashboard `mockSalesData` → gerçek özet) için doğrudan spec.
`get_sale_details()` (satır 53) çıktısının kırılımı:

- **Satış ve iade ayrı akümülatörlerde** — `products_sold` / `refund_done`, `taxes` / `refund_taxes`.
  Tek toplamda birleştirmek iade oranını görünmez yapar.
- **Vergi kırılımı taban + vergi olarak ayrı** (`base_amount` + oran başına). Bizim `TaxRateBPS`
  alanımız bunu üretmeye yeterli; rapor tarafı yok.
- **Ödeme yöntemi başına toplam**, ve nakit için (satır 198):
  `final_count = total + cash_register_balance_start + cash_real_transaction`
  Yani gün sonu raporu kasa oturumuna **bağımlı**. Sıralama netleşiyor: önce madde 1, sonra bu.
- Ürün kırılımı kategori bazında gruplu, `price_unit` ve `discount` ayrı boyut.

- [ ] `GET /api/v1/pos/reports/sale-details` — tarih aralığı + şube + oturum filtresi
- [ ] Satış/iade ayrı, vergi taban+oran kırılımlı, ödeme yöntemi bazlı
- [ ] Admin dashboard'daki `mockSalesData` bu uçtan beslensin

### 3. UBL-TR: iskonto düğümlerini doğrula

Bizim `billing/adapter/edm/ubl.go` 229 satır; Odoo'nun TR'ye özgü katmanı
(`l10n_tr_nilvera_einvoice/models/account_edi_xml_ubl_tr.py`) tam UBL 2.1 tabanının **üstünde** 332 satır.
Fark büyük görünüyor ama çoğu bizi pilotta ilgilendirmiyor. Ayıralım:

**Pilot için gerçek olan:**
- `_add_document_allowance_charge_nodes` (155) ve `_add_document_line_allowance_charge_nodes` (289) —
  **iskonto/indirim düğümleri**, hem fatura hem satır seviyesinde. Restoranda ikram/indirim günlük
  vaka; bizim `PaymentMethodComp` ve indirimlerin faturaya nasıl yansıdığı doğrulanmalı.

**Faz 2 (yalnız TRY dışı faturada tetikleniyor):**
- `YALNIZ : ... TL` yazıyla tutar notu (`_l10n_tr_get_amount_integer_partn_text_note`, 100)
- `cac:PricingExchangeRate` + `KUR : x.xxxxxx TL` notu (136)
- `cac:Delivery` / `ActualDeliveryDate` (109) — irsaliye bağı

Not: bunların bir kısmını EDM sağlayıcısı kendi tarafında ekliyor olabilir. Madde "eksik" değil,
**"doğrula"** olarak açılmalı.

- [ ] İskonto düğümlerinin (fatura + satır) EDM çıktısında yer aldığını doğrula
- [ ] `comp` / `no_charge` ödemelerin faturaya yansıması netleştirilsin

### 4. Personel kimliği ve PIN — Odoo'nun yaptığı, bizim yapmayacağımız

`pos_hr` addon'u restoran gerçeğine cevap veriyor: **bir kasada onlarca kasiyer vardiya boyunca
sırayla çalışır ve her değişimde tam oturum açma yapılamaz.** Sorunun kendisi gerçek; verilen
cevabın uygulaması ise baştan sona yanlış yerde.

**Odoo'nun modeli.** Manifest bunu açıkça yazıyor: *"The actual till still requires one user but an
unlimited number of employees can log on to that till and process sales."* Yani sunucu tarafında
kasadan çıkan **her sipariş tek bir `res.users` principal'ı** tarafından oluşturuluyor.
`pos.order.employee_id` yalnızca bir veri sütunu — o çalışanın gerçekten yetkilendirdiğini
doğrulayan sunucu tarafı bir kontrol yok. Personel katmanı bir **UX kolaylığı, yetki sınırı değil.**

**Dört somut anti-pattern** (hepsi kodda doğrulandı):

1. **PIN doğrulaması istemcide.** `pos_hr/static/src/app/utils/select_cashier_mixin.js:43,50,80,104` →
   `employee._pin !== Sha1.hash(inputPin)`. Karşılaştırma tarayıcıda. Değiştirilmiş bir istemci
   kontrolü tamamen atlar.
2. **Tuzsuz SHA-1 + 4 haneli sayısal alan + istemciye indirme.** `get_barcodes_and_pin_hashed()`
   görünür **tüm** çalışanların `sha1(pin)` değerini POS'a gönderiyor. Üç olgu tek başına masum
   görünür, birlikte kırılmayı önemsizleştirir: 10.000 aday, tuz yok, hızlı hash, ve saldırgan
   hash'lerin tamamına sahip. Bu "hash'lenmiş" değil, **gizlenmiş**.
3. **`sudo()` alan-seviyesi korumasını dolaşıyor.** `hr/models/hr_employee.py:209` PIN alanını
   `groups="hr.group_hr_user"` ile koruyor; `pos_hr` ise `self.sudo().search_read(...)` ile okuyor.
   Koruma var ve etrafından dolaşılıyor — `lessons-from-b2b`'deki "kural var ama zorlanmıyor"un
   birebir aynısı.
4. **Yetki kararı istemcide.** `pos_store.js:1370` →
   `!config.restrict_price_control || getCashier()._role == "manager"`. Fiyat/iskonto override'ı,
   istemcinin elindeki bir etikete bakılarak veriliyor.

**Bizim durumumuz — mimari olarak daha güçlü, UX olarak cevapsız.** POS istemcimiz Keycloak tarayıcı
akışıyla giriş yapıyor (`LoginScreen.tsx`), token OS anahtarlığında (`internal/tokenstore/store.go`),
her kasiyer gerçek bir principal. Odoo'nun 1-4 numaralı hatalarının hiçbiri bizde mümkün değil.
**Ama kasiyer değişimi için hâlâ tam Keycloak akışı gerekiyor** — yoğun serviste kullanılamaz.
Odoo doğru soruyu yanlış cevaplamış; biz doğru mimariye sahibiz ama soruyu henüz cevaplamadık.

**Alınacak şekil:** PIN, **zaten kimliği doğrulanmış bir principal'ın hızlı yeniden seçimi** olsun;
tek başına yetki veren bir kimlik bilgisi asla olmasın. Somut olarak:

- PIN sunucuda doğrulanır, istemciye hiçbir biçimde (hash dahil) inmez.
- Doğrulama mevcut `/auth/context` akışına bağlanır ve kısa ömürlü bir context token üretir.
- Her mutasyonda kasiyer kimliği **gerçek principal olarak** taşınır, istemcinin iliştirdiği etiket
  olarak değil.
- Rol bilgisi istemcide yalnızca **görüntü ipucu** olarak bulunabilir (butonu gizlemek için);
  karar her zaman sunucuda OPA katmanında verilir.
- Saklama: yavaş + tuzlu KDF (argon2id/bcrypt) ve **kaba kuvvete karşı hız sınırı** — 4-6 hane
  küçük bir uzaydır, asıl savunma deneme sayısıdır. Kilitlenme olayları denetim izine yazılır.

**Bu karar kasa oturumundan ayrı verilemez.** Oturumun `opening_notes`/`closing_notes`'u ve fark
açıklaması, arkasındaki kasiyer kimliği sunucuda doğrulanmıyorsa hiçbir şey ifade etmez. Oturum
sahipliği (terminal | kasiyer | şube) ile kasiyer kimlik doğrulaması **tek bir ADR'de** birlikte
kararlaştırılmalı.

- [ ] ADR: kasa oturumu sahipliği + kasiyer PIN doğrulaması (tek karar)
- [ ] PIN yalnız sunucuda; argon2id + deneme hız sınırı + kilitlenme denetim izi
- [ ] İstemcideki rol bilgisi yalnız görünüm; her override sunucuda `permit(...)`'ten geçer

### 5. Kendi bulgumuz: seed edilmiş ama hiçbir yere bağlanmamış izinler

Odoo'ya bakarken bizim tarafta doğruladım — `lessons-from-b2b`'nin tam olarak önlemek için yazıldığı
hata bu repoda **şu an mevcut**:

`000006_seed_system_roles.up.sql` şunları seed ediyor:
- `shift_manager` → `checks:approve` ve `orders:approve`
- `shift_manager` → `shifts` read/create/update (kasiyerde de `shifts:read`)

Kodda ise:
- `approve` fiili yalnız `inventory` modülünde bağlı (`inventory.transfer_order.approve`,
  `inventory.shipment.advance`). **`pos` modülünde 0 çağrı yeri.**
- `shifts` kaynağı `backend/internal/` altında **hiç geçmiyor**.

b2b dersi birebir tekrarlıyor: *"Casbin RBAC init ediliyordu → `RequirePermission` middleware'i
0 route'a bağlıydı."* Yönetici onayı (ikram/iskonto/iptal için) ve kasa vardiyası izin sözlüğü
tanımlı, hiçbir yerde zorlanmıyor.

İki sonucu var:
1. **Kasa oturumunu yazarken yeni izin adı uydurmayın** — `shifts:create/update` zaten seed'li.
   Aynı şekilde yönetici onayı için `checks:approve` / `orders:approve` hazır.
2. **Asıl teslimat bir test:** `role_permissions` içindeki her satırın kodda karşılık gelen bir
   `permit(...)` çağrı yeri olduğunu doğrulayan, olmayanda **kırılan** bir CI testi. Not değil, test.

- [ ] CI testi: seed'li her `(resource, action)` için en az bir `permit(...)` çağrı yeri
- [ ] Yönetici onayı akışı (`checks:approve`) — ikram/iskonto/iptal, sunucuda zorlanan
- [ ] Kasa oturumu izinleri mevcut `shifts:*` sözlüğünü kullansın

---

## Tier 2 — Faz 2 mimari kararları

### 6. Offline modeli: Odoo edge server kullanmıyor

Odoo POS'un tüm offline hikâyesi **istemci tarafında**:
`static/src/app/services/data_service.js` (1040 satır) + `models/utils/indexed_db.js` (446) +
`utils/devices_synchronisation.js` (245) ≈ **1730 satır**, ayrı bir sunucu süreci yok.
`checkConnectivity`, `synchronizeLocalDataInIndexedDB`, `syncData`, `checkAndDeleteMissingOrders`
ve ORM çağrılarındaki `queue` bayrağı bu dosyalarda.

Bizim ADR-DATA-004 ise `cmd/edge` + SQLite + gömülü NATS'e bağlanıyor ve bu iş 6–10 hafta tahmin edildi.

**Ayırıcı soru — şube offline'ken birden fazla terminal canlı ortak durum paylaşmak zorunda mı?**

- **Evet ise** edge server doğru karar ve Odoo'nun modeli bize uymuyor. Bizde iki sinyal bunu söylüyor:
  KDS WebSocket hub'ı (mutfak ekranı ile kasa aynı anda aynı siparişi görmeli) ve `basket_mode: list`
  (sepet şubedeki her terminale düşüyor). Yani mevcut ADR muhtemelen **doğrulanıyor** — bu da bir bulgu.
- **Hayır ise** (tek kasalı pilot şekli) istemci-taraflı offline dramatik biçimde daha ucuz.
  Wails POS'ta zaten yerel bir Go süreci var; SQLite'ı oraya gömmek ayrı binary'den basit.

ADR'yi yeniden açma önerisi değil; edge-sync'e başlamadan önce bu sorunun yazılı cevabı olmalı.

- [ ] "Offline'da çok terminal ortak durum paylaşır mı?" sorusunu ADR-DATA-004'e ek olarak yanıtla

### 7. Marş sistemi — Odoo'nun "course"u, Türkiye'de zaten var olan bir pratik

> **Terminoloji notu:** Odoo buna `course` diyor. Türkiye'de üst segment restoranlarda bu pratiğin
> yerleşik adı **"marş"**. Kod içi tanımlayıcılar İngilizce kalır, ama ürün dili, admin paneli ve
> POS/KDS arayüzü **marş** demeli — garson "1. marş, 2. marş" diye konuşur, "kurs" demez.

**İşleyiş (saha gerçeği).** Garson masaya oturan müşteriden **siparişin tamamını baştan alır** —
çorba, ana yemek, tatlı. Ama mutfağa hepsi birden gitmez. Sipariş marş gruplarına yazılır
(1. marş = çorbalar, 2. marş = ana yemekler, 3. marş = tatlı) ve garson **masanın durumuna bakarak**
sırayla marşa basar:

1. 1. marş tetiklenir → çorbalar hazırlanır, servis edilir.
2. Çorbalar masaya gelince garson **2. marşa basar** → müşteri çorbasını içerken ana yemek hazırlanır.
3. Ana yemekler bitmeye yakınken garson **3. marşa basar** → tatlı, müşteri beklemeden gelir.

Amaç hazırlık süresini yeme süresiyle örtüştürmek: müşteri masada boş beklemesin, yemek de erken
çıkıp soğumasın. Tetikleme zamanı **garsonun masayı okumasına** bağlı — sabit süre veya otomasyon değil.

**Ürün kararı:** Bu bir restoran kültürü, **zorunlu değil.** Fast-food, kafe, paket servis ve food
truck'ta hiç kullanılmaz. Dolayısıyla şube/işletme düzeyinde **açılıp kapanabilir bir özellik** olmalı,
varsayılanı kapalı; açık olmadığında sipariş bugünkü gibi tek parça mutfağa düşer.

**Bizdeki durum.** `pos` modelinde sipariş `pending → accepted → preparing → ready` olarak bir bütün;
marş kavramı yok. Marş açık bir şubede tatlı ana yemekle birlikte mutfağa düşer — yani özellik yokken
o segmentte ürün kullanılamaz.

**Odoo'nun veri modelinden alınacak iki şey** (`restaurant_order_course.py`):
- Marş grubunun kendi kaydı var; sipariş satırları gruba bağlanıyor (`line_ids`), sipariş satırında
  "kaçıncı marş" diye bir sayı taşınmıyor. Grup ayrı kayıt olunca "2. marşı 3.'den önce tetikle"
  gibi sıralama ve "marşa basıldı mı" durumu tek yerde yaşar.
- `fired` + `fired_date` ayrı tutuluyor: **tetiklendi mi** ve **ne zaman tetiklendi**. İkincisi
  mutfak performans ölçümü için gerekli (marşa basıldıktan kaç dakika sonra `ready` oldu).
- Grubun kendi `uuid`'si var — offline üretilen kimlik. Bizim outbox/idempotency desenimizle uyumlu.

**⚠️ Kritik: son marş hiç tetiklenmeyebilir — ve bu bir para hatası kaynağı.**

Müşteri tatlıdan vazgeçip kalkabilir. Yani sipariş kaydında **hiç mutfağa düşmemiş, hiç üretilmemiş,
hiç servis edilmemiş** satırlar kalır. Bu satırlar üç yerden **çıkmak zorunda**:

| Yer | Tetiklenmemiş marş ne olmalı | Olmazsa sonuç |
|---|---|---|
| Adisyon toplamı / `CloseCheck` | Sayılmaz | **Müşteri yemediği tatlıyı öder** veya adisyon kapanmaz |
| ÖKC mali sepeti | Gitmez | Yasal fişte satılmamış ürün — yanlış vergi matrahı |
| Stok düşümü (`inventory`) | Düşmez | Fiilen tüketilmemiş malzeme stoktan iner |

Bu tam olarak `TestPOSSpine_ClosePaysOnlyForActiveOrders`'ın koruduğu hatanın kardeşi: orada
**reddedilen sipariş** toplama sayılıyordu, burada **tetiklenmemiş marş**. Aynı sınıf, aynı regresyon
testi disiplini gerekiyor.

**Tasarım sonucu.** Sipariş ve tüm satırları **baştan kaydedilir** — garsonun tamamını alması marşın
bütün amacı, mutfak da neyin geleceğini görmeli. Marş grubu mutfağa **sevkiyatı** kapılar, kaydı değil.
Dolayısıyla grubun kendi durumu olmalı:

`pending` (alındı, tetiklenmedi) → `fired` (mutfağa düştü) → `served`
&nbsp;&nbsp;&nbsp;&nbsp;`pending` → `cancelled` (müşteri vazgeçti)

ve **para/stok/fiscal yollarının hepsi bu duruma göre filtrelemeli.** Varsayılan güvenli yön:
tetiklenmemiş marş **hiçbir toplama girmez**; tetiklenmiş bir marşın iptali ise ayrı bir karar
(üretilmiş olabilir — ikram mı, zayi mi?).

**Adisyon kapatılırken:** `pending` marş varsa sessizce yok sayma — kasiyere göster ve açık bir
"iptal" aksiyonu iste (kim, ne zaman, hangi tutar — denetim izi). Sessiz düşürme, garsonun yanlışlıkla
tetiklemeyi unuttuğu bir marşı da sessizce siler; o zaman mutfak yaptı, müşteri yedi, kimse ödemedi.

**Bizim eklememiz gereken, Odoo'da olmayan:** marşa basma **ve iptal etme** yetkisi kimde? Garson mu,
shift müdürü mü? Tetiklenmiş bir marşın iptali muhtemelen `checks:approve` seviyesinde olmalı
(üretilmiş yemek zayi olur), tetiklenmemişin iptali garsonda kalabilir. Madde 5'teki "seed'li ama
bağlanmamış izin" tuzağına düşmemek için bu baştan netleşmeli.

- [ ] Marş özelliği şube düzeyinde feature flag (varsayılan kapalı) — ADR-ARCH-001 iki katmanlı flag
- [ ] `order_courses` tablosu + durum makinesi (`pending → fired → served`, `pending → cancelled`)
- [ ] **Para yolu:** `CloseCheck` / adisyon toplamı yalnız `fired`+ marşları sayar — regresyon testi
      `TestPOSSpine_ClosePaysOnlyForActiveOrders` desenine birebir
- [ ] **Fiscal yolu:** ÖKC sepetine tetiklenmemiş marş satırı girmez
- [ ] **Stok yolu:** tetiklenmemiş marş stoktan düşmez
- [ ] Adisyon kapanışında `pending` marş → kasiyere göster, açık iptal aksiyonu + denetim izi
- [ ] KDS'te marş bazlı görünüm; tetiklenmemiş marş mutfağa düşmez
- [ ] POS'ta "marşa bas" aksiyonu + yetki kararı (tetikleme vs. tetiklenmiş marşı iptal ayrı seviye)
- [ ] Marş → `ready` süresi metriği (mutfak performansı)

### 8. `restaurant.floor` / `restaurant.table` — masa planı karşılaştırması

Bizde `table_zones` + `tables` var ve konum `layout_position JSONB` içinde. Odoo'da ayrı alanlar:
`position_h`, `position_v`, `width`, `height`, `shape` (square|round), `seats`, `color`.
JSONB tercihimiz esnek; ama iki alan bizde **yok gibi** ve ürün açısından önemli:

- `seats` — masa kapasitesi (bizim `Check.Pax` var ama masanın kendi kapasitesi yok)
- **`parent_id` — masa birleştirme.** İki masayı tek adisyona bağlama; kalabalık grupta günlük vaka.

- [ ] `tables.seats` ve masa birleştirme (`parent_id`) ihtiyacını ürün tarafında teyit et

### 9. `pos_preset` — sipariş tipi bizim `OrderChannel`'dan zengin

Bizde `dine_in | takeaway | delivery` sabit bir enum. Odoo'da preset ayrı bir kayıt ve şunları taşıyor:
`pricelist_id` (tipe göre farklı fiyat listesi — paket serviste farklı fiyat), `fiscal_position_id`
(tipe göre farklı vergi konumu), `identification` (isim/adres zorunluluğu), `is_return` (iade modu),
ve zaman yönetimi: `use_timing`, `slots_per_interval`, `interval_time` (ön sipariş/randevu slotu).

Fiyat ve verginin sipariş tipine bağlanması Türkiye'de de geçerli (paket serviste farklı KDV oranı
tartışması). Enum yerine kayıt olması ileride migration'sız genişleme sağlıyor.

- [ ] `OrderChannel` enum mu kalsın, tenant'ın tanımlayabildiği bir kayda mı dönsün — karar

### 10. `pos_self_order` — ürün adımızın tam karşılığı

QR ile müşterinin kendi telefonundan sipariş vermesi. `pos_self_order/models/` altında kendi
`pos_config`, `pos_order`, `pos_payment_method`, `pos_preset` uzantıları var; `ir_http.py` ile
auth'suz public route yüzeyi. Ürün adımız "onlinemenu.tr" olduğuna göre bu yol haritasında bir yerde.
İncelenecek asıl kısım **auth'suz yüzeyin nasıl daraltıldığı** — bizim ADR-SEC-004 ve
`lessons-from-b2b` maddesi "public endpoint'ler kendi dar query yolunu kullansın" ile aynı problem.

- [ ] QR self-servis sipariş yol haritada nereye giriyor — Faz kararı

---

### 11. İşleyiş tiplerine göre boşluk taraması

ROADMAP tenant modelinde `restoran | bar | market | food_truck | imalat | depo` var. Odoo'nun ilgili
addon aileleriyle karşılaştırdım — ikisi bizde zaten var, biri yok:

| İhtiyaç | Odoo | Bizde | Durum |
|---|---|---|---|
| Barkod okuma (market) | `barcodes`, `barcodes_gs1_nomenclature`, `barcode_rule` | `products.barcode` + `products_barcode_idx` | ✅ Şema var. Eksik olan **POS istemcisinde okuyucu entegrasyonu** — `barcode_reader_service.js` referans |
| Ölçü birimi (market, imalat) | `uom`, `product_uom` | `products.unit`, `stock_items.canonical_unit` + `unit_conversions` (ADR-DATA-005) | ✅ Var, üstelik daha disiplinli: tek kanonik birim, dönüşüm yalnız yazmada |
| SKT / parti takibi | `product_expiry`, `mrp_product_expiry`, `sale_stock_product_expiry` | — | ❌ **Yok.** Markette ve imalatta zorunlu; restoranda soğuk zincir için de gerekebilir |

`unit_conversions` tasarımımız Odoo'nun UoM kategorilerinden sade ve bilinçli bir tercih (ADR-DATA-005
İlke 2: paralel `order_unit`/`sale_unit` ailesi yok). Odoo'ya bakıp bunu genişletme dürtüsüne
direnmek gerekir; sadelik burada avantaj.

- [ ] Parti/SKT takibi — market ve `imalat` işleyiş tipleri satışa açılmadan önce (Faz 2/3)
- [ ] POS istemcisinde barkod okuyucu (market pilotu gündeme gelirse)

---

## Tier 3 — Sonra bakılacak, şimdi değil

| Addon | Ne için |
|---|---|
| `pos_hr`, `pos_hr/single_employee_sales_report.py` | Kasiyer bazlı satış raporu, personel-POS bağı |
| `pos_loyalty`, `pos_discount`, `sale_loyalty` | Sadakat puanı + indirim kuralları (Faz 3 CRM) |
| `pos_cashdro`, `pos_cashmatic`, `pos_glory_cash` | Otomatik para makinesi entegrasyonu (kurumsal zincir) |
| `pos_sale`, `pos_sale_margin` | POS ↔ satış siparişi köprüsü, kâr marjı |
| `pos_mrp`, `mrp*` | Üretim/reçete — bizim `manufacturing` iskeleti için Faz 3 |
| `stock*` (~15 addon) | Depo/stok — bizim `inventory` zaten en büyük modülümüz, karşılaştırmalı okuma |
| `l10n_tr_nilvera_edispatch` | e-İrsaliye — Faz 2 |
| `l10n_tr_nilvera_base_vat` | VKN/TCKN doğrulama kuralları |
| `payment_iyzico` | İyzico entegrasyon deseni (PayTR yerine/yanında, Faz 2) |

**Bakılmayacaklar:** `website*` (~50 addon), `mass_mailing*`, `hr_recruitment*`, `website_event*`,
`survey`, `im_livechat` — 684 addon'un yarısından çoğu bizim ürün kapsamımız dışında.

---

## Pilot iş listesine etkisi

Önceki 8 maddelik pilot listesinde iki değişiklik:

- **Madde 3 (kasa mutabakatı)** artık sıfırdan tasarım değil; referans tasarım var ve bir ön karar
  gerektiriyor (oturum sahipliği). Riski düşüyor ama **kapsamı büyüyor**: kupür dökümlü sayım ekranı
  ve kuruş yuvarlama ilk tahmine dahil değildi. 5–8 gün yerine 7–10 gün beklemek daha gerçekçi.
- **Madde 4 (gün sonu raporu) madde 3'e bağımlı** — nakit satırının `final_count`'u kasa oturumunun
  açılış bakiyesini kullanıyor. Sıra: önce oturum, sonra rapor. Paralel planlanamaz.

Yeni aday madde: **marş sistemi** (madde 7). Pilot müşterinin segmentine bağlı —
üst segment, oturarak servis veren bir restoransa **bloklayıcı**; fast-food, kafe, paket servis veya
food truck ağırlıklı bir pilotta hiç gerekmez. Özellik şube düzeyinde flag'lenebilir olduğu için
pilotu seçtikten sonra karara bağlanabilir, şimdi bloklamaz.
