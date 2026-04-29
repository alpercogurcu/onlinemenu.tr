# ADR-OPS-001: Backup ve Disaster Recovery

**Durum:** Taslak
**Tarih:** 2026-04-19
**İlgili delta:** P2-1
**Kategori:** Operasyonel (OPS)

## Bağlam

Baseline'da backup/DR planı yoktu. Shared-schema multi-tenant'ta "tek tenant'ı geri al" ihtiyacı non-trivial:
- Cluster-level PITR mümkün ama tek tenant'ı etkilemiyor (tüm veriyi geri alır).
- Tenant-level recovery: soft delete + audit log + event replay kombinasyonu gerekiyor.

## Karar

1. **Cluster-level backup:** pgBackRest veya WAL-G. Prod'da saatlik full + sürekli WAL shipping.
   - RPO hedefi: < 15 dakika
   - RTO hedefi: < 1 saat

2. **Point-in-time recovery (PITR):** Cluster bazında, major incident senaryolarında. Tek tenant için PITR mümkün değil (shared schema).

3. **Tenant-level logical recovery:** Event sourcing-lite yaklaşımı.
   - Audit log + outbox event replay
   - Soft delete + 30 gün restore window
   - "3 saat önceki menüye dön" = `catalog.*.v1` event'lerini belirli timestamp'e kadar replay

4. **Mali kayıt istisnası:** `payment`, `invoice`, `fiscal` tablolarında hard delete **yasak** (ADR-FISCAL-001 gereği de). 10 yıl saklama zorunlu.

5. **Soft delete politikası:** Tüm tablolarda `deleted_at TIMESTAMPTZ` kolonu; mali kayıtlar için 10 yıl, diğerleri için 30 gün restore window.

6. **Backup verification:** Haftalık otomatik staging restore + smoke test. Sonuçlar monitoring'de raporlanır.

## İmplementasyon Detayları (Dolacak)

- pgBackRest vs WAL-G seçimi (Faz 2 başı)
- S3/MinIO backup hedefi ve encryption at rest
- Restore playbook (operasyonel runbook)
- Staging restore automation CI/CD entegrasyonu
- Monitoring: backup başarı/başarısızlık alertleri

## Değerlendirilen Alternatifler

- **Yalnız cluster PITR:** Reddedildi. Tek tenant recovery için tüm cluster'ı geri almak kabul edilemez.
- **Partition-per-tenant:** Değerlendirilecek Faz 3+. Tenant-level PITR sağlar ama başlangıçta aşırı karmaşık.
