# ADR-DATA-009: Şube Bazlı Ürün Override'ları (Fiyat + Satılabilirlik)

**Durum:** ✅ Kabul (Faz 1'de uygulandı)
**Tarih:** 2026-09-20
**Kategori:** Veri / Event (DATA)
**İlgili:** DATA-004 (katalog delta sync), DATA-002 (event immutability), DATA-007 (şube-yerel maliyet), SEC-005 (şube kapsamlı rol), AUTH-001 (4 katmanlı yetkilendirme)

## Bağlam

Tenant sahibinden gelen gerçek gereksinim (Diver Street Food, bugünkü işleyiş):

- **Ürün yelpazesi şubeye göre değişir:** bir şube A markası kolayı, diğeri B markasını satar (tedarikçi serbestliği). Kalemin kendisi aynı kanonik ürün değildir — biri satılır, diğeri satılmaz.
- **Satış fiyatı şubeye göre değişir:** kira ve müşteri kitlesi farkı (Adapazarı/Serdivan < İzmit/Kırkpınar). Aynı ürün iki şubede iki fiyattan satılır.
- **Ayarı tenant sahibi yapar**, şube personeli değil.

Bugün katalog tamamen tenant genelindedir: `products.price_amount` tek fiyattır, `menus`/`menu_items` şube ayrımı taşır ama POS satışı menüden geçmez (bkz. `catalog/public.StaffPricer` yorumu), `product_channel_availability` yalnızca kanal (dine_in/takeaway/delivery) eksenindedir — şube ekseni yoktur.

## Karar

### 1. Tenant geneli katalog kalır; şube farkı ince bir override tablosuyla ifade edilir

`products` satırı tek kalır (tek kanonik ürün, tek reçete bağı, tek ÖKC bölüm eşlemesi). Şube farkı ayrı bir tabloda yaşar:

```
branch_product_overrides (
  tenant_id    UUID    NOT NULL,
  branch_id    UUID    NOT NULL,
  product_id   UUID    NOT NULL REFERENCES products(id) ON DELETE CASCADE,
  is_available BOOLEAN NOT NULL DEFAULT TRUE,
  price_amount BIGINT  NULL CHECK (price_amount IS NULL OR price_amount >= 0),  -- kuruş
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  PRIMARY KEY (tenant_id, branch_id, product_id)
)
```

**Satır yoksa tenant varsayılanı geçerlidir.** Bu, "override yok" ile "override var ama boş" arasındaki ayrımı korur ve N şube × M ürün kartezyen satırı üretmez. `price_amount NULL` + `is_available=true` = ürün bu şubede tenant fiyatıyla satılır (kullanıcı fiyatı temizlediğinde bu duruma döner); `DELETE` ise satırı tümden kaldırır.

`tenant_id` her satırda zorunludur, `FORCE ROW LEVEL SECURITY` + `app_runtime` politikaları vardır (SEC-001/SEC-002). PK `tenant_id` ile başlar, şube listeleme sorgusu bu indeksi kullanır; `product_id` üzerinde FK cascade için ayrı indeks vardır.

### 2. Menü/menu_items şubesiz kalır — ama öncelik sırası açıkça yazılır

`menus.branch_id` kolonu bu ADR'de **kullanılmaya devam eder** (misafir QR menüsü onun üzerinden çözülür) ancak şube fiyat farkının aracı **değildir**. İki mekanizma aynı anda var olduğu için öncelik tek yerde sabitlenir:

```
branch_product_overrides.price_amount  >  menu_items.price_override  >  products.price_amount
```

Gerekçe: override, tenant sahibinin doğrudan yönettiği eksendir ve satış anında bağlayıcıdır; menü fiyatı bir sunum/kampanya aracıdır. Sessiz bir `COALESCE` sırası yerine burada yazılı olması, ileride birinin sırayı fark etmeden değiştirmesini engeller.

**Menü katmanı yalnızca misafir (QR) yolunda devrededir.** POS satışı ve ürün listeleme menüden hiç geçmez (gerekçe `catalog/public.StaffPricer` yorumunda: menü tanımlamamış bir tenant tezgâhta hiç satış yapamaz hale gelirdi), dolayısıyla o yollarda zincir kısalır:

```
branch_product_overrides.price_amount > products.price_amount
```

İki yolun tek farkı taban fiyatın nereden geldiğidir; override her ikisinde de aynı önceliktedir. Staff yolunu menüye bağlamak bir ürün kararıdır, bu ADR'nin kapsamı değildir.

### 3. Satılabilirlik iki eksenin **AND**'idir

Etkin satılabilirlik = `product_channel_availability` (kanal ekseni, opt-out) **VE** `branch_product_overrides.is_available` (şube ekseni, opt-out). Biri diğerinin yerine geçmez: bir ürün "delivery'ye kapalı" ile "İzmit şubesinde satılmıyor" aynı şey değildir.

### 4. Etkin fiyat tek bir yerde çözülür

Fiyat çözümlemesi `catalog/repo.storefrontMenuItemsCTE` ve `PriceCatalogProducts` içinde, **override join'i CTE'nin kendisine eklenerek** yapılır. Böylece misafirin gördüğü menü (`ListMenuRows`), misafir sepeti (`PriceProducts`/`PriceCart`) ve POS satışı (`PriceCatalogProducts`/`PriceStaffCart`) aynı satırı okur — sunulan fiyattan farklı bir fiyattan tahsilat yapılması yapısal olarak mümkün değildir.

Bu yüzden `public.StaffPricer` sözleşmesi şube parametresi alacak şekilde genişletilir:

```go
PriceStaffCart(ctx, tenantID, branchID uuid.UUID, lines []StaffCartLine) ([]PricedLine, error)
```

Ayrı bir `EffectivePricer` arayüzü **açılmaz**: ikinci bir kapı, ikinci bir çözümleme kuralı demektir. `branchID == uuid.Nil` "şube belirtilmedi → tenant varsayılanı" anlamına gelir ve geriye uyumluluğu korur.

### 5. Okuma uçları `branch_id` ile şubeleşir, varsayılan geriye uyumludur

`GET /catalog/products` ve `GET /catalog/categories/{id}/products` opsiyonel `branch_id` alır. Verildiğinde: etkin fiyat döner, `is_available=false` ürünler listeden düşer ve yanıt `branch_price_overridden: bool` taşır (admin/POS'un "bu fiyat şubeye özel" rozetini basabilmesi için). Verilmediğinde davranış bugünküyle aynıdır.

**Ürün düzenleme yolu `branch_id` geçmez.** Geçseydi, admin ürün formu şube fiyatını okuyup `PUT /products/{id}` ile tenant fiyatı olarak geri yazardı. Override yönetimi yalnızca kendi uçlarından yapılır.

### 6. Yetkilendirme

- **Yazma:** yeni `catalog.branch_override.manage` eylemi. `authz.rego`'daki `default allow = false` + manager wildcard sayesinde yalnızca zincir yöneticisi (tenant sahibi) geçer; kasiyer/şef/garson 403 alır. Eylem bilerek `catalog_read_actions` kümesine **eklenmez**.
- **Okuma:** mevcut `catalog.product.read` + AUTH-001 Layer 3 şube guard'ı. Şube kapsamlı bir principal başka bir `branch_id` sorarsa 403 (`branch_forbidden`) — SEC-005'teki desenin aynısı.

### 7. Event: `catalog_outbox` + `branch_override.changed`

Override değişikliği (upsert ve delete) aynı transaction içinde `catalog_outbox`'a immutable bir event yazar (DATA-001/DATA-002): `catalog.branch_override.changed.v1`. Payload override'ın yeni halini taşır; silme `deleted: true` ile ifade edilir. Satır **güncellenmez**, her değişiklik yeni event'tir.

Bu, catalog modülünün ilk outbox tablosudur; dispatcher tablo listesine (`cmd/api/main.go newOutboxConfig`) eklenir.

### 8. DATA-004 (delta sync) ile ilişki

Şube override'ı katalog içeriğini değiştirir, dolayısıyla **katalog versiyonunu ilerletmelidir**: aksi halde edge/POS önbelleği eski fiyatı taşımaya devam eder. Ancak DATA-004'ün `catalog_version` sayacı bugün kodda **mevcut değildir** (yalnızca ADR'de tarif edilmiştir). Bu yüzden bu ADR sayacı uydurmaz: `branch_override.changed` event'i bugünden yayımlanır ve versiyon mekanizması hayata geçtiğinde sayacı ilerleten tetikleyicilerden biri olarak bağlanır. Faz 2 delta event listesine `catalog.branch_override.changed.v1` eklenir.

## Açık Bırakılan Kararlar

1. **Modifier `price_delta` tenant genelidir.** Şube bazlı modifier fiyatı (ör. "ekstra peynir İzmit'te daha pahalı") bu ADR'de **kapsam dışıdır**. Gerekirse `branch_modifier_overrides` aynı desenle açılır; bugün ihtiyaç raporlanmadı ve her ekseni önden açmak şemayı gereksiz büyütür.
2. **Kategori/menü düzeyinde toplu override yok.** "Tüm içecekleri bu şubede %10 zamla sat" gibi bir kural ürün başına satır gerektirir. Toplu işlem bugün admin UI'nın çoklu yazma döngüsüdür; kural tabanlı fiyatlandırma (DATA-007'nin `supply_policies` zaman ekseni gibi) ayrı bir karardır.
3. **Zaman ekseni yok.** Override'ın `effective_from`'u yoktur; değişiklik anında geçerlidir. Geçmiş fiyat izi sipariş satırının kendi snapshot'ında (`order_items.product_price_amount`) ve outbox event akışında durur.
4. **Maliyet ekseni ayrıdır.** Bu ADR yalnızca **satış** fiyatını kapsar. Şube-yerel **maliyet** DATA-007'nin konusudur ve iki eksen birbirine bağlanmaz.

## Değerlendirilen Alternatifler

- **Şube başına ayrı `products` satırı:** Reddedildi. Ürün kimliği patlar; reçete (DATA-005), ÖKC bölüm eşlemesi ve raporlama şube sayısına bölünür, "Adana Kebap ne kadar sattı" sorusu zincir genelinde cevapsız kalır.
- **`products.price_amount`'u JSONB şube haritasına çevirmek:** Reddedildi. RLS ve CHECK kısıtları kolon üzerinde çalışır; JSONB'ye taşımak fiyatı tip güvencesinden ve indekslemeden çıkarır.
- **Şube farkını `menus`/`menu_items` üzerinden çözmek:** Reddedildi. POS satışı menüden geçmez (bir tenant hiç menü tanımlamamış olabilir, bkz. `StaffPricer` yorumu); şube fiyatı menüye bağlanırsa menüsüz tenant'ta hiç uygulanmaz.
- **`product_channel_availability`'ye `branch_id` eklemek:** Reddedildi. O tablo kanal eksenidir; şube eksenini aynı satıra sıkıştırmak "kanal × şube" kartezyenini ve iki farklı opt-out anlamını tek kolona yıkardı. Fiyatı da taşıyamaz.
- **Etkin fiyatı her çağrı yerinde ayrı hesaplamak:** Reddedildi. `storefrontMenuItemsCTE`'nin kendi yorumunun söylediği hata: gezinme ve tahsilat farklı çözerse, müşteriye gösterilmeyen bir fiyattan tahsilat yapılır ve hiçbir uç testi bunu görmez.
