# b2b → Online Menu Aktarım Planı (Diver Street Food)

**Durum:** TASLAK — prod'a dokunulmadı. Dev'de (şema kopyası + prod'a benzer başlangıç durumu) kuru koşu,
gerçek koşu, tekrar koşu ve geri alma denendi. **Tarih:** 2026-09-20
**Dosyalar:** `deploy/scripts/b2b-export.sql` (kaynak, yalnız SELECT) · `deploy/scripts/import-from-b2b.sql` (hedef).

## 1. Kapsam — ne aktarılır, ne aktarılmaz

b2b bir **imalat→şube tedarik/sevkiyat** sistemidir; kasa satışı yok (`pos_sessions` = 0). Menüye dönüşebilecek tek
küme `product_type='pos_sale'` ürünleridir.

| Aktarılır | Aktarılmaz (bu aktarımda) |
|---|---|
| 4 satış şubesi (+ istenirse İmalat Merkezi) | 81 `raw_material` + 3 `intermediate` + 45 `manufacturing` ürün (B2B stok kalemi; Faz 2 reçete/stok) |
| 18 `pos_sale` ürünü, 3 kategori (isimden türetilir) | Siparişler (330), sevkiyatlar, faturalar, stok/sayım, denetim, İK/bordro |
| 20 şube fiyat farkı → `branch_product_overrides` | b2b parolaları (bcrypt; Keycloak'a taşınamaz) |
| Personel → **API ile** (bkz. §6) | Maliyet fiyatları (`cost_price_tl`) — export'a bilerek alınmaz |

## 2. b2b veri haritası (tablo.alan)

- **Firma:** `tenants` (1 satır: Diver Street Food) · KDV: `tax_rates` (%1/%10/%20).
- **Şube:** `branches` (code, name, `type` = `manufacturing`|`sales`, address, is_active) · adres kısa etiket (ör. "İzmit Merkez").
- **Ürün:** `products` (sku, name, `category` enum, `sale_unit`, `sale_price_tl` **KDV-dahil**, `tax_rate` %, `product_type`).
- **Şube fiyatı:** `branch_product_prices` (branch_id, product_id, sale_price_tl; UNIQUE şube×ürün) — yalnız `pos_sale` için.
- **Şube-yerel ürün:** `pos_branch_products` (0 satır) · **Varyant/opsiyon:** `pos_modifier_groups/options`, `pos_product_modifier_groups` (0 satır).
- **Şube-ürün serbestliği:** b2b'de **tablo yok** — her `pos_sale` ürün her şubede satılır. Şube-yerel ürün özelliği (`pos_branch_products`) hiç kullanılmamış.
- **Personel:** `users` (role, branch_id) — roller: admin, branch, branch_staff, manufacturing, driver, auditor (+`pos_cashier`, 0 kişi).

## 3. Prod b2b sayıları (yalnız SELECT ile)

- **Şubeler (5):** Adapazarı (ADA), İzmit (IZM), Kırkpınar (KRK), Serdivan (SRD) — satış; İmalat Merkezi (IMALAT) — imalat. Şehir alanı yok, adres etiketi var.
- **pos_sale:** 18 aktif ürün, hepsi KDV %10, birim adet, SKU'lar tekil (çakışma yok), açıklama/barkod boş.
- **Kategori:** b2b `category` anlamsız — 13 `meat`, 5 `other`; hepsi burger.
- **Şube fiyatı:** 22 satır. **2'si gereksiz** (ADA/SRD × UMAMİ = taban fiyat) → 20 satır aktarılır: İzmit 10, Kırkpınar 10, Adapazarı 0, Serdivan 0.
- **Fiyat farkı:** 10 üründe İzmit = Kırkpınar = taban **+10…+20 TL**. Örnekler: American Smash 470 → 490; Double Cheese 560 → 575; Klasik hamburger 430 → 440. Kalan 8 üründe (tavuk burgerler, Kıds, Vejeteryan) fark yok.
- **Personel (aktif 14):** admin 2 · branch 6 · branch_staff 2 · manufacturing 2 · driver 1 · auditor 1. Şube dağılımı: ADA 1, IZM 1, KRK 3, SRD 3, IMALAT 1; şubesiz 5 (admin 2, auditor, driver, 1 imalat).

## 4. Online Menu hedef durumu (prod, yalnız SELECT ile)

- 1 tenant (`diverstreetfood`, starter) · **1 şube "Ana Şube"** (slug yok, `branch_settings` satırı **yok**) · 3 masa + 1 bölge.
- 2 kategori (Ana Yemekler, İçecekler) · 3 ürün (Adana Kebap, Ayran, Lahmacun — demo; 32000/4000/9000 kuruş, %10) · 0 menü / modifier.
- 1 kişi + 1 üyelik (Yönetici). Sistem rolleri: Yönetici, Shift Müdürü, Kasiyer, Garson, Mutfak, Bar, Depo, Şoför (klon yok, hepsi sistem satırı).
- `branch_product_overrides` (catalog/000003, ADR-DATA-009): PK (tenant_id, branch_id, product_id), `price_amount` NULL = tenant fiyatı, `is_available`.
- `branches`: slug için (tenant_id, slug) kısmi UNIQUE var; **`categories`/`products` üzerinde ad/SKU UNIQUE'i yok**.

## 5. Eşleme kararları

| b2b | Online Menu | Not |
|---|---|---|
| şube `sales` | `branches` `operation_type='fast_food'`, slug = adın ASCII hali (`adapazari`, `izmit`, `kirkpinar`, `serdivan`) | ownership: ADA/IZM/KRK = `franchise`, SRD = `sube` (§10 karar 3) |
| şube `manufacturing` | `operation_type='imalat'`, ad "İmalat Merkezi (Serdivan)", `sube` | yalnız `include_manufacturing=1` ile |
| şube adres etiketi | `branches.address` | `city/district` boş kalır (b2b'de yok) |
| — | `branch_settings` (yeni şubeler için) `fiscal_device_type='mock'`, `tax_rate_bps=1000` (`business_day_offset=240` migration varsayılanı) | prod şu an mock ÖKC; `'none'` prod'da yasak |
| `products.sale_price_tl` (KDV-dahil TL) | `products.price_amount` = `round(TL×100)` kuruş, `currency='TRY'` | **Tenant fiyatı = b2b tabanı = en düşük fiyat.** Veri bunu destekliyor: ADA/SRD taban fiyattan, İzmit/Kırkpınar hep ≥ taban. |
| `tax_rate` % | `tax_rate_bps` = %×100 (10 → 1000) | tümü %10 |
| `sale_unit` `piece` | `unit='adet'` (`kg`→`kg`, `l`→`lt`; bilinmeyen birimde script durur) | |
| şube fiyatı ≠ taban | `branch_product_overrides(is_available=true, price_amount)` | eşitse satır yazılmaz; her ürün her şubede satılır (b2b'de kapama kavramı yok) |
| varyant/opsiyon | — | b2b'de 0 kayıt → **yapılacak iş yok** (modifier_groups'a çeviri gerekmez) |
| kategori | `Burgerler` (11) · `Tavuk Burgerler` (5) · `Çocuk Burgerler` (2, `Kıds*`) | isim kuralı script'te; sort 30/40/50 (Soru 3) |
| — | `menus` / `menu_items` **aktarılmaz** | prod'da 0 menü; POS satışı ve QR menüsü menüsüz çözülür, şube fiyatı override'la yürür (ADR-DATA-009 §2) |
| personel | Keycloak + `persons/memberships` — **API** | §6 |

**Çakışmalar/tuzaklar**
- **Fiyat KDV-dahil:** b2b "KDV DAHİL" der; Online Menu POS'u `unit_price_amount`'u olduğu gibi tahsil eder (rapor: `GrossSales = Σ qty×fiyat`) → aynı anlam, dönüşüm yok. **Ama** `billing_service.buildItems` KDV'yi fiyatın *üstüne* ekler (`total = net + vergi`). Fatura kalemleri istemciden gelir (`POST` gövdesi; bugün hiçbir POS/admin akışı check satırından beslemiyor) → **canlı şişme yok, gelecek risk**: bu fiyatlar KDV-dahil olarak faturaya girerse toplam %10 fazla çıkar (Soru 4).
- SKU'lar düzensiz (`burger001`, `002`, `kıds`, `0809`); birebir aktarılır, ADA/SRD'de aynı ürün farklı SKU **yok**.
- Aynı ada sahip import-dışı ürün varsa script **durur** (çift kayıt yerine).
- Doğrudan SQL `catalog.branch_override.changed.v1` outbox olayı üretmez (ADR-DATA-009 §7); fiyat okuma anında CTE ile çözülür, POS/QR hemen görür — kenar önbelleği (DATA-004) henüz yok.

## 6. Personel aktarımı (SQL değil, API)

`POST /v1/identity/{tid}/staff` `{full_name, email, branch_id?, role_id}` — Yönetici JWT'siyle; Keycloak hesabı + davet e-postası oluşur.
Şube-kapsamlı roller (Kasiyer, Garson, Mutfak, Bar, Şoför, Depo, Shift Müdürü) **`branch_id` zorunlu** (SEC-005 tetikleyicisi NULL'ı reddeder).

| b2b rol (kişi) | Öneri: OM rolü | Şube |
|---|---|---|
| admin (2) | Yönetici (`…0006`) | NULL (zincir) |
| branch (6) | Shift Müdürü (`…0002`) | kendi şubesi |
| branch_staff (2) | Kasiyer (`…0001`) | kendi şubesi |
| manufacturing (2) | Depo (`…0007`) | İmalat (Soru 2) |
| driver (1) | Şoför (`…0003`) | İmalat, b2b'de şubesiz |
| auditor (1) | karşılığı yok | atla veya Yönetici (Soru 5) |

Kaynak liste, repoya girmeyecek biçimde alınır: `SELECT full_name, email, role, branch_id FROM users WHERE deleted_at IS NULL AND is_active` → git-dışı CSV → döngüyle API çağrısı. Parolalar taşınmaz; kişi davet e-postasıyla belirler.

## 7. Kullanıcıya sorulacak kararlar (≤5)

1. **"Ana Şube" hangi gerçek şubeye dönüşsün** (`existing_branch_code=ADA|IZM|KRK|SRD`) ya da dokunulmasın (`NONE` → 5 şube olur)? 3 masa ve POS geçmişi o şubeye bağlı kalır.
2. **İmalat Merkezi** OM'ye alınsın mı (depo/şoför personeli + ileride sevkiyat için gerekir; POS şube seçicisinde görünür)?
3. **Kategori düzeni:** önerilen 3 kategori mi, tek "Burgerler" mi? Demo katalog (Adana Kebap, Ayran, Lahmacun + 2 kategori) pasifleştirilsin mi (silmek yerine önerilir)?
4. **Fiyat yorumu:** `price_amount` KDV-dahil kalsın (b2b + POS ile tutarlı, önerilen) ve fatura modülü bunu bilip KDV'yi içeriden ayırsın mı, yoksa fiyatlar KDV-hariç mi girilsin (tüm fiyatlar %10 düşer)? Bu, sayıyı değiştiren tek karar.
5. **Personel:** rol eşlemesi (özellikle `branch`→Shift Müdürü, `auditor`, şubesiz `driver`/`manufacturing`) uygun mu; davet e-postaları hemen mi gitsin?

## 8. Çalıştırma sırası

1. `catalog/000003` (+`000004`) migration'ı prod'a uygulanmış olmalı (`deploy/scripts/migrate.sh verify`); script yoksa **durur**.
2. Prod DB yedeği (`deploy/backup`), sonra b2b export: komut `b2b-export.sql` başlığında (çıktı repoya girmez).
3. Kuru koşu (varsayılan `dry_run=1`, ROLLBACK): `psql "$DSN" -v ON_ERROR_STOP=1 -v tenant_slug=diverstreetfood -v existing_branch_code=<KOD|NONE> -v include_manufacturing=<0|1> -v payload="$(cat b2b-export.json)" -f deploy/scripts/import-from-b2b.sql` — `DSN` = `seed-first-tenant.sh` deseniyle `app_migrator@127.0.0.1:5433`; sunucuda psql yoksa `postgres:17-alpine` ile.
4. Özet çıktısını doğrula (beklenen: 4 şube, 3 kategori, 18 ürün, 20 override; İzmit/Kırkpınar 10'ar) → aynı komut `-v dry_run=0` ile.
5. Tekrar koşu güvenlidir (yeni satır yok; yalnız ürün fiyatı/KDV/aktiflik ve override fiyatı tazelenir; `is_available` ve kullanıcı düzenlemeleri korunur). b2b'de fiyat değişirse geçiş süresince yeniden export + koşu yeterli.
6. Doğrula: `GET /catalog/products?branch_id=<İzmit>` etkin fiyatı (ör. American Smash 49000) döndürmeli; Adapazarı 47000.
7. Personel: §6, Yönetici JWT'siyle davetler. Demo katalog kararı (Soru 3) admin panelinden. Şube ürün serbestliği: b2b'de kayıt yok (`pos_branch_products` 0) → aktarılacak bir şey yok; şubede satılmayan ürünleri (marka farkı içecekler vb.) müşteri söyledikçe admin'den `is_available=false` girilir.
8. Kesim sonrası b2b tarafında POS fiyat girişi kapatılır; iki yönlü senkron yoktur.

## 9. Geri alma

- **İlk satıştan önce:** aynı komut `-v rollback=1 -v dry_run=0` — ürünler, yeni kategoriler, yeni şubeler + ayarları deterministik (uuid v5) id'yle silinir; **override'lar id'siz olduğundan** "import edilen ürün × import edilen şube" kümesi topluca silinir (sahibin sonradan girdiği override dahil). Ürün/şube sipariş satırlarına bağlandıysa FK hatasıyla **durur** (fail-closed) → silmek yerine `is_active=false`.
- **"Ana Şube"** yeniden adlandırılmış kalır ve import'un oluşturduğu `branch_settings` satırı silinmez: gerekirse elle `UPDATE branches SET name='Ana Şube', slug=NULL WHERE id=…` + o şubenin `branch_settings` satırı.
- Tam geri dönüş: adım 2'deki yedek.
- Personel daveti geri alınamaz sayılır: Keycloak kullanıcısı + membership ayrı silinir (DELETE membership, Keycloak'tan devre dışı).

## 10. Uygulandı 2026-09-20

**Kararlar:** (1) "Ana Şube" → Serdivan (`existing_branch_code=SRD`, aynı id, 3 masa yerinde). (2) İmalat Merkezi alındı: "İmalat Merkezi (Serdivan)", `imalat`, `sube`.
(3) Adapazarı/İzmit/Kırkpınar `franchise`+`fast_food`; Serdivan `sube`. (4) 3 kategori; demo ürünler `is_active=false` (`-v deactivate_demo=1`).
(5) Fiyatlar KDV-dahil, `tax_rate_bps=1000`. (6) Personel: davet ucu ÇAĞRILMADI, komutlar üretildi (aşağıda).

**Sıra:** dev'de kuru+gerçek+tekrar+geri alma → prod yedeği (`/root/backups/onlinemenu-pre-b2b-import-20260920.dump`) → prod kuru koşu (ROLLBACK, değişiklik yok doğrulandı) → prod gerçek koşu (`app_migrator`, tek transaction, `COMMIT`).

**Prod'da öncesi → sonrası (SELECT ile doğrulandı):** şube 1 → **5** (5'inde `branch_settings` mock/1000) · kategori 2 → **5** (Burgerler 11, Tavuk 5, Çocuk 2 aktif ürünle; eski 2'sinde aktif ürün 0) ·
ürün 3 → **21** (18 aktif + 3 pasif demo) · override 0 → **20** (İzmit 10, Kırkpınar 10; American Smash 49000, aralık 44000–58500 kuruş, hepsi `is_available=true`). Başka tabloya dokunulmadı.

**Doğrulanamayanlar:** Admin UI/API testi yapılamadı — `deploy/.env.diverserver.local` içindeki `FIRST_ADMIN_PASSWORD` Keycloak'ta "Invalid username or password" verdi (parola 09-15'ten sonra değişmiş); prod'da doğrudan-grant istemcisi yok, kilitlenmemek için tek denemeyle durduruldu. Etkin fiyat SQL düzeyinde doğrulandı (İzmit American Smash 49000). Kalan: yönetici parolasıyla `GET /catalog/branches/{izmit}/product-overrides` (10 satır) ve Şube Fiyatları sayfasında İzmit rozetleri.

**Personel komutları:** `deploy/scripts/invite-staff-from-b2b.sh` yalnız `curl` üretir, çalıştırmaz. Çıktı (git dışı): `deploy/b2b-staff.local.commands.sh` — 12 davet
(Yönetici 1, Shift Müdürü 6, Kasiyer 2, Depo 2, Şoför 1; auditor atlandı, 1 kişi OM'de zaten var). Koşmadan önce `TOKEN` (Yönetici bağlam token'ı) gerekir.

**Geri alma notu:** `-v rollback=1` demo ürünleri yeniden aktifleştirmez ve "Serdivan" adını geri çevirmez (elle).
