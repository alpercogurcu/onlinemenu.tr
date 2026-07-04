# ADR-DATA-007: Tedarik Politikası ve Şube-Yerel Maliyet

**Durum:** 📝 Taslak (Öneri / Proposed)
**Tarih:** 2026-07-04
**Kategori:** Veri / Event (DATA)
**İlgili:** DATA-005 (Reçete/BOM — İlke 4 revizyonu), DATA-006 (Şubeler Arası Sipariş — `unit_price` eklentisi), `party` (suppliers), `docs/lessons-from-b2b.md`

## Bağlam — Gerçek Franchise Senaryosu

DATA-005 İlke 4, hammadde görünürlüğünü mutlak bir kuralla ("hammaddeyi şube **asla** göremez") tanımlamıştı. Kullanıcıdan gelen gerçek bir franchise senaryosu bu varsayımın yanlış olduğunu gösterdi:

- **Hamburger reçetesi** = köfte + ekmek + patates + şamua kağıdı + eldiven. Franchisor sözleşmeleri **farklıdır**:
  - Kimi franchisor: "eldivene kadar her şeyi benden alacaksın" (tam bağımlı).
  - Diver Street Food: "köfte/ekmek/şamua benden, patatesi **benim toptancımdan**, eldiveni **serbest**."
- **İçecek:** kimi franchise Pepsi/Coca-Cola anlaşmalı, kimi serbest. Politika **zamanla değişir**: "özhisar ayran, nereden alırsan al" diyen franchisor, sonradan kendisi ayran satmaya başlayıp kalemi tekeline alabilir.
- **Marul:** bir franchise pazardan, diğeri marketten alır → aynı kalemin **şube maliyeti son alıma göre şubeye özel** değişir.
- **BBQ sos:** hem POS satış ürünü hem reçete bileşenidir; şubeler farklı markadan temin edebilir.
- **Elden fiş** (faturasız pazar/market alımı) da bir gider kalemi olarak sisteme girmelidir.

**Sonuç:** Görünürlük kalemin türüne (`kind`) değil, o kalem için geçerli **ticari tedarik politikasına** bağlıdır. Bazı hammaddeleri şube görmeli, yerelden almalı ve **kendi maliyetini yönetmelidir**.

Bu, b2b'nin `BranchStockTracking` satır-bayrağına geri dönmek **değildir**. Fark kritik: politika kalemin kimliğinde (`stock_items`) yaşamaz; ayrı bir **sözleşme** varlığında (`supply_policies`) yaşar, zaman-eksenlidir ve tekilden default'a çözülür.

---

## Karar

### 1. `supply_policies` tablosu (tenant-level, zaman-eksenli)

Tedarik kuralı, bir kalemin/kategorinin nasıl temin edileceğini tanımlar. Politika stok kaleminden **ayrı** bir tablodadır (ticari sözleşme, kalem özelliği değil).

- **scope** ∈ `{stock_item, category, tenant_default}` — kuralın kapsamı.
- **mode** ∈ `{exclusive_hq, approved_suppliers, free}`:
  - `exclusive_hq` — yalnızca imalathane/HQ'dan (BTO ile) alınır. Franchisor korumacı taraf.
  - `approved_suppliers` — belirli onaylı tedarikçilerden (Diver'ın patates toptancısı, anlaşmalı Pepsi).
  - `free` — şube serbest, herhangi bir yerden (pazar, market).
- **effective_from** — politika zamanla değişir (özhisar senaryosu): değişiklik eski satırı güncellemez, **yeni satır** ekler; çözümlemede `effective_from <= now()` olan en güncel satır etkilidir. Geçmiş korunur (immutable ruhu, DATA-002).
- **Varsayılan (hiç kural yoksa):** `exclusive_hq` — franchisor korumacı taraf, güvenli varsayım.

**Çözümleme önceliği** (en spesifik kazanır, `effective_from <= now()`):

```
stock_item override  >  category default  >  tenant_default  >  (kural yok → exclusive_hq)
```

### 2. Görünürlük politikadan türetilir (DATA-005 İlke 4 revizyonu)

Görünürlük artık `kind`'dan değil, çözülen `mode`'dan türetilir:

| mode | Şube kalemi görür mü? | Ne görür? | Yerel alım? |
|---|---|---|---|
| `exclusive_hq` | Yalnızca **BTO kataloğunda** | Sipariş verebilir; maliyet/tedarikçi detayı **yok** | Hayır — yalnız BTO |
| `approved_suppliers` | **Tam** | Onaylı tedarikçiler + kendi maliyeti | Evet (onaylı listeden) |
| `free` | **Tam** | Serbest; kendi maliyeti | Evet (herhangi) |

- OPA permission'ları **değişmez** (AUTH-001). Politika filtrelemesi **service katmanında** uygulanır — AUTH-001 **Layer 3** (WHERE/scope) deseni. `exclusive_hq` kalemler şubenin envanter/maliyet sorgusundan Layer 3'te düşürülür; yalnızca BTO katalog sorgusunda görünür.
- Bu bir satır-bayrağı değildir: `stock_items` üstünde `visibility` kolonu **yoktur**. Filtre, `supply_policies` çözümlemesinden gelir.

### 3. Şube-yerel maliyet + `purchase_receipts` (elden fiş)

- Maliyet artık kaleme değil, **(warehouse, stock_item) çiftine** bağlıdır. Kaynak = o depoya ait **son alım belgesi satırı** (en güncel `received_at`).
- İki alım belgesi tipi, ikisi de `stock_movements(in)` + bir **gider kaydı** üretir:
  - Mevcut `purchase_orders` — **faturalı** alım.
  - Yeni `purchase_receipts` — **elden fiş / faturasız** alım (pazar, market).
- **`NoInvoice`'un doğru hali:** b2b'de `NoInvoice` bir **ürün bayrağıydı** (`product_type='raw_material'` için anlamlı) — yanlış. Faturalı olup olmama kalemin özelliği değil, **belgenin** tipidir. Bu yüzden ayrım `purchase_receipts` (belge) ile modellenir; `stock_items`'a bayrak eklenmez.

### 4. Exclusive kalemin şube maliyeti = transfer fiyatı (DATA-006 eklentisi)

`exclusive_hq` kalemde şube yerelden alamaz → son-alım maliyeti yoktur. Maliyeti, imalathane/HQ'nun uyguladığı **transfer fiyatı**dır. Bu yüzden `branch_transfer_order_items` ve `shipment_items`'a `unit_price` (+`currency`) eklenir (DATA-006 güncellemesi). Sevkiyat satırında dondurulur; ileride **franchise faturalamasının** (HQ → franchise satışı) temeli olur.

### 5. BBQ sos ikili rolü — ayrı stock_item AÇILMAZ

BBQ sos hem POS satış ürünü hem reçete bileşenidir → mevcut `catalog.products.source_stock_item_id` bağı (DATA-005) yeterlidir. Marka çeşitliliği (bir şube Heinz, diğeri başka marka) **aynı kanonik `stock_item`** ile modellenir; marka/tedarikçi bilgisi **alım belgesi satırında** taşınır (`supplier_ref`, opsiyonel `brand`). Marka başına ayrı `stock_item` **açılmaz** (aşağıda reddedildi).

### 6. Reçete görünürlüğü — ayrı eksen (Faz 2 notu)

Tedarik politikası, kalemin **görünürlüğü/tedariği** ile ilgilidir. Reçetenin **içeriği** (hangi bileşen ne kadar) ayrı bir eksendir ve varsayılan **HQ-only**'dir (franchisor IP'si). Tenant ayarı `recipe_visibility ∈ {hq_only, cost_summary, full}` **Faz 2'ye** not edilir; bu ADR'de implemente edilmez.

---

## Şema (öneri)

`inventory` modülünde. Tüm tablolarda `tenant_id UUID NOT NULL` + `FORCE ROW LEVEL SECURITY`. Para `NUMERIC(18,4)` + `currency`.

### supply_policies (inventory)

| Kolon | Tip | Not |
|---|---|---|
| id | uuid PK | UUID v7 |
| tenant_id | uuid | RLS |
| branch_id | uuid NULL FK → branches | **ÖNERİ** — NULL = tenant geneli; dolu = belirli franchise şubesi override (bkz. Açık Karar #1) |
| scope | text | `stock_item` \| `category` \| `tenant_default` |
| ref_id | uuid NULL | scope=`stock_item` → `stock_items.id`; scope=`category` → kategori referansı (bkz. Açık Karar #4); `tenant_default` → NULL |
| mode | text | `exclusive_hq` \| `approved_suppliers` \| `free` |
| approved_supplier_ids | jsonb | `party.suppliers` id listesi (yalnız mode=`approved_suppliers`) |
| effective_from | timestamptz | zaman-eksenli; en güncel `<= now()` etkin |
| created_by | uuid | |

Maliyet/görünürlük kolonu `stock_items` üstünde **yoktur** — hepsi buradan türetilir.

### purchase_receipts (inventory) — elden fiş / faturasız alım

| Kolon | Tip | Not |
|---|---|---|
| id | uuid PK | |
| tenant_id | uuid | |
| branch_id | uuid FK → branches | alan şube |
| warehouse_id | uuid FK → warehouses | girişin yapıldığı depo |
| supplier_ref | uuid NULL FK → party.suppliers | pazar/serbest için NULL olabilir |
| supplier_name_text | text | tedarikçi cari değilse serbest metin ("X Pazarı") |
| document_type | text | `elden_fis` \| `makbuz` \| `fatura_disi` |
| total_amount | numeric(18,4) | |
| currency | char(3) | |
| received_at | timestamptz | maliyet çözümlemesinde "son alım" bunu kullanır |
| note | text | |
| created_by | uuid | |

### purchase_receipt_items (inventory)

| Kolon | Tip | Not |
|---|---|---|
| id | uuid PK | |
| receipt_id | uuid FK | |
| stock_item_id | uuid FK → stock_items | |
| brand | text NULL | marka çeşitliliği (BBQ sos) — kalem değil, satır bilgisi |
| quantity | numeric(18,3) | |
| unit | text | write-time kanonik birime çevrilir (DATA-005) |
| unit_cost | numeric(18,4) | şube-yerel maliyetin kaynağı |
| line_amount | numeric(18,4) | |

Hem `purchase_orders` hem `purchase_receipts` teslim alındığında `stock_movements(in)` + bir **gider kaydı** üretir (gider kaydının muhasebe modülü ayrı — bkz. Açık Karar #3).

---

## Şube-Yerel Maliyet Çözümlemesi (özet)

Bir `(warehouse, stock_item)` çiftinin güncel maliyeti:

1. Çözülen `mode` = `exclusive_hq` → maliyet = ilgili `shipment_items.unit_price` (transfer fiyatı, DATA-006).
2. `approved_suppliers` / `free` → maliyet = o depoya ait en güncel `received_at` taşıyan `purchase_order_items` **veya** `purchase_receipt_items` satırının `unit_cost`'u.

Bu, DATA-005'in `work_order_costs.cost_basis=last_purchase` tanımını **(warehouse, stock_item)** granülaritesine ve elden-fiş belgelerine genişletir.

---

## Faz Önerisi

| Parça | Faz |
|---|---|
| `exclusive_hq` varsayılanı (hiç politika yoksa) | **Faz 1** — güvenli varsayım, tablo olmadan da geçerli |
| `branch_transfer_order_items`/`shipment_items`.`unit_price` (transfer fiyatı) | **Faz 1** (DATA-006 ile) |
| `supply_policies` tablosu + çözümleme + Layer 3 görünürlük filtresi | **Faz 2** |
| `purchase_receipts` (+ items) + şube-yerel maliyet | **Faz 2** |
| `recipe_visibility` ekseni | **Faz 2** |
| Franchisor → franchise cross-tenant politika push'u | **Faz 2+** (bkz. Açık Karar #2) |

---

## Değerlendirilen Alternatifler

- **`stock_items.visibility` (veya `branch_visible`) bayrağı:** **Reddedildi.** b2b'nin `BranchStockTracking` satır-bayrağının birebir tekrarı olurdu (lessons-from-b2b): opt-in, her call site'ta unutulur, zaman-ekseni yok, tekil/default çözümlemesi yapılamaz. Görünürlük kalemin kimliği değil, sözleşmenin fonksiyonudur.
- **Marka başına ayrı `stock_item`** (Heinz BBQ, X BBQ ayrı kalem): **Reddedildi.** Kanonik kalem patlaması; reçete bileşeni hangi markaya bağlanacak belirsizleşir; stok/maliyet parçalanır. Doğrusu: tek kanonik `stock_item` + alım satırında `brand`/`supplier_ref`.
- **Politikayı `purchase_orders`/BTO'ya gömmek:** **Reddedildi.** Politika satın almadan **önce** gelir ("şube bu kalemi sipariş edebilir mi / yerelden alabilir mi?" kararı). Ayrı, zaman-eksenli tablo gerekir.
- **`NoInvoice`'u kalem bayrağı yapmak (b2b):** **Reddedildi.** Faturalı olmama belgenin tipidir; `purchase_receipts` ile modellenir.
- **Görünürlüğü yalnızca OPA permission'ıyla yönetmek (DATA-005 orijinal İlke 4):** **Reddedildi/revize edildi.** Aynı rol (franchise `manager`), kaleme göre farklı görmeli — permission statiktir, politika kalem-bazlı ve zaman-eksenlidir. Bu yüzden Layer 3 (service) filtresi.

---

## Açık Bırakılan Kararlar

1. **`branch_id` ekseni (önemli):** Team-lead'in onayladığı şema *tenant-level* (scope: item/category/tenant_default). Ancak senaryo **per-franchise** farkı gerektiriyor (Diver Street Food ≠ diğer franchise). `supply_policies.branch_id NULL` kolonunu **öneri olarak ekledim**: NULL = tenant varsayılanı, dolu = franchise şubesi override; öncelik `(branch+item) > (branch+category) > (branch default) > tenant(item) > tenant(category) > tenant_default`. **Team-lead onayı gerekir** — franchise'lar tek tenant'ın şubeleri mi (branch_id gerekli) yoksa ayrı tenant mı (branch_id gereksiz, her franchise kendi politikasını taşır)?
2. **Franchise = şube mi, tenant mi?** DATA-006 franchise'ı tek tenant içinde şube olarak modelliyor. Franchise ayrı tenant ise, franchisor'ın politikayı franchise-tenant'a **push** etmesi cross-tenant bir akış gerektirir (Faz 2+).
3. **Gider kaydı modülü:** "Elden fiş/fatura gider kalemi olarak girilmeli." Gider (expense/ledger) tablosunun hangi modülde (muhasebe/billing) yaşayacağı bu ADR'de kapsanmadı — `purchase_receipts` gideri tetikler ama gider defterinin şeması ayrı bir karar.
4. **`category` referansı:** `stock_items.category` şu an `text`. scope=`category` çözümlemesi metin eşleşmesi mi (ref_id yerine `ref_category text`) yoksa `catalog.categories`/ayrı stok-kategori tablosuna FK mi olacak? Öneri: stok kalemleri için ayrı `stock_categories` (uuid) — implementasyonda netleşir.
5. **`approved_supplier_ids` jsonb vs ilişkisel:** jsonb id listesi MVP için yeterli; onaylı tedarikçi sayısı büyürse ara tabloya taşınır.
