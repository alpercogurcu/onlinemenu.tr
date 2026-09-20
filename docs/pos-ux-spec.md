# POS UX Şartnamesi

**Kapsam:** POS desktop (Wails) + admin POS ekranları. Hedef kitle: garson/kasiyer/aşçı, bilişim profili düşük.
**Pilot:** tek şube, tek kasa, dokunmatik 1366x768 / 15". Garson tableti ikinci faz.
**Ölçüt:** ilk kez kullanan bir kasiyer eğitimsiz olarak sipariş alıp ödeme kapatabilmeli.

---

## 1. Denetim Bulguları

| # | Bulgu | Yer | Etki |
|---|---|---|---|
| 1 | **Masa planına dönüş yolu yok.** Orta panel `selectedCheck` varsa ProductGrid, yoksa TablePlan. `setSelectedCheck(null)` yalnız kapatma/çıkışta çalışır. Yanlış masaya giren garson adisyonu kapatmadan çıkamaz. | `App.tsx:1008-1019`, `806-837` | Kritik |
| 2 | **Kioskta yazılım klavyesi yok** ama üç yerde serbest metin/sayı isteniyor: tutar, banknot adedi, PIN. `inputMode` soft keyboard olmadan hiçbir şey yapmaz. | `Receipt.tsx:348-356`, `DenominationCounter.tsx:44-54`, `CashierSwitchModal.tsx:169,209-221` | Kritik |
| 3 | **Varyant/seçenek seçimi yok.** Ürün tile'ı tek dokunuşta düz ürün ekler; `ProductDTO`'da modifier alanı yok, Wails binding'i yok. Backend uçları hazır. | `ProductGrid.tsx:66-78`, `pos.go:26-36`, `catalog/http/handler.go:78-91` | Kritik |
| 4 | **ÖKC'ye kalem listesi gönderilmiyor.** `lines` boş gidiyor, servis tek satırlık sentetik "Satis" üretiyor — kodun kendi yorumu "gerçek cihaz adapter'ları bunu reddedecek" diyor. Token/Beko canlıya alındığında ödeme patlar. | `payment_service.go:291-302`, `App.tsx:722` | Kritik |
| 5 | `addProductToPending` yalnız `productId` ile birleştiriyor; farklı seçenekli iki aynı ürün tek satıra çöker. Varyant işi bu fonksiyonu değiştirmeden yapılamaz. | `cart.ts:15-33` | Yüksek |
| 6 | **Adet artırma yok.** 3 çay için tile'a üç kez dokunulur; adisyon satırında stepper yok. | `ProductGrid.tsx:70`, `Receipt.tsx:201-220` | Yüksek |
| 7 | Bekleyen satır silme hedefi 32px (`min-h-8 min-w-8`) — 48px tabanının altında, yanlış dokunuş riski. | `Receipt.tsx:211-218` | Yüksek |
| 8 | **Kart ödemesi yok.** Tek yöntem "Nakit al"; backend `method` alanını zaten taşıyor. | `Receipt.tsx:262-271`, `payment/http/handler.go:170` | Yüksek |
| 9 | Masa taşıma / adisyon birleştirme / kalem taşıma backend'de **yok** — rota listesinde karşılığı yok. Saha bunu manuel (kâğıt) çözer. | `pos/http/handler.go:80-114` | Yüksek |
| 10 | Açık adisyon listesinde **tutar yok** — yalnız masa adı + süre. Kasiyer hangi hesabın büyük olduğunu göremez. | `CheckRail.tsx:68-79` | Orta |
| 11 | **Banner yığılması.** Kasa + yazıcı + mutfak(N) + mali(N) sınırsız üst üste binebilir; 768px ekranda çalışma alanını yutar. | `App.tsx:915-996` | Orta |
| 12 | **Amber üç anlama geliyor:** dolu masa, birincil aksiyon, uyarı. Kod bunu kendi yorumunda itiraf ediyor. | `TablePlan.tsx:100`, `HoldButton.tsx:8`, `App.tsx:872-879` | Orta |
| 13 | `HoldButton` yalnız pointer olayı dinliyor; klavye/erişilebilirlik yolu yok (Enter/Space onaylamaz). | `HoldButton.tsx:43,54` | Orta |
| 14 | **Güvenlik (doğrulandı):** POS personel akışında `unit_price_amount` istemciden geliyor ve sunucuda katalog fiyatına karşı **hiç doğrulanmıyor** — `placeOrder` gövdeyi olduğu gibi domain'e kopyalıyor. POS keyfi fiyat/indirim yazabilir. QR misafir akışı tam tersini yapıyor: fiyat katalog read-model'inden türetiliyor ("client's own numbers never entered that derivation") — yani kod tabanı doğrusunu biliyor, personel yolunda uygulamıyor. | `cart.ts:47-58` → `pos/http/handler.go:324-339`; karşıtı `pos/service/order_service.go:264-267` | Güvenlik |
| 15 | Kategori sekmeleri yatay kaydırmalı ama kaydırma ipucu yok; 8+ kategoride sağdakiler görünmez. | `ProductGrid.tsx:43-56` | Orta |
| 16 | **Admin'de Sheet/drawer kalmadı** — `SheetContent` yalnız mobil sidebar'da. "Yapışık" şikâyetinin karşılığı iki yerde: (a) `MenuItemsDialog` 672px dialog içinde tam `<Table>` + ürün seçici, `max-h-[70vh]` kuyuda; (b) ürün editöründe sağdaki 5 sütun (~400px) modifier + menü kartlarını sıkıştırıyor. | `ui/sidebar.tsx:178`, `form-dialog.tsx:64`, `product-editor.tsx:370-374` | Orta |

---

## 2. Tasarım İlkeleri

1. **Tek ekran, tek görev.** Sipariş alma, ödeme ve kasa birbirinden ayrı; hiçbiri diğerini üst üste bindirmez. Modal yalnız *seçim* için (varyant, yöntem, onay) — asla veri girişi zinciri için.
2. **En fazla 2 dokunuş / kalem.** Seçeneksiz ürün 1 dokunuş. Seçenekli ürün: tile + "Ekle" = 2 (zorunlu grupta varsayılan önseçili gelir).
3. **≥48px hedef, ≥8px aralık.** Para ve iptal ilgili her hedef ≥56px. 32px'lik hiçbir dokunma hedefi kalmaz.
4. **Her ekranda görünür çıkış.** "Geri", "Vazgeç" veya "Masa planı" her zaman ekranda ve sabit yerde. Kullanıcı hiçbir durumda kapana kısılmaz.
5. **Bloklama yerine yönlendirme.** Yapılamayan aksiyon gizlenmez; nedeni ve düzeltme yolu aynı satırda yazılır (mevcut `closeBlockReason` deseni doğru — genele yayılacak).
6. **Sayısal giriş daima keypad.** Kioskta klavye yoktur. Tutar, adet, PIN, banknot sayısı → ekran içi tuş takımı. Serbest metin yalnız "not" alanında ve orada da hazır etiket çipleri önce gelir.
7. **Renk tek anlam + asla tek gösterge.** `amber` = para/birincil aksiyon; `teal` = tamam/ödenmiş; `danger` = iptal/void; uyarı için ayrı bir `warn` tokenı açılır ve dolu masa amber'den `slate-occupied`'a taşınır. Her renkli durum ayrıca metin/ikon taşır.

---

## 3. Akış Tasarımları

### 3a. Sipariş alma + varyant/seçenek

```
┌ Adisyonlar ─┬ [← Masa planı]  Kategori: İçecek | Ana | Tatlı ▸ ─┬ Masa 7 ──────┐
│ Masa 7  ₺240│  ┌──────┐ ┌──────┐ ┌──────┐ ┌──────┐             │ 2× Lahmacun  │
│ Masa 3  ₺ 95│  │ Çay  │ │Lahma-│ │ Ayran│ │Künefe│             │   acılı  ₺180│
│ Paket   ₺ 60│  │  ₺15 │ │ cin  │ │  ₺25 │ │ ₺120 │             │   [−] 2 [+] ×│
│             │  └──────┘ └──────┘ └──────┘ └──────┘             │ 1× Çay   ₺ 15│
│ [Paket srv] │           ▲ seçenekli ürün: köşede ⚙ rozeti      │ ─────────────│
└─────────────┴───────────────────────────────────────────────────┤ Toplam ₺195  │
                                                                  │ [Siparişi gön]│
                                                                  │ [Ödeme al]   │
                                                                  └──────────────┘
```

Seçenekli ürüne dokununca **ortada merkezî dialog** (drawer değil — sağ raf zaten adisyon):

```
┌─ Lahmacin ──────────────────────────── ₺60 ─┐
│ Acı (zorunlu, tek seçim)                    │
│  [ Acısız ]  [• Acılı ]  [ Çok acı ]        │
│ Ekstra (çoklu, opsiyonel)                   │
│  [ Peynir +₺15 ] [• Lavaş +₺5 ] [ Soğan ]   │
│ Not: [Soğansız] [Az pişmiş] [ ✎ Not yaz ]   │
│ Adet  [ − ]  2  [ + ]        Satır: ₺130    │
│ [ Vazgeç ]                [ Adisyona ekle ] │
└─────────────────────────────────────────────┘
```

**Kurallar**
- Zorunlu grupta ilk seçenek **önseçili** gelir → hızlı kasiyer hiç düşünmeden "Adisyona ekle"ye basar (2 dokunuş).
- Zorunlu grup seçilmeden "Adisyona ekle" **disabled değil**; basılırsa eksik grup kırmızı çerçeveyle işaretlenir ve oraya kaydırılır (ilke 5).
- Fiyat farkı çip üstünde `+₺15` olarak ve satır toplamında canlı gösterilir.
- Seçeneksiz üründe dialog **hiç açılmaz** — tek dokunuş, doğrudan adisyona.
- **"Aynısından bir tane daha":** adisyon satırındaki `[+]` stepper aynı seçenek setiyle adedi artırır. Ürün ızgarasına dönmeye gerek yok.

**Veri saklama kararı (şema değişikliği yok)**
- Seçilen opsiyonlar `order_items.note` alanına yapılandırılmış metin olarak yazılır: `Acılı | Lavaş(+5) | Soğansız`. Mutfak fişi bunu zaten basıyor — `internal/receipt/kitchen.go:85-86` her kalemin altına `    > <not>` satırı ekliyor ve uzun notu kırpmadan sarıyor. Ek iş gerekmez.
- Fiyat farkı `unit_price_amount` içine katılır. **Önkoşul:** bulgu #14 — backend `unit_price_amount`'ı ürün+modifier fiyatına karşı doğrulamalı; aksi halde varyant kanalı bir indirim açığına dönüşür.
- `cart.ts` `addProductToPending` birleştirme anahtarı `productId` → `productId + optionsHash` olur (bulgu #5).
- Wails tarafına iki binding eklenir: `ListProductModifierGroups(productID)`, `ListModifiers(groupID)`. Kategori ürünleriyle birlikte önbelleğe alınır (açılışta tek sefer), böylece tile dokunuşu ağ beklemez.

**Hata halleri:** modifier listesi çekilemezse ürün seçeneksiz olarak eklenir + adisyon satırına "seçenek alınamadı" uyarısı; sipariş gönderilir, kasiyer mutfağa sözlü iletir (manuele düşmek yerine akış sürer).

### 3b. Ödeme

**Yerleşim kararı:** ödeme, 384px'lik sağ rafa sığmaz (≥56px tuşlu keypad + tutar + para üstü). Ödeme kipi **orta paneli devralır** — ProductGrid/TablePlan yerine tam genişlikte ödeme ekranı açılır, sağ raf adisyon dökümü olarak kalır (kalem seçimi orada yapılır). Mevcut `Receipt.tsx`'teki "cashMode rafı yerinde genişletir" deseni terk edilir. Vazgeç → orta panel ProductGrid'e döner.
**Dokunuş sayısı:** tam nakit ödeme 3 (Ödeme al → Tam → Ödemeyi al). Para üstlü ödeme 4-6 (tutar keypad'de). Kalem bazlı 4+n (n = seçilen kalem).

```
┌ Ödeme — Masa 7 ───────────────────────────────────────┐
│ Toplam ₺240   Ödenen ₺0          KALAN  ₺240          │
│ [ Tümü ]  [ 2'ye böl ] [ 3'e böl ] [ Kalem seç ]      │
│ Yöntem:  [● Nakit ]  [ Kart ]                         │
│ ┌ Alınan ──────────┐  ┌ 7 8 9 ┐   Para üstü          │
│ │      200,00      │  │ 4 5 6 │      ₺60,00          │
│ └──────────────────┘  │ 1 2 3 │                      │
│ [₺50][₺100][₺200] [Tam]│ 0 00 ⌫│   [ Ödemeyi al ]     │
└───────────────────────┴───────┴───────────────────────┘
```

- Tuş takımı **her zaman görünür** — tutar alanına dokunmak gerekmez (kioskta klavye yok).
- "Kalem seç" → adisyon satırlarında seçim kipi; seçilenlerin toplamı tutar alanına düşer, kalan liste ekranda kalır:

```
 ☑ 2× Lahmacun (acılı)   ₺180     Seçilen: ₺195
 ☑ 1× Çay                ₺ 15     Kalan  : ₺ 45
 ☐ 3× Ayran              ₺ 45     [ Seçileni öde ]
```

**Kalem bazlı ödemede backend kararı — istemci tarafı seçim + mevcut `lines` alanı.**
Gerekçe: `fiscalLineRequest`'te `order_item_id` yok, `CheckSettlement` yalnız id+tutar döner, kapanış guard'ı (`TotalPaidForCheck`) tutar bazlıdır. Sunucuya kalem başına `paid_amount` eklemek şema + RLS + settlement + kapanış guard'ı zincirini açar; tek kasalı pilotta karşılığı yok. Buna karşılık seçilen kalemler **zaten var olan `lines` dizisine doldurulur** — bu hem kalem bazlı ödemeyi mali fişe doğru yansıtır hem de bulgu #4'ü (sentetik "Satis" satırı, gerçek ÖKC reddi) kapatır. **Tek taşla iki kuş: bu iş paketi zaten yapılmak zorunda.**
Artık risk (açıkça kabul ediliyor): kalem→ödendi eşleşmesi yalnız POS belleğinde durur; uygulama yeniden başlarsa veya ikinci kasa aynı adisyona girerse kaybolur; para toplamı yine de doğru kalır (tutar bazlı `remaining`). **Faz 2 tetikleyicisi:** ikinci kasa veya garson tableti devreye girdiğinde sunucu tarafı kalem `paid_amount` şart olur.

**Hata halleri:** ödeme POST'u hata verirse mevcut `refreshServerPayments` resync'i korunur; kalem seçimi ekranda kalır, kasiyer tekrar dener. Kart yöntemi Faz 1'de "harici POS cihazından çekildi" kaydıdır (entegrasyon yok) — düğme metni bunu söyler.

### 3c. Masa taşıma / adisyon birleştirme / kalem taşıma

Üçü de adisyon başlığındaki `[⋯]` menüsünden açılır; hedef seçimi **masa planının kendisi** üzerinden yapılır (ayrı liste yok — garson zaten planı tanıyor).
**Dokunuş sayısı:** masa taşıma 3 (`⋯` → "Masayı değiştir" → hedef masa). Birleştirme 4 (`⋯` → "Birleştir" → hedef masa → onay). Kalem taşıma 3+n (`⋯` → "Kalem taşı" → n kalem → hedef masa).

| Aksiyon | Metod + Path | Gövde | İzin |
|---|---|---|---|
| Masa taşıma | `POST /api/v1/pos/checks/{id}/transfer` | `{"table_id": uuid}` | `pos.check.transfer` |
| Adisyon birleştirme | `POST /api/v1/pos/checks/{id}/merge` | `{"source_check_id": uuid}` | `pos.check.merge` |
| Kalem taşıma | `POST /api/v1/pos/checks/{id}/move-items` | `{"target_check_id": uuid, "order_item_ids": [uuid]}` | `pos.order.move_items` |

**Ortak kurallar**
- Üçü de `httpx.Idempotency(hwc.cache)` ile sarılır (ADR-SEC-003; para taşıyan adisyon durumunu değiştiriyorlar).
- Hata gövdeleri payment handler'daki makine-okunur kod desenini izler (`respondError(w, 409, codeCheckNotOpen, ...)`), çıplak 409 değil.
- İzinler **iki yere** yazılır: `authz.rego`'daki `pos_counter_actions` kümesi (`cashier` + `shift_manager`) **ve** `role_permissions` seed migration'ı. Yalnız biri yapılırsa e2e'de 403 alınır.

**transfer**
- `200 CheckResponse`. `409 check_not_open` (kapalı/iptal adisyon), `409 table_occupied` (hedefte açık adisyon var — kullanıcıya "birleştirmek ister misiniz?" önerilir), `409 check_branch_mismatch`, `422 table_not_found`.
- **Masa durumu aynı transaction'da sunucuda çevrilir**: kaynak masa `available`, hedef `occupied`. İstemci `setTableStatus` çağıramaz — o uç `pos.table.manage` ile korunuyor ve kasiyerde yok.
- Mutfak fişi: **bilgi fişi basılır** — `TAŞINDI: Masa 4 → Masa 7`. Aşçı yemeği yanlış masaya çıkarmamalı.

**merge**
- `{id}` hedef (kalan) adisyon, `source_check_id` eriyen adisyon. Kaynağın tüm order'ları hedefe bağlanır.
- **Kaynak adisyonun statüsü: yeni `merged` değeri** (`cancelled` DEĞİL). `cancelled`'a düşürmek gün sonu raporunu ve admin adisyon tablosunu (`pos/checks/page.tsx:137-139` durum rozeti) bozar — her birleştirme bir iptal gibi sayılır. `merged` için migration + `checkStatusVariant` + `posChecks.status.merged` çevirisi eklenir; raporlarda `cancelled` gibi değil, "hesabı başka adisyona aktarıldı" olarak ele alınır.
- **`409 payments_present`: kaynak adisyonda herhangi bir ödeme (completed *veya* pending) varsa birleştirme reddedilir.** Ödeme satırlarını başka adisyona taşımak mali kayıt/ÖKC eşleşmesini bozar — bu bir sprint işi değil veri bütünlüğü projesidir. Kasiyere "önce ödemesi olan adisyonu kapatın" denir.
- Diğerleri: `409 check_not_open` (iki taraf da açık olmalı), `409 check_branch_mismatch`, `422 same_check`.
- Kaynak masa `available`'a çevrilir. Mutfak fişi: bilgi fişi basılır (`BİRLEŞTİ: Masa 3 → Masa 7`).

**move-items**
- Seçilen `order_item` satırları hedef adisyonda **yeni bir order** altına taşınır; kaynak order'da kalem kalmazsa order iptal edilir. `200` hedef `CheckResponse`.
- `409 check_not_open` (her iki adisyon), `409 item_already_paid` (kaynakta ödeme varsa ve kalan tutar taşınan kalemleri karşılamıyorsa), `422 order_item_not_found` / kalem başka adisyona ait.
- Mutfak fişi: **evet, bilgi fişi** — `KALEM TAŞINDI: Masa 4 → Masa 7 / 2× Lahmacun`.

### 3d. Kasa açma/kapama ve kasiyer değiştirme — sadeleştirme

Mevcut `CashSessionModal` tek modalde dört görünüm (açılış/hareket/sayım/kapanış) + 478 satır. İlk kullanıcı için fazla.

- **Açılış:** tek soru, tek ekran — "Kasada başlangıç parası ne kadar?" + tuş takımı + `[₺0] [₺500] [₺1000]` hazır tutarlar. Not alanı **opsiyonel ve katlanmış**.
- **Kapanış:** `DenominationCounter` korunur ama `<input type="number">` kaldırılır (klavye yok) — `[−]/[+]` stepper + satıra dokununca keypad. Sık kullanılan banknotlar (₺200/₺100/₺50) listenin başına alınır.
- Hareket (para giriş/çıkış) kapanış akışından ayrılır, kasa ekranında ikincil bir düğme olur.
- **Kasiyer değiştirme:** isim listesi → dokun → PIN keypad'i aynı ekranda açılır (ayrı adım yok). PIN alanı 4-6 nokta gösterir, rakam girildikçe dolar, 4. haneden sonra otomatik doğrulama denenmez (yanlış PIN sayacını boşa harcar) — `[Giriş]` düğmesi gerekir.
- Kasa kapalıyken satış: mevcut banner davranışı doğru (bloklamıyor, gizlemiyor) — korunur.
- **Dokunuş sayısı:** kasa açma 3 (Kasa Aç → hazır tutar → Onayla). Kasiyer değiştirme 2 + PIN hanesi (isim → rakamlar → Giriş). Kasa kapama, banknot çeşidi sayısı kadar (stepper), + Sayımı gönder + Basılı tutup kapat.

### 3e. Admin: drawer → dialog kararı

**Bulgu:** admin POS ekranlarında drawer/Sheet zaten yok (`SheetContent` yalnız `ui/sidebar.tsx:178`, mobil navigasyon). Şikâyet iki gerçek sıkışıklığa karşılık geliyor:

| Ekran | Karar | Neden |
|---|---|---|
| `MenuItemsDialog` (`catalog/menus`) | **Dialog'dan çıkar, kendi sayfasına al:** `/catalog/menus/[id]` | 672px dialog içinde tam `<Table>` + ürün seçici + fiyat override, üstelik `max-h-[70vh]` kuyuda (`form-dialog.tsx:64`). Bu bir liste yönetim ekranı, bir form değil. |
| `product-editor` sağ sütun (`:370-374`) | Kırılım eşiğini yükselt: `lg:grid-cols-12` → `xl:grid-cols-12`; altında tek sütun, kartlar alt alta tam genişlik | 1366px'de sağ 5 sütun ~400px; modifier grubu + menü kartları o genişlikte yapışık duruyor. |
| `pos/tables` form dialog'ları | **Değişiklik yok** — Dialog doğru | Kısa form (ad, kapasite, bölge), 672px fazlasıyla yeterli. |
| `pos/checks`, `pos/kitchen` | **Değişiklik yok** | Drawer içermiyor; tablo/kart ızgarası. |

**Genel kural:** ≤6 alanlık form → `FormDialog`. Tablo, liste yönetimi veya iç içe seçim → kendi sayfası. Sheet yalnız mobil navigasyonda kalır.

---

## 4. Bileşen Kararları

| Bileşen | Karar | Yer |
|---|---|---|
| **Numpad** | Yeni, POS'a özel: `0-9`, `00`, `⌫`, `,`. Tuş ≥56px. `value`/`onChange` kontrollü, kuruş tabanlı. Tutar, PIN, adet ve banknot sayımının **tek** giriş yolu. | `pos-desktop/.../components/Numpad.tsx` |
| **OptionPicker** | Varyant dialog'u. Zorunlu grup = radio çipleri (ilk seçili), opsiyonel grup = toggle çipleri. Çip ≥48px, `aria-pressed`. | `pos-desktop/.../components/OptionPicker.tsx` |
| **Modal** | POS'ta shadcn yok (Wails tarafı kendi CSS'iyle çalışıyor). Mevcut `CashSessionModal` kabuğu ortak bir `Modal` bileşenine çıkarılır; Sheet deseni POS'ta **hiç kullanılmaz** (sağ raf zaten adisyon). | `pos-desktop/.../components/Modal.tsx` |
| **Adisyon satırı** | Tek düzen: `[−] adet [+]  ürün adı / seçenek satırı  tutar  [×]`. `[×]` hedefi 32px→48px. Seçenekler ürün adının altında `text-xs` ikinci satır. | `Receipt.tsx` |
| **Admin Dialog vs Sheet** | Sheet yalnız mobil sidebar. Form → `FormDialog`, liste yönetimi → sayfa (bkz. 3e). | — |
| **ui-kit'e taşıma** | **Hayır, şimdilik.** POS desktop kendi token/CSS setini kullanıyor, admin shadcn/Tailwind. Numpad ve OptionPicker tek tüketicili — erken ortaklaştırma iki tarafı da bozar. Garson tableti (Faz 2, aynı React + token seti) geldiğinde `@onlinemenu/ui-kit`'e taşınır. | — |

---

## 5. Uygulama Planı

Paketler bağımsızdır; P1 ve P2 paralel gidebilir, P3 P1'e bağımlıdır.

| # | Paket | Dosyalar | Kabul kriteri |
|---|---|---|---|
| **P0** | **Sunucu tarafı fiyat doğrulaması** (bulgu #14 — varyantın önkoşulu; açık doğrulandı) | `pos/service/order_service.go` (`Place`), `pos/http/handler.go:324-339`; katalog fiyatı için `catalog/public` arayüzü (modüller arası DB erişimi yasak) | Manipüle `unit_price_amount` ile POST `/pos/orders` → `422`. Birim test: ürün taban fiyatı + seçilen modifier toplamıyla uyuşmayan satır reddedilir; QR misafir yolu (zaten sunucuda fiyatlanan) etkilenmez. |
| **P1** | **Varyant/seçenek seçimi** (3a) | `pos-desktop/pos.go` (+2 binding, `ProductDTO` modifier alanları), `lib/cart.ts` (optionsHash), `components/OptionPicker.tsx`, `ProductGrid.tsx`, `Receipt.tsx` | Vitest: `addProductToPending` farklı opsiyonlu aynı ürünü ayrı satır tutar. Manuel: seçenekli ürün 2 dokunuşta eklenir, mutfak fişinde seçenekler görünür. |
| **P2** | **Numpad + dokunma hedefleri + kasa sadeleştirme** (3d, bulgu #2/#6/#7/#13) | `components/Numpad.tsx`, `Receipt.tsx`, `DenominationCounter.tsx`, `CashierSwitchModal.tsx`, `CashSessionModal.tsx`, `HoldButton.tsx` (klavye) | Kod taramasında `<input type="number">` ve klavye gerektiren serbest sayı alanı kalmaz. Tüm dokunma hedefleri ≥48px. `HoldButton` Space/Enter ile onaylanır. |
| **P3** | **Ödeme: orta panel devralma, kart yöntemi, kalem bazlı ödeme, `lines` doldurma** (3b, bulgu #4/#8) | `Receipt.tsx` (cashMode çıkarılır), yeni `components/PaymentScreen.tsx`, `App.tsx` (panel yönlendirmesi), `lib/payment.ts`, `pos-desktop/pos.go:270` (`RegisterCashPayment` imzası: `lines` + `method`) | `RegisterCashPayment` gövdesinde `lines` dolu gider (sentetik "Satis" satırı üretilmez — `buildFiscalSale` servis testi). Kalem seçip ödeme: kalan tutar doğru, kapanış guard'ı geçer. Ödeme ekranı 1366x768'de yatay taşmasız. |
| **P4** | **Backend: transfer / merge / move-items + `merged` statüsü** (3c) | `pos/http/handler.go`, `pos/service/check_service.go`, `pos/repo/*`, `merged` statüsü için pos migration + `admin/lib/status-badge.ts` + çeviri, `configs/opa/bundles/authz.rego`, identity `role_permissions` seed migration | Integration test (testcontainers): transfer sonrası kaynak masa `available` + hedef `occupied` aynı tx'te; ödemesi olan kaynakla merge → `409 payments_present`; kapalı adisyonla üçü de `409`; `Idempotency-Key` tekrarı ikinci kayıt yaratmaz; cashier rolüyle 200, waiter rolüyle 403. |
| **P5** | **POS: taşıma/birleştirme/kalem taşıma arayüzü + masa planına dönüş** (3c, bulgu #1/#10/#11) | `App.tsx` (geri düğmesi, banner sınırı), `CheckRail.tsx` (tutar), `TablePlan.tsx` (hedef seçim kipi), yeni `CheckActionsMenu.tsx` | P4'e bağımlı. Manuel: adisyon açıkken "Masa planı" ile geri dönülür; masa taşıma 3 dokunuşta biter; banner'lar en fazla 2 satır kaplar (fazlası "+N" ile toplanır). |
| **P6** | **Admin sıkışıklık düzeltmesi** (3e) | `app/(main)/catalog/menus/[id]/page.tsx` (yeni), `components/catalog/menu-items-dialog.tsx` (kaldır), `components/catalog/product-editor.tsx:370-374` | `web/apps/admin/e2e/catalog.spec.ts` güncellenir: menü içeriği yönetimi sayfa üzerinden yürür, dialog beklenmez. 1366px viewport'ta ürün editörü sağ sütununda yatay taşma yok. |

**Renk tokenı notu (bulgu #12):** P2 ile birlikte `style.css`'e `warn` tokenı eklenir ve dolu masa amber'den ayrı bir tona taşınır; `amber` yalnız para/birincil aksiyon anlamında kalır. **Tek dosya değil:** dolu masa amber'den çıkınca `TablePlan.tsx:100` varyantı, `TablePlan.tsx:120-125`'teki `PendingFiscalDot onAmber={isOccupied}` propu ve `PendingFiscalDot.tsx` kontrast varyantı birlikte düzeltilmeli — aksi halde bekleyen-mali-kayıt noktası yeni zeminde görünmez olur. P2 kapsamındadır.
