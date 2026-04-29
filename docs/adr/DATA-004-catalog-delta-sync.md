# ADR-DATA-004: Catalog Delta Sync

**Durum:** Taslak
**Tarih:** 2026-04-19
**İlgili delta:** P2-5
**Kategori:** Veri / Event (DATA)

## Bağlam

Baseline edge'e tam catalog snapshot gönderiyor. 10k SKU'lu market zincirinde her fiyat güncellemesinde tam snapshot:
- Gereksiz network trafiği (büyük payload)
- Edge'de I/O yükü (SQLite tam rewrite)
- Birden fazla şube aynı anda güncellenirken bandwidth saturation

Faz 1'de tam snapshot sorunsuz çalışır; Faz 2'de 100+ şube × günde birkaç güncelleme çarpımında sorun olur.

## Karar

### Faz 1: Tam Snapshot + Versiyon Sayacı

- Edge son snapshot version'ını saklar (`catalog_version`).
- Cloud'daki versiyon değiştiyse tam snapshot çeker.
- Değişmediyse istek yapmaz.

### Faz 2: Event-Based Incremental Delta

Event tipleri (`contracts/events/catalog/`):
- `catalog.product.created.v1`
- `catalog.product.updated.v1`
- `catalog.product.deactivated.v1`
- `catalog.price.changed.v1`
- `catalog.variant.added.v1`

Edge son işlediği `catalog_version`'dan sonrasını event replay ile günceller.

**Drift detection:** Edge periyodik olarak catalog checksum gönderir; cloud mismatch tespit ederse tam snapshot tetikler (corrective action).

**Back-fill:** Yeni şube eklendiğinde tam snapshot + sonrası delta.

## İmplementasyon Detayları (Dolacak)

- Checksum algoritması (MD5 yeterli mi, SHA-256 mı?)
- Event ordering — aggregate (product) başına sıralı garanti nasıl verilir?
- Drift detection periyodu (önerilen: 1 saatte bir)

## Değerlendirilen Alternatifler

- **Her zaman tam snapshot:** Reddedildi (Faz 2+ için ölçeklenmiyor).
- **Polling + timestamp:** Değerlendirildi. Event-based daha reliable; timestamp-based race condition riski var.
