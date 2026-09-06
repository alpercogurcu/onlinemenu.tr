# Admin Katalog UX Yeniden Tasarımı — Uygulama Planı

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Admin panelinde ürün ve seçenek grubu yönetimini tek akışa indirmek: ürün detay sayfası (bilgiler + seçenek grupları + menüler), seçenek grubu detay sayfası (kural + satır içi seçenek düzenleme + müşteri önizlemesi), aranabilir/filtrelenebilir ürün listesi, her silmede onay.

**Architecture:** Yalnız admin frontend (Next.js 16 App Router, shadcn, TanStack Query, next-intl) + bir küçük backend ucu (`GET /modifier-groups/{id}/products`). Backend katalog API'si zaten ürün/grup/seçenek CRUD ve ürün↔grup atamasını sağlıyor. Yeni sayfalar dinamik rota (`[id]`) ve `new` ile aynı bileşeni paylaşır. Terminoloji: kullanıcıya dönük her yerde "Modifier" → **"Seçenek grubu"**, "modifier" → **"seçenek"**.

**Tech Stack:** Next.js 16, React 19, shadcn/ui (new-york, Radix), TanStack Query 5, next-intl, vitest + RTL; Go chi + pgx (T1).

**Spec:** Claude Design canvas'ı https://claude.ai/code/artifact/8f299ea6-b991-42bb-9a5e-8d7de2241d7a (3 artboard: ürün listesi + silme onayı, ürün detayı, seçenek grubu detayı) ve bu dosya. Mevcut kod: `web/apps/admin/src/app/(main)/catalog/*`, `src/hooks/use-catalog.ts`, `src/components/catalog/menu-items-sheet.tsx`.

## Global Constraints

- Para: kuruş `int64`; TL giriş/çıkışı yalnız `src/lib/money.ts` (`formatKurus`, `parseLiraToKurus`, `formatKurusForInput`) ile.
- Backend sözleşmeleri (değişmez): ürün `POST/PUT /api/v1/catalog/products[/{id}]` gövdesi `{category_id, name, description, price_amount, currency, unit, tax_rate_bps, is_active (PUT), sort_order}`; grup `POST/PUT /modifier-groups[/{id}]` `{name, selection_type: "single"|"multiple", min_selections, max_selections (null = sınırsız), is_required, sort_order}`; seçenek `POST /modifier-groups/{gid}/modifiers`, `PUT/DELETE /modifier-groups/{gid}/modifiers/{id}` `{name, price_delta, is_active, sort_order}`; atama `POST /products/{id}/modifier-groups` `{group_id, sort_order}`, `DELETE /products/{id}/modifier-groups/{gid}`, `GET /products/{id}/modifier-groups` → `string[]` (grup id'leri); menü kalemi `POST /menus/{id}/items` `{product_id, price_override, is_active}`, `DELETE /menus/{id}/items/{productID}`, `GET /menus/{id}/items` → `[{menu_id, product_id, price_override, is_active}]`.
- `selection_type` değerleri backend'de doğrulanır; UI "Tek seçim" = `single` (max 1), "Birden fazla" = `multiple`.
- Tüm metinler next-intl `catalog` namespace'inden (Task 2 tüm anahtarları ekler; sonraki görevler `tr.json`'a **dokunmaz**). Kod/yorum İngilizce.
- Her silme `ConfirmDialog` ile onaylanır (alert-dialog). Yıkıcı buton `variant="destructive"`.
- Erişilebilirlik: her ikon-buton `aria-label`; form alanlarında görünür `Label`; hata metni alanın altında; odak halkaları korunur; dokunma hedefi ≥ 36px (masaüstü admin).
- Testler: her görev vitest + RTL; `@/lib/api` mock deseni `src/test/dashboard.test.tsx`'teki gibi (NextIntl + QueryClient sarmalayıcı). `task web:test`, `task web:typecheck`, `task web:lint` (0 hata, yeni uyarı yok).
- Commit Türkçe `tip(alan): açıklama`, AI attribution yok, push yok. Dal: `feat/admin-katalog-ux`.
- Aynı çalışma ağacında paralel görevler: dosya kümeleri ayrık; `git add` yalnız kendi yolların.

### Rulings

- **R1** "Grubu kullanan ürünler" için yeni backend ucu (T1) eklenir; N+1 istemci taraması yapılmaz.
- **R2** Ürün listesinde arama/filtre istemci tarafında (pilot ölçeği); seçenek sütunu `useQueries` ile ürün başına `GET /products/{id}/modifier-groups` (önbellekli). Backend liste zenginleştirmesi backlog'a.
- **R3** Sıralama için sürükle-bırak yok; seçeneklerde ▲▼ butonlarıyla `sort_order` güncellenir (kütüphane eklenmez).
- **R4** Yeni seçenek grubu doğrudan ürün detayından oluşturulabilir (combobox içinde "Yeni grup: …" → POST + atama), detay düzenlemesi grup sayfasında.
- **R5** Sidebar etiketi "Modifier Grupları" → "Seçenek Grupları" (rota `/catalog/modifiers` kalır).

---

## Dosya Haritası

| Görev | Dosya |
|---|---|
| T1 | `backend/internal/modules/catalog/{repo/modifier_repo.go, service/modifier_service.go, http/handler.go}`, repo integration test |
| T2 | `src/components/ui/{alert-dialog,textarea,popover,command}.tsx` (shadcn CLI), `src/components/catalog/confirm-dialog.tsx`, `src/components/catalog/money-input.tsx`, `src/hooks/use-catalog.ts`, `src/types/index.ts`, `src/messages/tr.json` (`catalog` namespace), `src/components/layouts/admin-sidebar.tsx` (etiket) |
| T3 | `src/app/(main)/catalog/products/page.tsx`, `src/components/catalog/product-row-actions.tsx`, `src/test/products-page.test.tsx` |
| T4 | `src/app/(main)/catalog/products/[id]/page.tsx`, `src/app/(main)/catalog/products/new/page.tsx`, `src/components/catalog/product-form.tsx`, `product-modifier-groups-card.tsx`, `product-menus-card.tsx`, `src/test/product-detail.test.tsx` |
| T5 | `src/app/(main)/catalog/modifiers/page.tsx`, `src/app/(main)/catalog/modifiers/[id]/page.tsx`, `src/app/(main)/catalog/modifiers/new/page.tsx`, `src/components/catalog/modifier-group-form.tsx`, `modifier-options-editor.tsx`, `modifier-preview.tsx`, `src/test/modifier-group-detail.test.tsx` |

---

### Task 1: Backend — grubu kullanan ürünler ucu

**Files:**
- Modify: `backend/internal/modules/catalog/repo/modifier_repo.go` (`ListProductIDsByGroup`)
- Modify: `backend/internal/modules/catalog/service/modifier_service.go` (`ListGroupProducts`)
- Modify: `backend/internal/modules/catalog/http/handler.go` (route + handler)
- Test: mevcut catalog repo integration test dosyasına (testcontainers scaffold'u) bir test; handler için `httptest` ile 400 (bozuk id) testi.

**Interfaces:**
- Produces: `GET /api/v1/catalog/modifier-groups/{id}/products` → `200 []uuid` (ürün id'leri, `product_modifier_groups.sort_order, product_id` sırasıyla), izin `catalog.modifier_group.read`; bozuk id → 400 `invalid group id`; bilinmeyen grup → `200 []` (mevcut `ListProductGroups` ile aynı kontrat: varlık kontrolü yok).

- [ ] **Step 1: Repo integration testi (kırmızı)** — grup G'ye iki ürün ata (mevcut `AssignGroup` repo metodu), üçüncü ürünü başka gruba; `ListProductIDsByGroup(G)` tam olarak iki id'yi sırayla döndürür; farklı tenant altında boş döner (RLS).
- [ ] **Step 2: Repo + servis + handler** — SQL: `SELECT product_id FROM product_modifier_groups WHERE group_id = $1 ORDER BY sort_order, product_id`. Handler `listProductGroups`'ın (satır ~668) aynası. Route: `r.With(h.permit("catalog.modifier_group.read")).Get("/modifier-groups/{id}/products", h.listGroupProducts)`.
- [ ] **Step 3: Yeşil + lint** — `cd backend && go test ./internal/modules/catalog/... -count=1 -race`; `task backend:lint`.
- [ ] **Step 4: Commit** — `feat(catalog): seçenek grubunu kullanan ürünler ucu`.

---

### Task 2: Admin temeli — primitifler, tipler, hook'lar, i18n

**Files:**
- Create (shadcn CLI, `web/apps/admin` içinde `pnpm dlx shadcn@latest add alert-dialog textarea popover command`): `src/components/ui/alert-dialog.tsx`, `textarea.tsx`, `popover.tsx`, `command.tsx` (cmdk zaten bağımlılıkta). CLI paket eklerse `pnpm install` çalıştır; `pnpm-lock.yaml` commit'e girer.
- Create: `src/components/catalog/confirm-dialog.tsx`, `src/components/catalog/money-input.tsx`
- Modify: `src/types/index.ts`, `src/hooks/use-catalog.ts`, `src/messages/tr.json`, `src/components/layouts/admin-sidebar.tsx`
- Test: `src/test/confirm-dialog.test.tsx`, `src/test/money-input.test.tsx`, `src/test/use-catalog.test.tsx`

**Interfaces (Produces):**

```ts
// types/index.ts — genişletme
export type SelectionType = "single" | "multiple"
export interface ModifierGroup { id; tenant_id; name; selection_type: SelectionType; min_selections: number; max_selections: number | null; is_required: boolean; sort_order: number; created_at; updated_at }
export interface Modifier { id; tenant_id; group_id; name; price_delta: number; is_active: boolean; sort_order: number; created_at; updated_at }
export const TAX_RATE_OPTIONS = [0, 100, 1000, 2000] as const // bps: %0, %1, %10, %20
export const UNIT_OPTIONS = ["adet", "porsiyon", "gram", "kg", "ml", "lt"] as const

// hooks/use-catalog.ts — eklenenler (mevcutlar korunur)
useProductModifierGroupIds(productId): string[]        // GET /products/{id}/modifier-groups
useAssignModifierGroup(): mutate({productId, groupId, sortOrder})
useRemoveModifierGroup(): mutate({productId, groupId})
useModifierGroup(id)
useCreateModifierGroup(), useUpdateModifierGroup(), useDeleteModifierGroup()
useModifiers(groupId): Modifier[]                        // GET /modifier-groups/{gid}/modifiers
useCreateModifier(), useUpdateModifier(), useDeleteModifier()  // {groupId, id?, ...body}
useGroupProductIds(groupId): string[]                    // T1 ucu
useProductsModifierGroupIds(productIds: string[]): Record<string, string[]>  // useQueries, R2
useUpdateCategory() (yok ise ekleme; gerekmiyor)
```
Query key sözleşmesi: `["modifier-groups"]`, `["modifier-groups", id]`, `["modifiers", groupId]`, `["product-modifier-groups", productId]`, `["group-products", groupId]`, `["menu-items", menuId]`. Mutasyonlar ilgili anahtarları invalidate eder (ürün↔grup değişince hem `product-modifier-groups` hem `group-products`).

```tsx
// components/catalog/confirm-dialog.tsx
export function ConfirmDialog(props: { open; onOpenChange; title: string; description?: ReactNode; confirmLabel: string; cancelLabel?: string; destructive?: boolean; onConfirm: () => Promise<void> | void; secondaryAction?: { label: string; onClick: () => void } })
// components/catalog/money-input.tsx — kuruş <-> TL alanı
export function MoneyInput(props: { id; valueKurus: number | null; onChangeKurus: (v: number | null) => void; allowNegative?: boolean; placeholder? })
```

`tr.json` `catalog` namespace anahtarları (hepsi bu görevde girer):
```
catalog.products.{title, subtitle, add, search, filter.all, filter.uncategorized, filter.status.all, filter.status.active, filter.status.inactive, columns.{name, category, price, options, status}, status.{active, inactive}, noOptions, empty.{title, body, cta}, count ("{count} ürün · {active} satışta"), actions.{edit, duplicate, deactivate, activate, delete}, deleteConfirm.{title ("\"{name}\" silinsin mi?"), body, confirm, deactivateInstead}, toast.{created, updated, deleted, deactivated, activated, error}}
catalog.product.{new, edit, sections.{basics, pricing, sale}, fields.{name, category, unit, description, descriptionHint, price, priceHint, taxRate, taxHint ("Matrah {base} · KDV {tax}"), sortOrder, sortOrderHint, active, activeHint}, groups.{title, subtitle, required, optional, single, multiple, optionsCount ("{count} seçenek"), edit, remove, addPlaceholder, createNew ("Yeni grup: \"{name}\""), empty}, menus.{title, hint, priceDefault}, save, cancel, delete, unsavedTitle, unsavedBody, unsavedLeave, unsavedStay, validation.{nameRequired, priceRequired}}
catalog.groups.{title, subtitle, add, columns.{name, rule, options, products, required}, rule.{single, multiple, required, optional, max ("en fazla {max}"), unlimited}, empty.{title, body, cta}, deleteConfirm.{title, body ("{count} üründen kaldırılır…"), confirm}, toast.{created, updated, deleted, error}}
catalog.group.{new, edit, sections.{rule, options, preview, usedBy}, fields.{name, nameHint, selection, selectionSingle, selectionMultiple, required, requiredNo, requiredYes, max, maxUnlimited, maxHint}, options.{name, priceDelta, active, addRow, addPlaceholder, moveUp, moveDown, delete, deleteConfirm, empty, hint ("Satıra tıklayıp yazın · Enter ile sonraki satır")}, preview.{title, optional, required, free, hint}, usedBy.{title, empty, hint}, save, cancel, delete, validation.{nameRequired, maxLessThanMin}}
catalog.common.{loading, retry, loadFailed}
nav.modifiers → "Seçenek Grupları" (mevcut anahtar güncellenir)
```

- [ ] **Step 1:** shadcn CLI ile dört primitifi ekle; `git status` ile yalnız beklenen dosyaların geldiğini doğrula.
- [ ] **Step 2 (TDD):** `MoneyInput` testi: "12,50" → 1250; boş → null; negatif `allowNegative` yoksa reddedilir; blur'da "12,50" olarak biçimlenir. `ConfirmDialog` testi: onay `onConfirm` çağırır ve bekler (buton `disabled` olur), vazgeç kapatır, `secondaryAction` render edilir.
- [ ] **Step 3:** Hook'lar ve tipler; `use-catalog.test.tsx` ile `useProductsModifierGroupIds` 3 ürün için 3 istek + doğru map, `useAssignModifierGroup` başarıda iki anahtarı invalidate eder (QueryClient spy).
- [ ] **Step 4:** `tr.json` anahtarları + sidebar etiketi. Mevcut `modifiers/page.tsx`'teki lokal `useCreateModifierGroup`'ı hook dosyasına taşı (sayfa Task 5'te yeniden yazılacak; şimdilik import'u düzelt, derleme yeşil kalsın).
- [ ] **Step 5:** `task web:test && task web:typecheck && task web:lint`; commit `feat(admin): katalog UX temeli — primitifler, hook'lar, i18n`.

---

### Task 3: Ürün listesi

**Files:**
- Modify: `src/app/(main)/catalog/products/page.tsx`
- Create: `src/components/catalog/product-row-actions.tsx`
- Test: `src/test/products-page.test.tsx`

**Interfaces:** Consumes T2 hook'ları, `ConfirmDialog`, `useCategories`, `useUpdateProduct`, `useDeleteProduct`, `useProductsModifierGroupIds`, `useModifierGroups`. Tasarım: canvas artboard 1.

Davranış:
- Başlık altı özet: "{count} ürün · {active} satışta".
- Arama kutusu (ad/açıklama, `tr-TR` locale-insensitive `localeCompare`/`toLocaleLowerCase("tr")`), kategori chip'leri (Tümü, her kategori, "Kategorisiz · n"), durum select (Tümü / Satışta / Satışta değil). Filtre durumu URL query'sinde (`?q=&cat=&status=`) tutulur (geri tuşu korur).
- Tablo sütunları: Ürün (ad + açıklama alt satırı, satır tıklanınca `/catalog/products/{id}`), Kategori (kategorisiz → uyarı rengi "Kategorisiz"), Fiyat (sağa dayalı, `tabular-nums`), Seçenekler (grup adı rozetleri; yoksa "Seçenek yok"), Durum rozeti, satır menüsü (kebab: Düzenle, Kopyala, Satıştan kaldır/Satışa al, Sil…).
- Kopyala: aynı gövdeyle `POST /products` (ad sonuna " (kopya)"), sonra detaya git.
- Sil: `ConfirmDialog` (destructive), `secondaryAction` = "Satıştan kaldır" (PUT `is_active:false`). Gövde: menü sayısını bilmiyorsak genel metin.
- "Ürün ekle" → `/catalog/products/new`.
- Boş durum ve yükleniyor iskeleti korunur; hata durumunda `catalog.common.loadFailed` + yeniden dene.

- [ ] **Step 1 (TDD):** test: liste render, arama daraltır, kategori chip'i filtreler, kebab → Sil → onay dialog'u → `DELETE` çağrılır; "Satıştan kaldır" ikincil aksiyon `PUT` ile `is_active:false` gönderir; satır tıklaması `router.push('/catalog/products/{id}')` (next/navigation mock).
- [ ] **Step 2:** Uygulama. Kebab için mevcut `dropdown-menu.tsx`.
- [ ] **Step 3:** Doğrula + commit `feat(admin): ürün listesi — arama, filtre, satır aksiyonları ve silme onayı`.

---

### Task 4: Ürün detayı (`/catalog/products/[id]`, `/new`)

**Files:**
- Create: `src/app/(main)/catalog/products/[id]/page.tsx`, `src/app/(main)/catalog/products/new/page.tsx` (ikisi de `ProductEditor` bileşenini render eder; `[id]` `useParams`)
- Create: `src/components/catalog/product-editor.tsx` (sayfa düzeni + kaydet/vazgeç/sil), `product-form.tsx` (sol kolon), `product-modifier-groups-card.tsx`, `product-menus-card.tsx`
- Test: `src/test/product-detail.test.tsx`

**Interfaces:** Tasarım: canvas artboard 2. Consumes T2 (`MoneyInput`, `ConfirmDialog`, Command/Popover, hook'lar), `useMenus`, `useMenuItems`, `useAddMenuItem`, `useRemoveMenuItem`.

Davranış:
- Başlık: ad + Aktif/Pasif rozeti; alt satır "{kategori} · {fiyat} · %{kdv} KDV · {n} seçenek grubu · {m} menüde". Sağda Sil (ghost, destructive renk), Vazgeç, Kaydet (primary). `new` modunda başlık "Yeni ürün", Sil yok.
- Sol kolon kartları: **Temel bilgiler** (Ad*, Kategori select (native `Select`), Birim select (`UNIT_OPTIONS`), Açıklama `Textarea` + ipucu), **Fiyat ve vergi** (`MoneyInput` "KDV dahil", KDV select `TAX_RATE_OPTIONS`, canlı ipucu "Matrah … · KDV …" — formül `tax = round(gross*bps/(10000+bps))`, Sıra), **Satışta** (Switch + açıklama).
- Doğrulama: ad boş → alan altında hata, fiyat boş → hata; Kaydet `isPending`'de disabled. Kaydet: `new` → `POST` sonra `router.replace('/catalog/products/{id}')`; edit → `PUT` (is_active dahil). Toast.
- Kirli form + sayfadan ayrılma: `beforeunload` + uygulama içi Vazgeç'te `ConfirmDialog` (unsaved*).
- **Seçenek grupları kartı** (yalnız edit modunda; `new`'de "Önce ürünü kaydedin" notu): atanmış gruplar `useProductModifierGroupIds` × `useModifierGroups` ile ad/rozet/kural özeti (`useModifiers` ile ilk 3 seçenek adı ve "+n"), "Düzenle" → `/catalog/modifiers/{gid}`, × → kaldır (onay yok, geri alınabilir toast "Geri al" ile yeniden atar). Ekleme: `Popover` + `Command` combobox: atanmamış gruplar listelenir, yazılan metin eşleşmiyorsa "Yeni grup: "…"" seçeneği → `POST /modifier-groups` (`selection_type:"single"`, min 0, max 1, is_required false) + atama, toast "Grup oluşturuldu, kuralını düzenleyin" (link).
- **Menüler kartı**: tüm menüler satır satır, Switch = üründe var/yok (`useMenuItems(menu.id)` içinde `product_id` eşleşmesi); açma `POST items {product_id, price_override:null, is_active:true}`, kapama `DELETE`. Alt ipucu.
- Sil: `ConfirmDialog` destructive + ikincil "Satıştan kaldır"; başarıda `/catalog/products`.

- [ ] **Step 1 (TDD):** testler: edit modunda form dolu gelir; ad silinip Kaydet → hata metni, `PUT` çağrılmaz; fiyat "150" → `price_amount 15000`; KDV ipucu %10 için "Matrah ₺136,36 · KDV ₺13,64"; grup combobox'ından mevcut grup seçilince `POST /products/{id}/modifier-groups`; "Yeni grup" seçilince önce `POST /modifier-groups` sonra atama; menü switch'i `POST /menus/{id}/items`; Sil → onay → `DELETE` → `router.push('/catalog/products')`.
- [ ] **Step 2:** Uygulama.
- [ ] **Step 3:** Doğrula + commit `feat(admin): ürün detay sayfası — bilgiler, seçenek grupları, menüler`.

---

### Task 5: Seçenek grubu listesi ve detayı

**Files:**
- Modify: `src/app/(main)/catalog/modifiers/page.tsx` (liste yeniden yazılır)
- Create: `src/app/(main)/catalog/modifiers/[id]/page.tsx`, `src/app/(main)/catalog/modifiers/new/page.tsx`, `src/components/catalog/modifier-group-editor.tsx`, `modifier-group-form.tsx`, `modifier-options-editor.tsx`, `modifier-preview.tsx`
- Test: `src/test/modifier-group-detail.test.tsx`, `src/test/modifier-groups-page.test.tsx`

**Interfaces:** Tasarım: canvas artboard 3. Consumes T1 ucu (`useGroupProductIds`), T2 hook'ları.

Liste sayfası: başlık "Seçenek Grupları", "Grup ekle" → `/catalog/modifiers/new`; sütunlar Ad (satır tıklanınca detay), Kural ("Tek seçim · Zorunlu" / "Birden fazla · en fazla 3"), Seçenekler (`useModifiers` sayısı — `useQueries`), Ürünler (`useGroupProductIds` sayısı), kebab (Düzenle, Sil… → `ConfirmDialog`, gövde "{count} üründen kaldırılır").

Detay sayfası:
- Başlık ad + "{n} seçenek · {m} üründe kullanılıyor"; Vazgeç, Kaydet.
- **Kural kartı**: Grup adı* (+ ipucu), "Müşteri kaç tane seçebilir?" segmentli (Tek seçim / Birden fazla → `selection_type`; tek seçimde `max_selections=1`, min = zorunluysa 1), "Seçim zorunlu mu?" segmentli (Hayır / Evet → `is_required`, min 1), "En fazla" (yalnız birden fazla: select Sınırsız / 2..10 → `max_selections`). Doğrulama: ad boş; max < min.
- **Seçenekler kartı**: satır içi düzenlenebilir liste. Sütunlar: ▲▼ sıralama butonları, Ad (input), Fiyat farkı (`MoneyInput allowNegative`), Satışta (Switch), Sil (ikon, `ConfirmDialog` küçük). Son satır "+ Yeni seçenek…" — yazıp Enter → `POST modifiers` (sort_order = son+10) ve yeni boş satır odaklanır. Mevcut satır alanı blur/Enter'da değiştiyse `PUT`. Her satır kendi kaydetme durumunu gösterir (küçük spinner/✓). `new` modunda seçenekler kartı "Önce grubu kaydedin" der; Kaydet sonrası `router.replace('/catalog/modifiers/{id}')`.
- **Müşteri böyle görür** kartı: aktif seçenekler, tek seçimde radio, çoklu seçimde checkbox görünümü; başlıkta "İsteğe bağlı"/"Zorunlu"; fiyat farkı "+₺60" / "Ücretsiz". Salt görsel.
- **Bu grubu kullanan ürünler** kartı: `useGroupProductIds` × `useProducts` → ürün adları linkli; boşsa "Henüz hiçbir üründe kullanılmıyor"; ipucu "Buradaki değişiklik {n} üründe de geçerli olur."
- Sil (yalnız edit): `ConfirmDialog`; başarıda listeye dön.

- [ ] **Step 1 (TDD):** testler: liste kural metnini doğru üretir; detayda "Birden fazla" seçilince "En fazla" alanı görünür, "Tek seçim"de gizlenir ve `max_selections:1` gönderilir; yeni seçenek satırında Enter → `POST modifiers` doğru gövde; fiyat farkı "-5" → `price_delta -500` (allowNegative); satır ▼ → iki `PUT` ile sort_order takası; önizleme aktif olmayan seçeneği göstermez; kullanan ürünler kartı adları listeler.
- [ ] **Step 2:** Uygulama.
- [ ] **Step 3:** Doğrula + commit'ler: `feat(admin): seçenek grubu listesi — kural özeti ve silme onayı`, `feat(admin): seçenek grubu detayı — kural, satır içi seçenekler, önizleme`.

---

### Task 6: Tarayıcı UX turu (controller görevi)

Playwright ile canlı dev yığınında: ürün ekle → kategori/KDV seç → kaydet → detayda "Yeni grup: Sos" oluştur → grubu düzenle → 3 seçenek ekle (Enter zinciri) → önizlemeyi gör → ürüne dön → menüde aç → listede ara → sil onayı. Her adımda ekran görüntüsü; bulgular plan dışı fix dalgası olarak ele alınır.

## Plan Dışı

- Sürükle-bırak sıralama, ürün görseli yükleme (MinIO), toplu düzenleme, backend liste zenginleştirme (R2) — backlog.
