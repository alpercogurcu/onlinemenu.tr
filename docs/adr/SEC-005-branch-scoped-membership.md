# ADR-SEC-005 — Şube-kapsamlı rollerde membership şube zorunluluğu

- **Durum:** Kabul edildi
- **Tarih:** 2026-07-19
- **İlgili:** ADR-SEC-001 (RLS), ADR-SEC-002 (FORCE RLS), ADR-AUTH-001 (4 katmanlı authorization), ADR-DATA-005
- **Migration:** `backend/migrations/identity/000012_memberships_branch_scoped_guard.{up,down}.sql`
  ve `000013_memberships_role_tenant_guard.{up,down}.sql` (bkz. [000013 eki](#000013-eki--rol-tenant-bütünlüğü))

---

## Bağlam

`memberships.branch_id` nullable'dır ve `NULL` değeri authorization zincirinde
"zincirin tamamı" (tüm şubeler) olarak yorumlanır. Bu, zincir sahibi / zincir
denetçisi gibi roller için doğru davranıştır.

Sorun: kasiyer, mutfak, bar, şoför gibi **şubeye bağlı** roller de
`branch_id = NULL` bir membership ile verilebiliyordu. Bu durumda tek şubede
çalışan bir kasiyer, tenant'ın bütün şubelerinde yetki kazanıyordu.

Mevcut tek savunma Go tarafındaydı:

```go
if role.Scope() == domain.RoleScopeBranch && branchID == nil { ... }
```

Bu kontrol **hiçbir zaman tetiklenmiyordu**. `Scope()` yalnızca
`roles.branch_id IS NOT NULL` olan satırlar için `RoleScopeBranch` döndürür;
sistem şablonlarının ve tenant klonlarının `branch_id`'si `NULL`'dır.

Ayrıca tenant onboarding'de
(`identity/events/subscriber.go::SeedTenantRoles`) sistem rolleri tenant'a
kopyalanırken `system_key` **NULL'lanır** ve satıra rastgele bir `id` verilir.
Yani klon satırda rolün "hangi sistem rolü olduğu" bilgisi kalmaz.

---

## Karar

Şube-kapsamı bilgisi **rolün kendi satırında** taşınır:

```sql
ALTER TABLE roles ADD COLUMN branch_scoped BOOLEAN NOT NULL DEFAULT FALSE;
```

Üç katmanlı zorlama:

1. **DB trigger (son savunma)** — `memberships_branch_scope_guard`,
   `BEFORE INSERT OR UPDATE OF role_id, branch_id ON memberships`.
   `NEW.branch_id IS NOT NULL` ise geçer. Aksi halde rol satırı okunur:
   - satır bulunamazsa (cross-tenant veya dangling `role_id`) → **fail-closed** red,
   - `branch_scoped` veya `branch_id IS NOT NULL` ise → red (ERRCODE `23514`).

   Fonksiyon **SECURITY INVOKER**'dır (varsayılan). Rol okuması çağıranın RLS'i
   altında yapılır; başka tenant'ın rolü zaten görünmez ve "bulunamadı" dalına
   düşerek reddedilir. `REVOKE ALL ... FROM PUBLIC` + `GRANT EXECUTE ... TO app_runtime`.

2. **Klonlama** — `SeedTenantRoles` klon INSERT'i `branch_scoped` alanını
   kaynak şablon satırından kopyalar. Bu satır olmadan düzeltme kozmetik kalır:
   yeni açılan her tenant hatayı yeniden üretir.

3. **Servis (erken hata / UX)** — `MembershipService.Create`,
   `role.RequiresBranch()` (`BranchScoped || Scope()==RoleScopeBranch`) ve
   `branchID == nil` durumunda temiz `pub.ErrInvalid` (HTTP 400) döndürür.
   Trigger'ın 500'ü kullanıcıya sızmaz.

### Neden sabit sistem-rol UUID'lerine CHECK yazılmadı

`CHECK (role_id NOT IN ('00000001-...-0001', ...))` biçimindeki bir kısıt:

- Tenant klonlarını **hiç yakalamaz** (klonun id'si rastgeledir),
- Yeni sistem rolü eklendiğinde migration gerektirir,
- `roles` tablosuna subquery içeremeyeceği için zaten CHECK olarak yazılamaz.

Bilgi rolün satırında taşındığında klonlama, yeniden adlandırma ve yeni rol
ekleme senaryolarının hepsi doğal olarak kapsanır.

### `memberships_unique` — NULLS NOT DISTINCT

Aynı migration'da kısıt yeniden kuruldu:

```sql
UNIQUE NULLS NOT DISTINCT (person_id, tenant_id, branch_id, role_id)
```

Varsayılan `UNIQUE` semantiğinde `NULL != NULL` olduğu için aynı kişi aynı
zincir-geneli rolü **sınırsız kez** alabiliyordu. `roles` tablosu zaten
`NULLS NOT DISTINCT` kullanıyor (000002); tutarlılık sağlandı.

---

## 000013 eki — rol/tenant bütünlüğü

> Eklenme tarihi: 2026-07-31 · Migration: `000013_memberships_role_tenant_guard`

000012 yazıldığında bir kural **hiçbir yerde ifade edilmemiş** durumdaydı:
*"bir membership'in rolü, o membership'in tenant'ına ait olmalıdır."*

000012 bunu kazara kısmen kapatıyordu: rol satırını çağıranın RLS'i altında
okuduğu için başka tenant'ın rolü görünmez, "bulunamadı" dalına düşer ve INSERT
reddedilir. Ancak bu kapağın **bir deliği ve bir örtük bağımlılığı** vardı.

### Delik — `branch_id IS NOT NULL` dalı rolü hiç okumuyordu

000012'nin guard'ı `NEW.branch_id IS NOT NULL` durumunda **erken dönüyordu**;
o dalda `roles` satırına hiç bakılmıyordu. `memberships.role_id` üzerindeki FK
ise sistem tarafından doğrulanır ve **RLS'i baypas eder**. Sonuç: somut şubeli
bir membership, başka tenant'ın rolüne işaret etse bile DB katmanında kabul
ediliyordu.

Tek engel Go tarafındaydı — `MembershipService.Create`, rolü `RoleRepo.GetByID`
ile tenant filtreli çekiyor. Yani kural bir servis metodunun içinde yaşıyordu;
doğrudan SQL veya ileride eklenecek başka bir kod yolu onu atlardı. Bu tam olarak
`docs/lessons-from-b2b.md`'nin "varsayılan yol güvensizse N'inci call site'ta
unutulur" dersi.

### Örtük bağımlılık — "görünmez ⇒ reddedilir" bu kuralın özelliği değil

"Cross-tenant rol RLS'te görünmez, dolayısıyla reddedilir" çıkarımı `roles`
tablosunun **policy setinin** bir özelliği; bu kuralın kendisinin değil.
identity/000009 zaten `roles_all_scope_read` politikasını eklemişti
(`app.tenant_scope = 'all_tenants'` olan oturum her tenant'ın rolünü görür).
Rol görünürlüğünde ileride yapılacak herhangi bir genişletme, garantiyi
**sessizce** aşındırırdı.

### Düzeltme

Rol satırı **koşulsuz** okunur (`branch_id` erken dönüşünden önce) ve tenant
ilişkisi açık bir yüklemle iddia edilir:

```sql
roles.tenant_id IS NULL                      -- sistem şablonu, her tenant alabilir
OR roles.tenant_id = memberships.tenant_id   -- tenant'ın kendi rolü
```

İhlalde `ERRCODE = '23514'`. Bu kontrol `app_runtime` altında normalde
erişilemezdir (tenant'ı uymayan rol zaten RLS'te görünmez ve bir üstteki
"bulunamadı" dalında reddedilir) — ama kuralı policy setinden **bağımsız** kılar:
platform-scope oturumlar (`app.tenant_scope = 'all_tenants'`) ve `app_migrator`
için de geçerlidir, ki ikisi de `roles` üzerinde RLS ile kısıtlı değildir.

### Neden trigger, neden bildirimsel bir kısıt değil

| Aday | Neden olmuyor |
|---|---|
| Bileşik FK `(role_id, tenant_id) → roles (id, tenant_id)` | `MATCH SIMPLE` yalnızca **referans eden** bir kolon NULL olduğunda kontrolü atlar. Burada referans eden iki kolon da NOT NULL; NULL olan **referans edilen** `roles.tenant_id` (sistem şablonları). Sonuç: sistem rolüne dayanan **her** membership reddedilirdi. |
| Generated column | Başka tabloyu okuyamaz. |
| `CHECK` kısıtı | Subquery içeremez. |

Bu yüzden kural yalnızca 000012'nin trigger'ında yaşayabilir; 000013 ikinci bir
trigger eklemek yerine **mevcut fonksiyonu `CREATE OR REPLACE` ile genişletir**.
Trigger tanımı (`BEFORE INSERT OR UPDATE OF role_id, branch_id`) değişmez —
`CREATE OR REPLACE FUNCTION` mevcut bağlamayı korur.

### `tenant_id` neden `UPDATE OF` listesinde yok

Bir membership satırını tenant'lar arasında taşımak `app_runtime` için zaten
imkânsız: `memberships_write` politikası (000003) tenant yüklemini hem `USING`
hem `WITH CHECK` tarafında taşır — eski satır görünmez, yeni satır reddedilir.
`tenant_id`'yi listeye eklemek, erişilebilir bir kazanç olmadan her iki yönde
`DROP`/`CREATE TRIGGER` çifti gerektirirdi.

### Deploy sonrası denetim — 000013 için

Trigger'dan önce yazılmış cross-tenant rol bağlantılarını yakalar
("Deploy öncesi denetim" bölümündeki 3 numaralı sorgunun kardeşi):

```sql
SELECT m.id, m.tenant_id AS membership_tenant, r.tenant_id AS role_tenant,
       m.person_id, r.name AS role_name
FROM memberships m
JOIN roles r ON r.id = m.role_id
WHERE r.tenant_id IS NOT NULL
  AND r.tenant_id <> m.tenant_id;
```

Çıkan her satır, bir tenant'ın üyesine **başka bir tenant'ın rolünün** verilmiş
hâlidir. Bu sorgu `app_migrator` ile koşulmalıdır; `app_runtime` altında ihlal
satırları zaten görünmez.

---

## Bayrak popülasyonu — sorumluluk

| Durum | Ne yapılır |
|---|---|
| Yeni **sistem rolü** seed'i | Seed INSERT'ine `branch_scoped` değeri **açıkça** yazılır. Varsayılan `FALSE`'tır; unutulursa rol sessizce zincir-geneli olur. |
| Yeni **tenant** onboarding | Otomatik — `SeedTenantRoles` bayrağı şablondan kopyalar. |
| Tenant'ın kendi **custom rolü** | `RoleRepo.Create`, `role.RequiresBranch()` değerini yazar. Şubeye bağlı custom roller için bayrağı API'den taşımak **açık iştir**. |
| Custom rolün sonradan `FALSE → TRUE` olması | **Geriye dönük yakalanmaz.** Trigger yalnızca INSERT/UPDATE'te çalışır; bayrak değişmeden önce yazılmış membership satırları yerinde kalır. Denetim sorgusu (aşağıda) çalıştırılmalıdır. |

---

## Açık soru — `warehouse` / ADR-DATA-005

`warehouse` rolü backfill'de `branch_scoped = TRUE` olarak işaretlendi. Gerekçe:
mevcut `configs/opa/bundles/authz.rego` scope'u depo personelini şube bağlamında
değerlendiriyor. Ancak ADR-DATA-005 (İlke 4) merkezi depo / zincir-geneli
imalat senaryosunu tarif eder; bu senaryoda depo sorumlusunun zincir-geneli
membership'e ihtiyacı olabilir.

**Karar ertelendi.** Bugünkü rego davranışıyla uyum korundu. Merkezi depo
özelliği hayata geçtiğinde ya `warehouse` bayrağı `FALSE`'a çekilir ya da ayrı
bir `central_warehouse` sistem rolü eklenir. Bu ADR güncellenmelidir.

`waiter` anahtarı backfill listesinde yer alır ancak bugün seed edilmiş böyle
bir şablon yoktur — ileriye dönük, etkisiz bir kayıttır.

---

## Deploy öncesi denetim

### 1. Duplicate zincir-geneli membership (migration'ı bloklar)

`NULLS NOT DISTINCT`'e geçiş, mevcut duplicate satırlar varsa migration'ı
**başarısız eder**. Önce çalıştır, çıktı boş değilse temizle:

```sql
SELECT person_id, tenant_id, branch_id, role_id, COUNT(*), array_agg(id)
FROM memberships
GROUP BY person_id, tenant_id, branch_id, role_id
HAVING COUNT(*) > 1;
```

### 2. Şablon fingerprint — isim değiştirmiş klonlar

Backfill klonları **isimle** eşler. Adı değiştirilmiş klonlar ıskalanır. Bunları
izin parmak iziyle (permission set) yakala:

```sql
WITH tmpl AS (
    SELECT r.id, r.system_key,
           md5(string_agg(rp.resource || ':' || rp.action, ',' ORDER BY rp.resource, rp.action)) AS fp
    FROM roles r
    JOIN role_permissions rp ON rp.role_id = r.id
    WHERE r.tenant_id IS NULL AND r.branch_scoped
    GROUP BY r.id, r.system_key
),
clone AS (
    SELECT r.id, r.tenant_id, r.name, r.branch_scoped,
           md5(string_agg(rp.resource || ':' || rp.action, ',' ORDER BY rp.resource, rp.action)) AS fp
    FROM roles r
    JOIN role_permissions rp ON rp.role_id = r.id
    WHERE r.tenant_id IS NOT NULL
    GROUP BY r.id, r.tenant_id, r.name, r.branch_scoped
)
SELECT clone.tenant_id, clone.id AS role_id, clone.name, tmpl.system_key
FROM clone
JOIN tmpl ON tmpl.fp = clone.fp
WHERE NOT clone.branch_scoped;
```

Çıkan satırlar için `UPDATE roles SET branch_scoped = TRUE WHERE id = ...`.

### 3. Deploy sonrası — trigger'dan önce yazılmış ihlaller

```sql
SELECT m.id, m.tenant_id, m.person_id, r.name AS role_name
FROM memberships m
JOIN roles r ON r.id = m.role_id
WHERE m.branch_id IS NULL
  AND (r.branch_scoped OR r.branch_id IS NOT NULL);
```

Her satır, tek şubeye ait bir rolün zincir geneline açılmış hâlidir. Doğru
şubeyle yeniden yazılmalı veya `terminated` edilmelidir. Bu sorgu, bayrağı
sonradan `TRUE`'ya çekilen custom roller için de periyodik olarak koşulmalıdır.

---

## Sonuçlar

**Olumlu**
- Zincir-geneli yetki sızıntısı DB seviyesinde kapandı; uygulama hatası veya
  doğrudan SQL erişimi bu kuralı atlayamaz.
- Klonlama yolu artık kapsanıyor — asıl açık buydu.
- Cross-tenant / dangling `role_id` fail-closed reddediliyor (yan kazanım).

**Olumsuz / maliyet**
- Her chain-wide membership INSERT'inde bir `roles` lookup'ı (PK üzerinden,
  ihmal edilebilir).
- Yeni sistem rolü ekleyenin bayrağı açıkça yazma disiplini gerekiyor;
  varsayılan `FALSE` sessizce yanlış tarafa düşebilir.
- Mevcut ihlaller otomatik düzelmiyor; denetim sorgusu operasyonel iştir.
