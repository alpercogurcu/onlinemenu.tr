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

## Açık soru

Vardiya içinde kasiyerin **çıkışı** nasıl olur — açık bırakılan bir istasyon, kasiyer değişiminde
otomatik mi düşer, yoksa süre aşımıyla mı? Odoo'da bu kavram yok (istemci tarafı etiket olduğu için
gerekmiyor). Bizde gerçek principal olduğu için context token'ın ömrü bu kararı taşır; TTL değeri
implementasyon sırasında ölçülüp bu ADR'ye yazılmalıdır.
