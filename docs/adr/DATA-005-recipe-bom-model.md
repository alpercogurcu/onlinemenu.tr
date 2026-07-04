# ADR-DATA-005: Reçete / BOM ve Stok Kalemi Modeli

**Durum:** 📝 Taslak (Öneri / Proposed)
**Tarih:** 2026-07-04
**Kategori:** Veri / Event (DATA)
**İlgili:** DATA-006 (Şubeler Arası Sipariş), `docs/lessons-from-b2b.md`, `docs/db-schema.md`

## Bağlam

Zincir/franchise işletmelerde imalathane şubeleri hammaddeden ara ürün ve mamul üretir; bunları depo/franchise şubelerine sevk eder. Bu, üç kavramın modellenmesini gerektirir:

1. **Satılabilir ürün** — POS'ta müşteriye satılan menü kalemi (`catalog.products`).
2. **Stok kalemi** — hammadde, ambalaj, ara ürün, mamul (stoklanır, satılmaz ya da imalattan sonra satılır).
3. **Reçete (BOM)** — bir stok kaleminin hangi bileşenlerden üretildiği.

Bu ADR, kardeş repo `onlinemenu.b2b`'de bu modelin nasıl bir "tanrı-nesne"ye dönüştüğünü kanıta dayalı olarak analiz eder ve `onlinemenu.tr`'de aynı hatanın **doğmamasını** sağlayan bir tasarım önerir.

---

## b2b Post-Mortem (neden bu tasarım)

Kanıtlar `onlinemenu.b2b` kaynak kodundan alınmıştır. Kök sorun: **satılabilir ürün, hammadde, ara ürün ve mamul tek bir `products` tablosuna sıkıştırıldı** ve aralarındaki fark `product_type` discriminator'ı + türe göre anlamı değişen bayraklarla yönetildi.

### 1. `product_type` discriminator + türe göre anlam değiştiren bayraklar

`models/product.go`'da tek tablo dört rolü taşır (`raw_material`, `intermediate`, `manufacturing`, `pos_sale`). Bayrakların anlamı türe bağlıdır — kod yorumları bunu itiraf eder:

```go
// NoInvoice ... This field is only meaningful for product_type='raw_material'.
NoInvoice bool
// BranchStockTracking ... Meaningful for raw_material and manufacturing types.
BranchStockTracking bool
// MfgStockTracking ... Meaningful for manufacturing type products.
MfgStockTracking bool
```

Sonuç: her `products` satırında kolonların yarısı o satır için anlamsız. "X türü için geçerli" yorumu, tablonun tek sorumluluğu olmadığının kanıtıdır. Yeni bir tür eklemek, tüm bayrakların yeniden yorumlanmasını gerektirir.

### 2. Üç paralel birim sistemi (aynı satırda)

Aynı üründe **üç ayrı birim temsili** bir arada yaşar:

```go
OrderUnit       UnitType        // şubenin sipariş verdiği birim
SaleUnit        UnitType        // takip/satış birimi
PackageQuantity decimal.Decimal // dönüşüm faktörü
// Legacy field for backward compatibility
UnitType UnitType               // eski birim alanı — hâlâ duruyor
AtomicUnit        *UnitType     // reçete/sayım birimi (paket içi adet)
AtomicPerSaleUnit *decimal.Decimal
```

`recipe_items.Unit` bir dördüncüsünü ekler ve dönüşüm **service katmanında, elle, yalnızca aynı aile içinde** yapılır:

```go
// unit conversion is handled in service layer (within same family: kg↔gr, l↔ml).
```

Kanonik birim olmadığı için her okuma bir uzlaştırma gerektirir. `atomic_per_sale_unit`'e bölme mantığı `stock/service.go` içinde **üç ayrı yerde** (`AdjustStock`, `RecipeWaste`, `ListStock`) kopyalanmıştır. Aynı formülün üç kopyası = üç kez ayrı ayrı bozulabilecek bir bakım tuzağı. Redundansın en çıplak kanıtı seed kodundadır: `seed_raw_materials.go` her hammadde için `OrderUnit`, `SaleUnit` ve `UnitType`'ı **aynı değere** atamak zorundadır (üç kolon, tek gerçek bilgi); kaynak veri yorumu bile `"TL/kg (or TL/unit)"` diyerek birim belirsizliğini itiraf eder.

### 3. Maliyet cascade'i (`CostSource=recipe_auto`)

```go
// CostSource controls whether CostPriceTL is maintained manually or auto-updated by recipe cascade.
CostSourceRecipeAuto CostSource = "recipe_auto" // Reçete cascade'inden otomatik güncellenir
```

Bir bileşenin maliyeti değişince reçeteyi kullanan tüm ürünlerin `cost_price_tl`'i otomatik geriye yayılır. Bu, "geçmiş maliyetin şu anki fiyata göre yeniden hesaplanması" demektir — üretim anındaki gerçek maliyet kaybolur, denetlenemez. Kullanıcının ifadesiyle: *"reçetelere girmeye başladık … derken baya durum kötüleşti."*

### 4. Görünürlük = satır-bazlı opt-in bayrağı + çift maliyetli DTO

Hammaddeyi şubenin görmesi `BranchStockTracking` bayrağıyla açılır (satır-bazlı opt-in). Dahası `recipe_dto.go` **hem** admin **hem** şube maliyetini aynı yanıt tipinde taşır:

```go
AdminUnitCost  *float64  // admin view
BranchUnitCost *float64  // branch view
```

Service katmanı role göre yanlış olanı `nil`'lemeye güvenir. `docs/lessons-from-b2b.md`'nin tam olarak uyardığı tuzak: `decimal.Decimal` bir struct olduğu için `omitempty` çalışmaz, "gizlenen" maliyet `"0"` olarak sızardı. Görünürlük, **alan yokluğuyla değil, opt-in filtrelemeyle** yönetildiği için her yeni call site'ta yeniden unutulabilir.

**Özet ders:** Tek tablo + discriminator + türe-bağlı bayraklar + paralel birimler + cascade + satır-bazlı görünürlük = birbirini besleyen karmaşıklık. Her biri diğerini zorunlu kılar.

---

## Karar

Dört ilke, doğrudan yukarıdaki dört bulguya karşılık gelir:

### İlke 1 — Satılabilir ürün ile stok kalemi ayrı varlıklar (discriminator yok)

- `catalog.products` **yalnızca satılabilir** kalemleri tutar (mevcut hâliyle kalır: `product_type` = `item|combo|service`, satış anlamında).
- Yeni `inventory.stock_items` tablosu **stoklanan** kalemleri tutar: hammadde, ambalaj, ara ürün, mamul.
- İki tablo arasında `product_type` gibi bir tür-tanrısı **yoktur**.

**`kind` kolonu — düz sınıflandırma, discriminator değil:** `stock_items.kind ∈ {raw, intermediate, packaging, finished}`. b2b'nin günahı, `product_type`'ın *diğer alanların anlamını değiştirmesiydi*. Buradaki `kind` düz bir sınıflandırmadır: **her kolon her satır için aynı anlama gelir**, hiçbir kolon `kind`'a göre "geçerli/geçersiz" olmaz. `kind` yalnızca listeleme/filtreleme ve invariant ifadesi içindir (ör. "`raw` bir tedarikçiye bağlanabilir, `intermediate` bir reçetenin çıktısıdır").

> **Reddedilen alt-alternatif:** "`kind` kolonunu hiç koymayıp satın-alınan/üretilen ayrımını reçete varlığından türetmek" (`NOT EXISTS (recipe)`). Reddedildi: kurulum sırasında reçetesi henüz girilmemiş kalem yanlış sınıfa düşer, "hammadde listele" pahalı bir anti-join olur ve invariant'lar (ör. mamul-satılabilir ürün bağı) ifade edilemez. Düz `kind` kolonu, b2b'nin dersini ihlal etmeden bunları çözer.

### İlke 2 — Tek kanonik birim + write-time dönüşüm

- Her `stock_items` satırının **tek** `canonical_unit`'i vardır. Paralel/legacy birim alanı yoktur.
- Reçete/işlem girişinde farklı birim kullanılırsa, **yazma anında bir kez** `unit_conversions` referans tablosuyla kanonik birime çevrilir; DB'ye daima kanonik birim yazılır.
- Okuma hiçbir zaman dönüşüm yapmaz (b2b'nin "her okumada 3 yerde böl" tuzağı yok).

```sql
CREATE TABLE unit_conversions (      -- tenant-bağımsız seed (kg↔g, l↔ml, ...)
    from_unit TEXT NOT NULL,
    to_unit   TEXT NOT NULL,
    factor    NUMERIC(18,6) NOT NULL, -- 1 from_unit = factor × to_unit
    PRIMARY KEY (from_unit, to_unit)
);
```

### İlke 3 — Maliyet cascade YOK: iş emri anında snapshot

- `stock_items` **hiçbir maliyet kolonu taşımaz** — cascade şemada imkânsız hâle gelir.
- Maliyet, yalnızca bir iş emri (work order) tamamlandığında `work_order_costs`'a **dondurulur** (snapshot). Geriye yayılım yoktur.
- **Snapshot kaynağı (cost_basis):** varsayılan `last_purchase` — o stok kalemi için en son teslim alınan `purchase_order_items.unit_price`. `moving_avg` (hareketli ortalama) Faz 3 seçeneği olarak açık bırakılır.
- **İç içe BOM (nested):** Bir ara ürün, kendi iş emrinde üretildiği anda `output_unit_cost`'u dondurulur. Daha sonra başka bir reçetede bileşen olarak kullanıldığında, bu dondurulmuş değer girdi maliyetidir — ağaç yukarı doğru **yeniden hesaplanmaz**. Her seviye kendi iş emrinde donar. b2b modelini tam da bu ayırır.

### İlke 4 — Görünürlük modül sınırı + OPA ile, satır bayrağıyla değil

- Hammadde, reçete ve iş emirleri `inventory` / `manufacturing` domain'lerinde yaşar.
- Şube rolleri (`cashier`, `waiter`, `kitchen`) bu route'lara **OPA permission'ı taşımaz** → veriyi hiç göremez. `BranchStockTracking` benzeri satır-bazlı opt-in bayrağı **yoktur**.
- Görünürlük "alan yokluğu" ile sağlanır (lessons-from-b2b): route erişimi yoksa, veri yoktur. Depo/imalat şubesinin `manager`/`warehouse` rolü ilgili permission'ı alır.

---

## Şema (öneri)

`inventory` ve `manufacturing` modüllerinde. Tüm tablolarda `tenant_id UUID NOT NULL` + `FORCE ROW LEVEL SECURITY` (mevcut desen). Para `NUMERIC(18,4)` + `currency`. Miktar `NUMERIC(18,3)`.

### stock_items (inventory) — kanonik stok kalemi

| Kolon | Tip | Not |
|---|---|---|
| id | uuid PK | UUID v7 |
| tenant_id | uuid | RLS |
| sku | text | tenant içinde UK |
| name | text | |
| kind | text | `raw`\|`intermediate`\|`packaging`\|`finished` — düz sınıflandırma |
| canonical_unit | text | **tek** birim; paralel alan yok |
| category | text | raporlama/filtre |
| is_active | bool | |

Maliyet kolonu **yoktur** (İlke 3). Türe-bağlı bayrak **yoktur** (İlke 1).

### Satılabilir mamul ↔ stok kalemi bağı

Hem üretilen hem satılan mamul (ör. franchise POS'unda satılan köfte paketi) iki kimliğe sahiptir: bir `stock_items` satırı (`kind=finished`, stoklanır/sevk edilir) **ve** bir `catalog.products` satırı (satılır). Bağ tek yönlü, opsiyonel bir FK'dir:

```
catalog.products.source_stock_item_id  uuid NULL  → inventory.stock_items(id)
```

Yalnızca stok kaleminden beslenen satılabilir ürünlerde doludur; saf hizmet/kombo ürünlerde `NULL`.

### recipes (manufacturing) — BOM başlığı, versiyonlu

| Kolon | Tip | Not |
|---|---|---|
| id | uuid PK | |
| tenant_id | uuid | |
| output_stock_item_id | uuid FK → stock_items | çıktı; `kind ∈ {intermediate, finished}` |
| version | int | değişiklik = yeni satır (immutable ruhu, DATA-002) |
| yield_qty | numeric(18,3) | bir parti kaç `output` üretir |
| yield_unit | text | = output.canonical_unit |
| is_active | bool | tek aktif versiyon |
| effective_from | timestamptz | |
| notes | text | |

Reçete değişikliği eski satırı güncellemez; **yeni versiyon** ekler. İş emirleri hangi versiyonu kullandığını `recipe_version` ile dondurur.

### recipe_items (manufacturing) — BOM satırı

| Kolon | Tip | Not |
|---|---|---|
| id | uuid PK | |
| recipe_id | uuid FK → recipes | |
| component_stock_item_id | uuid FK → stock_items | bileşen; `intermediate` olabilir → iç içe BOM |
| quantity | numeric(18,3) | **component.canonical_unit** cinsinden (write-time çevrilmiş) |
| waste_pct | numeric(5,2) NULL | satır-bazlı fire, opsiyonel |
| sort_order | int | |

Bileşen daima bir `stock_items`'tır — reçete **hiçbir zaman** `catalog.products`'a bakmaz (katalog BOM'dan izole kalır).

### work_orders (manufacturing) — üretim emri

| Kolon | Tip | Not |
|---|---|---|
| id | uuid PK | |
| tenant_id | uuid | |
| branch_id | uuid | imalat şubesi |
| warehouse_id | uuid FK → warehouses | `warehouse_type='imalat'` |
| recipe_id | uuid FK → recipes | |
| recipe_version | int | dondurulmuş versiyon |
| planned_qty | numeric(18,3) | |
| produced_qty | numeric(18,3) NULL | tamamlanınca dolar |
| status | text | `draft`\|`released`\|`in_progress`\|`completed`\|`cancelled` (tek `allowedTransitions`) |
| created_by | uuid | |
| released_at / completed_at | timestamptz NULL | |

Tamamlanınca: bileşenleri tüketir (`stock_movements` `out`), çıktıyı üretir (`stock_movements` `in`), `work_order_costs`'u yazar.

### work_order_items (manufacturing) — gerçek tüketim

| Kolon | Tip | Not |
|---|---|---|
| work_order_id | uuid FK | |
| component_stock_item_id | uuid FK → stock_items | |
| planned_qty / consumed_qty | numeric(18,3) | |
| unit | text | component.canonical_unit |

### work_order_costs (manufacturing) — MALİYET SNAPSHOT (anti-cascade)

| Kolon | Tip | Not |
|---|---|---|
| id | uuid PK | |
| work_order_id | uuid FK | |
| component_stock_item_id | uuid FK → stock_items | |
| unit_cost_snapshot | numeric(18,4) | dondurulmuş birim maliyet |
| quantity | numeric(18,3) | |
| line_cost | numeric(18,4) | = unit_cost_snapshot × quantity |
| cost_basis | text | `last_purchase` (varsayılan) \| `moving_avg` |
| currency | char(3) | |
| snapshotted_at | timestamptz | |

Çıktı birim maliyeti = `Σ line_cost / produced_qty`, iş emrinde dondurulur. **Asla geriye yayılmaz.** Raporlama bu dondurulmuş satırları okur.

---

## Mevcut stok temsili ile uzlaştırma (kritik)

`stock_items`'ı kanonik "stoklanan varlık" yapmak, bugün `products`'a bakan **tüm** referansları yeniden anahtarlar. Dört tablonun hepsi `stock_items`'a taşınır (yarısını taşıyıp yarısını bırakmak şemayı kendi içinde çelişkiye düşürür — bkz. DATA-006 db-schema güncellemesi):

- `stock_levels.product_id` → `stock_item_id`
- `stock_movements.product_id` → `stock_item_id`
- `shipment_items.product_id` → `stock_item_id`
- `purchase_order_items.product_id` → `stock_item_id` (hammadde satın alınır — bu bir `stock_item`, satılabilir ürün değil)

**İki paralel stok sistemi tuzağı (b2b hata modeli):** `catalog.products` bugün `is_stock_tracked` / `stock_quantity` / `auto_close_on_zero_stock` taşıyor. Öneri:

- **Stok modülü aktifken:** `stock_items` + `stock_levels` tek gerçek kaynaktır. Stok-takipli satılabilir ürün `source_stock_item_id` ile bağlanır; POS auto-close bağlı stok kaleminin seviyesinden beslenir. `products.stock_quantity` bu modda kullanılmaz.
- **Yalnızca POS (Stok modülü yok):** `products.is_stock_tracked` / `stock_quantity` basit sayaç olarak yeterlidir; materials/BOM yoktur.

> 🔎 **Fark edilen mevcut kod kokusu (görevle dolaylı ilgili, raporlanıyor):** Baseline'da stok üç yerde temsil ediliyor — `catalog.products.stock_quantity` (ürün üstünde tek sayı), `inventory.InventoryLevel` (Go domain'inde **şube**-scoped, warehouse'suz) ve `db-schema.md`'deki `STOCK_LEVELS` (**warehouse**-scoped). InventoryLevel (branch) ile STOCK_LEVELS (warehouse) çelişiyor. Bu ADR STOCK_LEVELS'i (warehouse) kanonik alır; `InventoryLevel`'ın warehouse'a taşınması ayrı bir uzlaştırma olarak **açık bırakılıyor** (aşağıda).

---

## Faz Önerisi

| Parça | Faz | Gerekçe |
|---|---|---|
| `stock_items` + `unit_conversions` + referans yeniden anahtarlama | **Faz 1** | ROADMAP Faz 1: "inventory — depo, stok seviyeleri, sevkiyat" bunu gerektirir |
| `catalog.products.source_stock_item_id` bağı | **Faz 1** | mamul satışı için |
| `recipes` + `recipe_items` (BOM tanımı) | **Faz 2** | manufacturing bugün stub |
| `work_orders` + `work_order_items` + `work_order_costs` | **Faz 2** | üretim + maliyet snapshot |
| POS-anı hammadde tüketimi (satış → bileşen düş) | **Faz 2** | b2b `RecipeWaste` karşılığı |
| MRP / talep planlaması (Temporal) | **Faz 3** | ROADMAP'te zaten Faz 3 |

---

## Değerlendirilen Alternatifler

- **b2b modeli (tek `products` + `product_type` + türe-bağlı bayraklar + cascade):** Reddedildi. Post-mortem'in tamamı gerekçesidir. Karmaşıklık kendini besliyor ve her yeni modül disiplini taşımıyor.
- **Discriminator yerine reçete-varlığından türetilen `kind`:** Reddedildi (İlke 1 altındaki alt-alternatif). Kurulum sırası, sorgu maliyeti ve invariant ifadesi sorunları.
- **Maliyeti `stock_items` üstünde tutup trigger ile güncelleme:** Reddedildi. Cascade'in şema düzeyindeki hâli; b2b'nin tam kopyası.
- **Reçeteyi doğrudan `catalog.products`'a bağlama:** Reddedildi. Katalogu BOM'a kuplajlar; imalat kavramlarını satış tablosuna sızdırır (b2b'nin başlangıç hatası).
- **Materials için ayrı, warehouse'suz basit sayaç (b2b `BranchStockTracking` modeli):** Reddedildi. Sevkiyat warehouse'tan çıkar; branch-scoped basit sayaç sevkiyatı modelleyemez.

## Açık Bırakılan Kararlar

1. **`InventoryLevel` (branch) ↔ `STOCK_LEVELS` (warehouse) uzlaştırması** — Go domain'i warehouse'a taşınmalı; ayrı bir refactor kararı.
2. **`cost_basis` varsayılanı** — `last_purchase` öneriliyor; `moving_avg` isteyen tenant'lar için Faz 3.
3. **Fire (`waste_pct`) + verim (`yield_qty`)** — reçete başlığında verim mi, satır başında fire mi, ikisi birden mi? Öneri: ikisi de opsiyonel; net formül Faz 2 implementasyonunda kesinleşir.
4. **`recipe` versiyonlama** — `is_active` bool mu, yoksa `(recipe_key, version)` çift-anahtarı mı? Öneri: `output_stock_item_id + version`; aktif versiyon işaretçisi implementasyonda.
