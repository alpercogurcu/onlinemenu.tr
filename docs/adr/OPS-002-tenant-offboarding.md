# ADR-OPS-002: Tenant Offboarding ve KVKK Uyumu

**Durum:** Taslak
**Tarih:** 2026-04-19
**İlgili delta:** P2-2
**Kategori:** Operasyonel (OPS)

## Bağlam

KVKK m.7 ve GDPR right to erasure: müşteri ayrılırsa veri silme hakkı. Shared schema'da bu non-trivial + denetlenebilir olmalı. Ayrıca vergi mevzuatı bazı mali kayıtların 10 yıl saklanmasını zorunlu kılıyor.

## Karar

### İki Fazlı Silme

**Faz 1 — Soft Disable (Gün 0):**
- Tenant tüm modüllerden devre dışı bırakılır.
- Veri erişimi durur (Keycloak oturumları sonlandırılır).
- 90 gün grace period (geri dönme şansı).
- Durum: `tenants.offboarding_state = 'disabled'`.

**Faz 2 — Hard Delete (Gün 90):**
- Tüm tablolarda `DELETE WHERE tenant_id = ?` — cascade sırasına göre.
- Audit log'a silme sertifikası yazılır.
- Durum: `tenants.offboarding_state = 'deleted'`.

### Cascade Sırası

Yaprak tablolardan (PII yoğun) başla → aggregate'ler → tenant satırı:
1. `order_item_modifiers`, `order_items`
2. `payments` (PII anonymize, mali kayıt kalır — bkz: mali kayıt istisnası)
3. `orders`, `checks`, `tables`
4. `invoices` (PII anonymize, mali kayıt kalır)
5. `stock_movements`, `stock_levels`, `shipment_items`, `shipments`
6. `employee_profiles`, `branch_users`, `users`
7. `branches`, `branch_settings`
8. `tenants`

`ON DELETE CASCADE` **kullanılmaz** — explicit script ile, her adım loglanır.

### Mali Kayıt İstisnası

Vergi mevzuatı gereği `payment`, `invoice`, `fiscal_receipt` kayıtları 10 yıl saklanır. "Silme" = PII alanlarının anonymize edilmesi:
- Müşteri adı → `[REDACTED-{deterministic_hash}]`
- TC kimlik → kaldırılır
- Mali metadata (tutar, tarih, fiş no, vergi no) → kalır

### Silme Sertifikası

JSON formatında, imzalı hash + timestamp + tablo başına silinen satır sayısı. MinIO'da 10 yıl saklanır. İçerik:
```json
{
  "tenant_id": "...",
  "deleted_at": "2026-04-19T...",
  "tables": {"payments": 1234, "invoices": 567, ...},
  "anonymized": {"customer_names": 890},
  "signature": "SHA256:..."
}
```

### Keycloak Tarafı

Tenant'ın tüm kullanıcıları Keycloak'ta silinir veya anonymize edilir.

### Backup'lardaki Veri

Restore edilirse silme sertifikası ile birlikte yeniden silinir (re-apply delete script).

## İmplementasyon Detayları (Dolacak)

- Offboarding workflow Temporal'da mı (90 gün bekleme → Temporal timer iyi fit)?
- Anonymization fonksiyonu (deterministik hash, irreversible)
- Customer-facing "verilerimi sil" endpoint'i (KVKK hakkı)
- Legal hold (dava durumunda silmeme) mekanizması — operatör kararı

## Değerlendirilen Alternatifler

- **Anında hard delete:** Reddedildi. 90 gün grace period ticari standart + yanlışlık geri dönüşü.
- **ON DELETE CASCADE:** Reddedildi. Loglanamaz, denetlenemez.
- **Her şeyi anonymize et, silme:** Değerlendirildi. "Silme sertifikası" yetersiz kalır KVKK ispat için; hard delete + sertifika birlikte.

## Bağımlılık Notu (2026-07-05, task #14)

Identity (roles/memberships/role_permissions/role_field_policies) ve hr-core (employee_profiles) tablolarındaki cross-module FK'ler modül izolasyonu gereği kaldırıldı (identity/000011, hr-core/000002). Eski `ON DELETE CASCADE`/`RESTRICT` davranışları artık DB seviyesinde yok — offboarding implementasyonu tenant verisi temizliğini modül modül, public interface üzerinden açıkça yapmak zorunda (bu ADR'nin "CASCADE reddedildi" kararıyla zaten uyumlu; artık şema da bu kararla tutarlı).
