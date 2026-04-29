# ADR-AUTH-001: Dört Katmanlı Authorization Mimarisi

**Durum:** Kabul Edildi
**Tarih:** 2026-04-19
**İlgili delta:** P0-3
**Kategori:** Yetkilendirme (AUTH)

## Bağlam

Baseline'da OPA var ama "hangi yetki kontrolü hangi katmanda yapılır" belirsizdi. Bu belirsizlik iki tehlikeli uca çeker:

1. **Her şey RLS'e:** "Kasiyer kendi satışlarını görür, müdür şubesini görür" mantığı RLS policy'sine yazılır → 5 rol × 10 tablo = 50 karmaşık policy, debug cehennem.
2. **Her şey handler'a:** Authz check'leri her handler'da ad-hoc → audit edilemez, tutarsız, bir endpoint'te unutulursa sızıntı.

Ayrıca **field-level görünürlük** (kasiyer `cost_price` görmesin) RLS ile çözülemez (satır bazlı); OPA ile de çözülmemeli (policy şişer).

## Karar: Dört Katmanlı Model

| Katman | Sorumluluk | Nerede | Neden |
|---|---|---|---|
| **1. RLS (DB)** | **Yalnızca `tenant_id` izolasyonu.** | PostgreSQL policy | Cross-tenant sızıntı son savunma hattı |
| **2. OPA (Policy)** | **"Bu action'a izin var mı" + Scope** (`ScopeOwn`/`ScopeBranch`/`ScopeTenant`) | Embedded in-process | Versiyonlanabilir, test edilebilir |
| **3. Service (Scope)** | OPA scope'unu query WHERE clause'una çevirir | Go service layer | Debuggable, unit-testable |
| **4. DTO Projection** | **Field-level filtreleme** (kasiyer `cost_price` görmez) | Response DTO | Tip güvenli tek yer |

### OPA Decision Sözleşmesi

OPA'dan dönen tek yapı:

```go
type Decision struct {
    Allow bool
    Scope Scope // enum: ScopeOwn | ScopeBranch | ScopeTenant
}
```

**OPA permission listesi döndürmez.** Field-level görünürlük, rol → permission mapping'i service/projection katmanında çözülür.

### Örnek Uçtan Uca Akış

Senaryo: Kasiyer "bugünün satışları" ister.

```
1. HTTP: GET /v1/sales?scope=today, Authorization: Bearer <JWT>
2. [Middleware: auth] JWT doğrula → Principal{UserID, TenantID, BranchIDs[], Roles[]} → ctx
3. [Middleware: tenant] BEGIN + SET LOCAL app.tenant_id = principal.tenant_id
4. [Service] decision := authz.Decide(ctx, "sales.list", principal)
   → Decision{Allow: true, Scope: ScopeOwn}
5. [Service] WHERE cashier_id = principal.UserID AND created_at >= business_day_start(branch_tz)
6. [DTO] ProjectSale(sale, permsForRoles(principal.Roles)) → cost_price düşürülür
7. JSON response
```

### Karar Detayları

1. **Domain model rolleri bilmez.** `domain.Sale` struct'ı `CostPrice` ve `Profit`'i **her zaman** tutar. Rol bazlı filtreleme yalnızca DTO projection'da.

2. **Platform katmanı `internal/platform/authz/`:**
   - `authz.Principal` — JWT'den parse
   - `authz.Decider` interface — embedded OPA implementation
   - `authz.Scope` enum (`ScopeOwn | ScopeBranch | ScopeTenant`) — tip güvenli
   - `authz.PermSet` — projection için rol listesinden türetilen permission kümesi

3. **OPA embedded mode:** Sidecar değil, in-process. Policy bundle'ları `configs/opa/bundles/`. Faz 2'de bundle server (hot-reload).

4. **Decision cache:** Redis `authz:{user_id}:{action}:{resource_type}`, TTL 60s. Keycloak `user.updated` event'i cache invalidate eder.

5. **JWT claim şeması:**
   ```json
   {
     "sub": "user-uuid",
     "tenant_id": "tenant-uuid",
     "branch_ids": ["branch-uuid-1", "branch-uuid-2"],
     "roles": ["cashier"]
   }
   ```
   `branch_ids` **array** — DB'deki `branch_users` M:N ilişkisiyle uyumlu.

6. **Projection helper:** `internal/platform/projection/projector.go`
   ```go
   type Projector[D any, V any] interface {
       Project(d D, perms authz.PermSet) V
   }
   ```

7. **Permission tablosu:** `internal/platform/authz/permissions.go`
   ```go
   var rolePerms = map[string]PermSet{
       "cashier":    {"sale.view", "order.create"},
       "manager":    {"sale.view", "sale.view_financials", "staff.manage"},
       "owner":      {"*"},
       "accountant": {"sale.view", "sale.view_financials", "invoice.view"},
   }
   ```
   Tenant-specific override: Faz 2+ (başlangıçta tek tablo, tüm tenant'lara uyar).

8. **Fail-closed:** OPA cevap vermezse → Deny. `Allow` default değeri `false`.

## Sonuçlar

**İyi:**
- Her authz sorusu tek bir katmanın sorumluluğu.
- OPA policy'leri basit kalır (sadece scope kararı); Rego 50 satırı geçmez.
- Field-level görünürlük tip sistemi ile zorlanır (DTO'da field yok → sızıntı imkânsız).
- Debug kolay: her katman ayrı loglanır, ayrı test edilir.

**Dikkat:**
- Dört katmanın her biri ayrı kod + test. Başlangıç maliyeti var.
- Projection katmanını atlama ayartması güçlü. Code review disiplini şart.

**Risk:**
- `PermSet` tablosu hard-coded — tenant-specific varyant Faz 2'de gelir.

## Değerlendirilen Alternatifler

- **Her şey RLS:** Reddedildi. Debug imkânsız, policy patlar, field-level yapamaz.
- **Her şey OPA (permission listesi dahil):** Reddedildi. Policy 500+ satır olur.
- **Casbin:** Reddedildi. OPA daha olgun, bundle distribution ekosistemi var.
- **Handler-bazlı ad-hoc:** Reddedildi. Audit edilemez, tutarsız.
