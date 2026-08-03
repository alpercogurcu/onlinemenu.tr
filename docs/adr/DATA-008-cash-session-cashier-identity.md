# ADR-DATA-008: Kasa Oturumu Sahipliği ve Kasiyer Kimlik Doğrulama

**Durum:** ✅ Kabul edildi (MVP kapsamı — kısıtı okumadan uygulamayın)
**Tarih:** 2026-07-31
**Kategori:** Veri / Event (DATA)
**İlgili:** SEC-004 (Cihaz Pairing — **Taslak, uygulanmadı**), AUTH-001 (4 katmanlı authorization),
AUTH-002 (Keycloak tek realm), FISCAL-002 (asenkron fiscal adapter), SEC-005 (şube-kapsamlı roller),
`docs/lessons-from-odoo.md` (Tier 1 madde 1 ve 4), `docs/backlog-pilot.md` madde 3

---

## Bağlam

POS'ta iki eksik var ve **birbirinden ayrı karara bağlanamazlar**:

1. **Kasa mutabakatı yok.** `pos/domain` yalnız check/order/table tanımlıyor; vardiya açılış-kapanış,
   sayım, açık/fazla kavramı hiç bulunmuyor. İşletme parasını takip edemiyor.
2. **Kasiyer değişimi pratik değil.** Her kasiyer gerçek bir Keycloak principal'ı — mimari olarak
   doğru, ama vardiya boyunca onlarca kez tam tarayıcı oturum akışı koşturmak yoğun serviste
   kullanılamaz.

Neden ayrılamaz: oturumun `opening_notes`/`closing_notes` alanları ve fark açıklaması, arkasındaki
kasiyer kimliği sunucuda doğrulanmıyorsa **hiçbir şey ifade etmez.** Kim saydı, kim açığı imzaladı
sorusunun cevabı yoksa mutabakat bir denetim aracı değil, süslemedir.

---

## Karar 1 — Oturum **şube** başına

Kasa oturumu `branch_id`'ye bağlanır. Aynı anda bir şubede **en fazla bir açık oturum** bulunur
(kısmi unique index).

### Neden diğer iki aday elendi

| Aday | Neden olmuyor |
|---|---|
| **Kasiyer başına** | İki kasiyer aynı çekmeceyi paylaşınca aynı fiziksel parayı iki kez sayarlar. Mutabakatın öznesi kişi değil, **fiziksel çekmecedir**. Kişi bazlı hesap verebilirlik zaten ödeme kaydındaki gerçek principal'dan geliyor — oturumu bölmeye gerek yok. |
| **Terminal/istasyon başına** | Doğru cevap bu, **ama bugün uygulanamaz**: POS istasyon kimliği yok. ADR-SEC-004 hâlâ Taslak ("İmplementasyon Detayları (Dolacak)"), `devices` tablosu yok. Elimizdeki tek "terminal" `fiscal_terminals` (ÖKC) ve o uygun değil: `basket_mode: list` ile **bir ÖKC şubedeki her kasaya** hizmet ediyor, üstelik ÖKC bir para çekmecesi değil. |

Odoo bu oturumu `pos.config` yani terminal başına tutuyor ve fiziksel çekmece gerekçesiyle haklı.
Biz aynı gerekçeyle terminal isterdik; istasyon kimliği olmadığı için şubeye düşüyoruz.

### ⚠️ Kabul edilen kısıt — pilot seçimini bağlar

**Bu karar, şubede tek para çekmecesi olduğu sürece doğrudur.** İki kasalı bir şubede iki fiziksel
çekmece tek sayıma indirgenir ve mutabakat anlamsızlaşır — açık bir kasadaki fazla, diğerindeki açığı
gizler.

Dolayısıyla **pilot müşteri seçim kriterine "tek kasa" eklenir** (`docs/backlog-pilot.md`).
Çok kasalı şube, istasyon kimliği gelene kadar desteklenmez ve bu ürün tarafında böyle
iletilmelidir — sessizce yanlış rapor üretmektense desteklenmediğini söylemek yeğdir.

### Yükseltme yolu (geri dönülemez değil)

SEC-004 hayata geçtiğinde:
1. `cash_sessions`'a nullable `station_id` eklenir,
2. Mevcut satırlar şubenin tek istasyonuna backfill edilir,
3. Kısmi unique index `(branch_id)` yerine `(station_id)` üzerine alınır.

Nullable kolon ekleme + backfill + index değişimi olağan ve güvenli bir migration'dır. Bu yüzden
MVP'de şube seçmek **düşük pişmanlıklı** bir karardır; yanlış çıkarsa maliyeti bir migration'dır.

---

## Karar 2 — PIN, kimlik bilgisi değil; doğrulanmış principal'ın hızlı yeniden seçimi

`docs/lessons-from-odoo.md` Tier 1 madde 4'te Odoo'nun `pos_hr` modelinin neden yanlış olduğu
ayrıntılı: PIN tarayıcıda doğrulanıyor, tuzsuz SHA-1 hash'leri tüm çalışanlar için istemciye
iniyor, `sudo()` alan korumasını dolaşıyor, yetki kararı istemcideki etikete bakıyor. Sorun gerçek,
cevap yanlış yerde.

Bizim modelimiz:

1. **Vardiya başında kasiyerler bir kez tam Keycloak akışıyla oturuma katılır.** Bu, o şubedeki
   kasa oturumuna bağlı bir "aktif kasiyer" kaydı üretir.
2. **Sonraki geçişler PIN ile.** PIN **yalnızca sunucuda** doğrulanır; istemciye hash olarak bile
   inmez. Doğrulama başarılıysa mevcut `/auth/context` akışı kısa ömürlü bir context token üretir.
3. **Her mutasyonda kasiyer kimliği gerçek principal olarak taşınır** — istemcinin iliştirdiği bir
   etiket olarak değil. `pos.order.employee_id` benzeri "veri sütunu" yaklaşımı reddedilmiştir.
4. **İstemcideki rol bilgisi yalnız görüntü ipucudur** (butonu gizlemek için). Her yetki kararı
   sunucuda ADR-AUTH-001 zincirinden geçer.
5. **Saklama:** argon2id, kullanıcı başına tuz. PIN 4-6 hane olduğu için anahtar uzayı küçüktür —
   **asıl savunma deneme sayısıdır**: kasiyer başına hız sınırı, ardışık hatada kilitlenme,
   kilitlenme olayı denetim izine yazılır.
6. PIN, tek başına hiçbir yetki vermez: yalnızca **o kasa oturumuna zaten katılmış** bir kasiyeri
   seçebilir. Vardiyaya katılmamış birinin PIN'i doğru olsa bile bir şey açmaz.

---

## Modül sahipliği — `payment`, `pos` değil

Mutabakat implementasyonuna başlarken karara bağlanan bir soru: `cash_sessions` /
`cash_movements` hangi modülde yaşar?

**Karar: `payment`.**

Gerekçe:

- Oturumun tüm içeriği para: beklenen kapanış = açılış + alınan nakit ödemeler +
  kasa hareketleri. Nakit ödemeler zaten `payments` tablosunda (`payment`
  modülü); bu veriyi `pos` modülünden okumak modül sınırını (yalnızca `public/`
  üzerinden erişim) ihlal ederdi.
- Kapatma guard'ı (`cannotClose`) şubenin bekleyen fiscal submission'ı olup
  olmadığına bakar — bu sorgu zaten `payment/repo.FiscalStatusRepo` içinde var
  (branch-wide fiscal-pending ucu ve reconciler onu kullanıyor). `pos`'a
  koymak bu sorguyu tekrar yazmayı ya da `payment`'ta iki yeni `public/`
  arayüzü (fiscal-pending sorgusu + nakit ödeme toplamı) açmayı gerektirirdi —
  kazanç olmadan iki yeni cross-module sözleşme.
- `pos` modülü zaten `payment.public.SaleReader` üzerinden `payment`'a
  bağımlı (check kapatma akışı `TotalPaidForCheck`/`PendingTotalForCheck`
  çağırıyor). Kasa oturumunu `payment`'a koymak bu bağımlılık yönünü
  değiştirmiyor, sadece parayla ilgili tüm mantığı tek modülde topluyor.

`pos` tarafında değişen tek şey: POS istemcisi kasa oturumu uçlarını
`/api/v1/payments/cash-sessions/*` altında çağırır (check/order akışlarıyla
aynı şekilde `payment`'a HTTP üzerinden gider, modül sınırı ihlali yok).

## İzin sözlüğü — yeni ad uydurulmayacak

`000006_seed_system_roles` zaten `shifts` read/create/update izinlerini seed ediyor
(`shift_manager`'da üçü, `cashier`'da `read`). Bu iş **o sözlüğü kullanır**; yeni izin adı
üretilmez.

Aynı seed `checks:approve` ve `orders:approve` da veriyor ve ikisi de bugün hiçbir yere bağlı değil.
Yönetici onayı gerektiren kasa aksiyonları (fark onayı, kasadan para çıkışı) bu izinlere bağlanır.

> Bu maddenin bekçisi ayrı bir CI testidir (`docs/backlog-pilot.md` madde 7): seed'li her
> `(resource, action)` için kodda bir çağrı yeri olmalı. Test **bu ADR'nin implementasyonundan önce**
> devreye girer, ki kasa işi yanlış ad üretmesin.

---

## Sonuçlar

**Olumlu**
- Mutabakatın öznesi fiziksel çekmece; sayım denetlenebilir.
- Kasiyer kimliği sunucuda doğrulanmış gerçek principal — denetim izi anlamlı.
- Odoo'nun dört PIN anti-pattern'inin hiçbiri mümkün değil.
- Mevcut `/auth/context` akışı yeniden kullanılıyor; paralel bir kimlik yolu açılmıyor.

**Olumsuz / maliyet**
- **Çok kasalı şube desteklenmiyor** — pilot seçimini kısıtlar, ürün tarafında açıkça iletilmeli.
- Vardiya başında her kasiyer için bir kez tam Keycloak akışı gerekiyor.
- PIN yolu yeni bir saldırı yüzeyi; hız sınırı ve kilitlenme olmadan devreye alınmamalı.
- SEC-004 geldiğinde bir migration borcu doğuyor (bilinçli, planlı).

---

## PIN akışının ayrıntıları (2026-08-03 eki)

Karar 2 modeli veriyordu ama dört noktayı açık bırakmıştı. Implementasyondan önce karara bağlandı,
çünkü dördü de güvenlik kararı ve tahmine bırakılamaz.

### 1. PIN'in kapsamı: `(person, tenant)`

Ne kişi başına ne membership başına.

- **Kişi başına olamaz:** tek realm kullanıyoruz (AUTH-002), aynı kişi iki işletmede çalışabiliyor.
  Kişi başına tek PIN, A işletmesinde belirlenen PIN'in B işletmesinde de çalışması demekti.
- **Membership başına olamaz:** aynı tenant'ta iki şubede görevli bir kişi iki ayrı PIN taşırdı;
  kasiyer için anlamsız, unutma kaynağı.

Tablo `identity` modülünde, tenant-kapsamlı, FORCE RLS ile.

### 2. PIN'i kasiyerin kendisi belirler; yönetici yalnız sıfırlar

Kasiyer, vardiyaya katıldığı anda (o anda zaten Keycloak ile kimliği doğrulanmış — güvenilir an)
kendi PIN'ini belirler. Yönetici PIN'i **okuyamaz ve belirleyemez**, yalnız **sıfırlayabilir**
(kayıt silinir, kasiyer bir sonraki katılımında yeniden belirler).

Gerekçe hesap verebilirlik: PIN'i yönetici koyarsa, kasiyerin adına yapılmış bir işlemin gerçekten
kasiyer tarafından yapıldığı savunulamaz. Mutabakat farkının altındaki imzanın anlamı buna bağlı.

### 3. Değişim = listeden isim seç + PIN gir

Odoo PIN'i bir **arama anahtarı** gibi kullanıyor (girilen PIN kime aitse o seçiliyor). Biz
kullanmıyoruz:

- Aynı PIN'e sahip iki kasiyer sorunu doğmuyor — çakışma yönetmek gerekmiyor.
- Daha önemlisi: PIN bir **kimlik belirteci değil, seçilmiş kimliğe ikinci faktör** oluyor. Tahmin
  edilen bir PIN tek başına kimin olduğunu söylemiyor.

Listede yalnız **o oturuma katılmış** kasiyerler görünür.

### 4. Katılım ve token ömrü

Bir kasiyerin PIN'le seçilebilmesi için o kasa oturumuna **en az bir kez tam Keycloak akışıyla
katılmış** olması şart (`cash_session_participants`). Vardiya başında bir kez; gün boyu PIN.

PIN doğrulaması mevcut `/auth/context` akışına bağlanıp CTX token üretir. Token:

- **`session_id` taşır** ve sunucu, oturum artık açık değilse reddeder.
- Ömrü `min(8 saat, oturumun kapanışı)`.

Böylece **kasayı kapatmak o oturumdan türetilmiş bütün token'ları geçersiz kılar** — açık bırakılan
bir istasyon, vardiya bitince kendiliğinden düşer. ADR'nin eski "açık soru"su bu şekilde kapandı:
çıkış ayrı bir aksiyon değil, oturumun kapanmasının sonucu.

### 5. Kaba kuvvet savunması

PIN 4-6 hane, yani anahtar uzayı küçük — asıl savunma deneme sayısı:

- Sayaç `(session_id, person_id)` başına, Redis'te (ADR-OPS-003'ün altyapısı).
- 5 başarısız denemeden sonra o kişi için PIN yolu **kilitlenir**; yalnızca tam Keycloak akışıyla
  yeniden katılım açar.
- Kilitlenme olayı denetim izine yazılır — sessizce kilitlenip kasiyeri şaşırtmamalı.

Saklama argon2id, kullanıcı başına tuz. PIN hiçbir biçimde istemciye inmez; hash de inmez.
