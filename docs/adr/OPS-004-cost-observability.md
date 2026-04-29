# ADR-OPS-004: Cost Observability

**Durum:** Taslak
**Tarih:** 2026-04-19
**İlgili delta:** P2-4
**Kategori:** Operasyonel (OPS)

## Bağlam

Modül-modül satış için her tenant'ın gerçek kaynak maliyeti bilinmeli. Yanlış fiyatlama = zarar. Tenant-tagged metric'ler baştan planlanmazsa sonradan eklemek cost attribution'ı geriye dönük mümkün değil — bu kararın early yazılması kritik.

## Karar

### Tenant-Tagged Metrics

Tüm OpenTelemetry metric'leri `tenant_id` label'ı taşır. Prometheus scraping bu label ile maliyet atfı yapılabilir.

**Kardinalite yönetimi:**
- 10k tenant altında: doğrudan `tenant_id` label
- 10k+ tenant: top-100 tam label, alt: bucket hash (örn: `tenant_bucket_042`)

### Maliyet Kalemleri

| Kaynak | Ölçüm Yöntemi |
|---|---|
| PostgreSQL | `pg_stat_user_tables` + `pg_total_relation_size` tenant bazlı sorgu (her gece) |
| NATS | Subject bazlı throughput — `tenant_id` zaten subject'in parçası |
| MinIO | Prefix-per-tenant usage API (`minio mc du`) |
| Compute (Faz 2+) | Kubernetes pod `tenant_id` label (workload bazlı maliyet) |

### Raporlama

1. **Aylık tenant usage report:** Hesaplanmış DB boyutu + NATS throughput + MinIO kullanımı → admin panel + opsiyonel customer self-service dashboard.
2. **Fiyatlama feedback döngüsü:** `actual_cost / plan_price` oranı < 0.8 olan tenant'lar → plan upgrade önerisi için alert.

### Maliyet Veri Akışı

```
Postgres pgstats → daily cron → tenant_usage tablosu
NATS metrics → OTel collector → Prometheus
MinIO usage → daily cron → tenant_usage tablosu
→ Grafana dashboard (tenant maliyet görünümü)
→ Aylık rapor üretme job'u (asynq)
```

## İmplementasyon Detayları (Dolacak)

- `tenant_usage` tablosu şeması (hangi metrikler, hangi granülarité)
- Prometheus kardinalite limitleri ve bucketting eşik değerleri
- Alerting threshold'ları (margin < %20 ise uyarı)
- Customer self-service erişim seviyesi (kendi maliyetini görebilir mi?)

## Değerlendirilen Alternatifler

- **Tenant başına ayrı Prometheus scrape job:** Reddedildi. N tenant = N job = Prometheus operasyonel cehennem.
- **OpenCost / cloud cost management tool:** Değerlendirildi. Bu araçlar cloud VM maliyeti için; DB/NATS/MinIO coverage yok, custom.
- **Geriye dönük ekleme:** Reddedildi. Metric serisi baştan etiketlenmezse attribution imkânsız.
