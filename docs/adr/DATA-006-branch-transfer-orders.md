# ADR-DATA-006: Şubeler Arası Sipariş (Branch Transfer Orders)

**Durum:** 📝 Taslak (Öneri / Proposed)
**Tarih:** 2026-07-04
**Kategori:** Veri / Event (DATA)
**İlgili:** DATA-005 (Reçete/BOM), DATA-002 (Event Immutability), `docs/lessons-from-b2b.md` (madde 2 — status makinesi), `docs/db-schema.md`

## Bağlam

ROADMAP'e göre "depolar ve imalathaneler belirli şubelere hizmet verir; öncelik/kural tabanlı sevkiyat yönlendirmesi tanımlanabilir." Bir franchise/şube, ihtiyacı olan malı bir imalat/depo şubesinden **sipariş eder**; sipariş onaylanır, hazırlanır/üretilir, sevk edilir ve teslim alınır.

`onlinemenu.tr`'de `SHIPMENTS` (sevkiyat) zaten var ama **talep tarafı** yok: sevkiyatı başlatan, önceliklendiren ve onaylayan bir sipariş belgesi eksik. Bu ADR `branch_transfer_orders` (BTO) belgesini, tek bir durum makinesini, `SHIPMENTS` ile ilişkisini ve `BRANCHES.supply_rules` ile yönlendirmeyi tanımlar.

## b2b Dersi (uygulanan)

`docs/lessons-from-b2b.md` madde 2: *"Status ~10 dağınık noktada elle atanıyordu. Geçiş kuralları tek bir `allowedTransitions` map'inde yaşasın; her mutasyon tek `Transition()` fonksiyonundan geçsin."* Bu ADR iki durum makinesi getirir (BTO + mevcut SHIPMENT) ve **her biri için tek bir `allowedTransitions` tablosu** + tek `Transition()` girişi zorunlu kılar. Ayrıca iki makinenin "received" gibi ortak kavramda çelişmemesi için **sahiplik** netleştirilir.

---

## Karar

### Belge modeli

Franchise/şube **talep eder** (requesting), imalat/depo **karşılar** (source). BTO talep tarafını temsil eder; fiziksel hareketi `SHIPMENTS` yürütür.

### branch_transfer_orders (inventory)

| Kolon | Tip | Not |
|---|---|---|
| id | uuid PK | UUID v7 |
| tenant_id | uuid | RLS |
| requesting_branch_id | uuid FK → branches | talep eden (franchise/şube) |
| source_branch_id | uuid FK → branches | karşılayan (`operation_type ∈ {imalat, depo}`) |
| status | text | durum makinesi (aşağıda) |
| priority | text | `normal`\|`urgent` |
| requested_delivery_date | date NULL | |
| note | text | |
| created_by | uuid | |
| submitted_at / approved_at | timestamptz NULL | |
| approved_by | uuid NULL | |

### branch_transfer_order_items (inventory)

| Kolon | Tip | Not |
|---|---|---|
| id | uuid PK | |
| transfer_order_id | uuid FK | |
| stock_item_id | uuid FK → stock_items | DATA-005'in kanonik stok kalemi (satılabilir ürün değil) |
| requested_qty | numeric(18,3) | talep |
| approved_qty | numeric(18,3) NULL | onayda belirlenir (kısmi onay mümkün) |
| shipped_qty | numeric(18,3) | SHIPMENTS'ten denormalize (sahibi shipment) |
| received_qty | numeric(18,3) | SHIPMENTS'ten denormalize (sahibi shipment) |
| unit | text | stock_item.canonical_unit |
| note | text | |

`shipped_qty` ve `received_qty` **türetilmiş** alanlardır; kaynağı `SHIPMENTS`'tir (aşağıdaki sahiplik kuralı).

---

## Durum Makinesi (tek `allowedTransitions`)

```
draft ──submit──▶ submitted ──approve──▶ approved ──fulfil──▶ fulfilling
                     │                       │                    │
                  reject                  cancel               shipped ──▶ received ──▶ closed
                     ▼                       ▼
                  rejected                cancelled
```

`allowedTransitions` tablosu (implementasyonda tek kaynak):

| from | to | tetikleyen |
|---|---|---|
| draft | submitted | requesting branch — gönder |
| draft | cancelled | requesting branch — iptal |
| submitted | approved | source branch — onayla (approved_qty set) |
| submitted | rejected | source branch — reddet |
| submitted | cancelled | requesting branch — geri çek |
| approved | fulfilling | source branch — hazırlığa/üretime başla |
| approved | cancelled | source branch — iptal (henüz sevk yok) |
| fulfilling | shipped | **SHIPMENT** tetikler (sevkiyat yola çıktı) |
| shipped | received | **SHIPMENT** tetikler (teslim alındı) |
| received | closed | otomatik: tüm kalemler tam teslim alındı |

- Geriye ve çapraz geçiş yok (b2b'nin ~10 dağınık atama sorununa karşı).
- Her mutasyon **tek** `Transition(bto, event)` fonksiyonundan geçer; doğrudan `status = ...` yasak (forbidigo/review kuralı, lessons madde 6).
- Tablo-bazlı regression testi: her (from→to) çiftinin izin/ret durumu assert edilir.

---

## SHIPMENTS ile İlişki ve "received" Sahipliği

**Bağlantı:** `SHIPMENTS`'e yeni FK eklenir:

```
shipments.transfer_order_id  uuid NULL  → branch_transfer_orders(id)
```

Onaylı bir BTO, `source_branch_id`'nin deposundan (`from_warehouse_id`) `requesting_branch_id`'ye (`to_branch_id`) **bir veya birden çok** `SHIPMENT` üretir. Kısmi sevkiyat mümkündür (bir BTO → N shipment).

**Sahiplik kuralı (iki durum makinesinin çelişmemesi için):**

- Fiziksel hareketin **tek sahibi `SHIPMENTS`**'tir. `shipment.status` (`draft|approved|in_transit|received|cancelled`) fiziksel gerçeği yönetir.
- `shipment.status → in_transit` olduğunda: bağlı BTO kalemlerinin `shipped_qty`'si güncellenir ve BTO `fulfilling → shipped`'e taşınır.
- `shipment.status → received` olduğunda: BTO kalemlerinin `received_qty`'si güncellenir; **teslim alma yalnızca burada yazılır**. Tüm BTO kalemleri tam teslim alındığında BTO otomatik `received → closed`.
- BTO **kendi başına** `received` set etmez; bu alan daima shipment olayından türetilir. Böylece iki makine tek bir "received" gerçeğinde buluşur, drift etmez.
- Stok hareketleri (`stock_movements`): sevk çıkışı `source` deposundan `out` (`reference_type='shipment'`), teslim alma `requesting` deposuna `in`. Kanonik varlık `stock_item_id`'dir (DATA-005 yeniden anahtarlaması).

---

## Yönlendirme — `BRANCHES.supply_rules` (jsonb)

`supply_rules` jsonb, bir talep eden şubenin hangi kaynaktan besleneceğini tanımlar. BTO oluşturulurken `source_branch_id` bu kurallardan çözülür (kalem kategorisine göre override + genel varsayılan):

```json
{
  "default_source_branch_id": "uuid-of-depo-or-imalat",
  "overrides": [
    { "match": { "category": "bread" }, "source_branch_id": "uuid-imalat", "priority": 1 },
    { "match": { "kind": "raw" },       "source_branch_id": "uuid-depo",   "priority": 2 }
  ]
}
```

- Çözümleme: kalemin `category`/`kind`'ına uyan en yüksek öncelikli override, yoksa `default_source_branch_id`.
- jsonb tutulur (esneklik) ama **şekli bu ADR'da sabittir**; doğrulama servis katmanında.
- `priority` (`normal|urgent`) BTO'da; kaynak tarafında iş sıralaması için kullanılır.

---

## Work Order ile İlişki (Faz 2)

BTO **Faz 1'de tek başına** çalışır: saf depo→şube stok transferi için manufacturing modeline ihtiyaç yoktur (kaynak deposunda mevcut stok sevk edilir).

**Faz 2 zenginleştirmesi:** `source_branch_id` bir imalat şubesiyse, talep edilen `stock_item` üretilen türdense (`kind ∈ {intermediate, finished}`) ve mevcut stok yetersizse — BTO onayı bir `work_order` (DATA-005) tetikleyebilir: eksik miktar üretilir, sonra sevk edilir. Bu bağ Faz 1'i bloklamaz; Faz 1 BTO'su üretim olmadan çalışır.

---

## Faz Önerisi

| Parça | Faz |
|---|---|
| `branch_transfer_orders` + `_items` + durum makinesi | **Faz 1** (ROADMAP: "şube→depo yönlendirme") |
| `shipments.transfer_order_id` bağı + received sahipliği | **Faz 1** |
| `supply_rules` çözümleme | **Faz 1** |
| BTO onayı → `work_order` otomatik tetikleme | **Faz 2** |

---

## Değerlendirilen Alternatifler

- **Ayrı sipariş belgesi olmadan doğrudan SHIPMENT oluşturmak:** Reddedildi. Talep/onay/önceliklendirme kaybolur; franchise'ın "istedim ama gelmedi" izi olmaz. b2b'de sipariş ve sevkiyat aynı şeye karışmıştı.
- **Status'u shipment ve BTO'da bağımsız tutmak:** Reddedildi. İki makine "received"da çelişir (lessons madde 2). Tek sahiplik (SHIPMENT) zorunlu.
- **`supply_rules`'u ayrı ilişkisel tablo yapmak:** Şimdilik reddedildi. jsonb + sabit şekil MVP için yeterli; kural sayısı büyürse Faz 2'de tabloya taşınabilir (ADR güncellenir).
- **Transfer kalemlerinin `catalog.products`'a bağlanması:** Reddedildi. Transfer edilen şey stok kalemidir (hammadde/ara ürün/mamul), satılabilir ürün değil — DATA-005 ilkesi.

## Açık Bırakılan Kararlar

1. **Kısmi onay + kısmi sevk kombinasyonu** — `approved_qty < requested_qty` ve çok-shipment birlikte: `closed`'ın tam mı yoksa `approved_qty` bazında mı tetikleneceği implementasyonda netleşecek. Öneri: `received_qty ≥ approved_qty` → closed.
2. **BTO iptali sevk sonrası** — `shipped` sonrası iptal yolu yok (yalnızca teslim alma/eksik teslim). İade akışı gerekiyorsa ayrı bir "transfer iadesi" belgesi (Faz 2+).
3. **Event yayımı** — `transfer_order.submitted.v1`, `.approved.v1`, `.received.v1` outbox event'leri (DATA-001) — subject/şema Faz 1 implementasyonunda tanımlanır.
