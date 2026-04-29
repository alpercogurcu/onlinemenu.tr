# ADR-AUTH-002: Keycloak Tek Realm Stratejisi

**Durum:** Kabul Edildi
**Tarih:** 2026-04-19
**İlgili delta:** v1'den kapanan açık soru
**Kategori:** Yetkilendirme (AUTH)

## Bağlam

Multi-tenant Keycloak'ta iki ana strateji:
1. **Realm-per-tenant:** Her tenant kendi realm'ı. Sert izolasyon, tenant kendi SSO'sunu getirebilir.
2. **Tek realm + tenant_id claim:** Tüm tenant'lar tek realm'da. İzolasyon claim + uygulama katmanında.

Türkiye pazarı (zincir/franchise restoran, market) büyük çoğunlukla kendi SSO'sunu getirmeyecek. Google/Apple/phone OTP tipik login metodları. 1000+ tenant ölçeklendiğinde realm-per-tenant Keycloak'ı diz çöktürür.

## Karar

**Tek realm stratejisi kullanılır.**

1. **Realm:** `onlinemenu` (tek realm, tüm tenant'lar).

2. **Custom claim'ler:** JWT'ye mapper ile basılır:
   - `tenant_id` — kullanıcının ait olduğu tenant (tek değer)
   - `branch_ids[]` — erişim yetkisi olan tüm şubeler
   - `roles[]` — tüm rollerin birleşimi

3. **Grup hiyerarşisi:**
   ```
   /tenants/{tenant_id}
           /branches/{branch_id}
                    /roles/{role}   (cashier, manager, ...)
   ```
   Bir kullanıcı birden fazla şubede farklı rol alabilir.

4. **JWT mapper:** Grupları düz array'lere çözer (`branch_ids[]`, `roles[]`).

5. **Identity provider:** Email/password + Google + Apple + phone OTP (Faz 2). Tenant-specific IDP Faz 3-4.

6. **Keycloak config-as-code:** Custom claim mapper'lar `keycloak-config-cli` veya Terraform Keycloak provider ile version kontrollü olarak yönetilir.

7. **Admin API kullanımı:** Platform backend, tenant oluşturulduğunda otomatik Keycloak grubu yaratır. Davet akışı Keycloak built-in invite flow'unu kullanır.

## Sonuçlar

**İyi:**
- Ölçeklenebilir: 10k+ tenant, 100k+ kullanıcı tek realm'da rahat.
- Tek JWKS endpoint, tek token validation middleware.
- Keycloak admin operasyonları hızlı (realm başına cache overhead yok).
- Grup hiyerarşisi değişiklikleri anlık.

**Dikkat:**
- Tenant kendi SSO'sunu getiremez (başlangıçta). Enterprise müşteri talep ederse Faz 3-4'te realm-per-tenant veya broker pattern değerlendirilir.
- Kullanıcı email'i global unique — bir kullanıcı birden fazla tenant'ta olmak isterse farklı email gerekir (multi-tenant user Faz 3).

**Risk:**
- Custom claim mapper bozulursa tüm auth bozulur. Mapper'lar config-as-code ile yönetilmeli.

## Değerlendirilen Alternatifler

- **Realm-per-tenant:** Reddedildi. Keycloak admin API 100+ realm'da ciddi yavaşlar; cross-realm token validation karmaşık; migration cehennem.
- **Her tenant için ayrı Keycloak instance:** Reddedildi. Operasyonel maliyet × tenant sayısı.
- **Auth0/Clerk gibi SaaS:** Reddedildi. Maliyet, veri egemenliği (KVKK), özelleştirme sınırlı.
