# ADR-DATA-001: Outbox Dispatcher Mimarisi

**Durum:** Kabul Edildi
**Tarih:** 2026-04-19
**İlgili delta:** P0-5
**Kategori:** Veri / Event (DATA)

## Bağlam

Baseline'da outbox pattern var ama dispatcher'ın nasıl çalışacağı belirsizdi. "DB'ye yaz + NATS'a publish" dual-write sorununu çözen outbox, yanlış implement edilirse:
- Naif polling paralel dispatcher'da sıralama bozar.
- Dispatcher crash olursa event'ler stuck kalır veya çift publish olur.
- Retry stratejisi yoksa poison message tüm queue'yu tıkar.

## Karar

**Faz 0-1 dispatcher:** Postgres `LISTEN/NOTIFY` + `FOR UPDATE SKIP LOCKED` polling kombinasyonu.

### Outbox Tablosu (her modülde)

```sql
CREATE TABLE <module>_outbox (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL,
    aggregate_type TEXT NOT NULL,
    aggregate_id UUID NOT NULL,
    event_type TEXT NOT NULL,
    event_version INT NOT NULL,
    payload JSONB NOT NULL,
    subject TEXT NOT NULL,             -- NATS subject
    is_synced BOOLEAN NOT NULL DEFAULT FALSE,
    is_dead BOOLEAN NOT NULL DEFAULT FALSE,
    retry_count INT NOT NULL DEFAULT 0,
    next_retry_at TIMESTAMPTZ,
    last_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    synced_at TIMESTAMPTZ
);
CREATE INDEX ON <module>_outbox (is_synced, next_retry_at) WHERE is_synced = FALSE AND is_dead = FALSE;
CREATE INDEX ON <module>_outbox (aggregate_id, id) WHERE is_synced = FALSE;
```

### LISTEN/NOTIFY Trigger

```sql
CREATE OR REPLACE FUNCTION notify_outbox() RETURNS TRIGGER AS $$
BEGIN
    PERFORM pg_notify('outbox_new', TG_TABLE_NAME);
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER <module>_outbox_notify
    AFTER INSERT ON <module>_outbox
    FOR EACH ROW EXECUTE FUNCTION notify_outbox();
```

### Dispatcher Goroutine

- Uygulama binary'si içinde çalışır (ayrı worker değil; lifecycle app ile aynı).
- LISTEN `outbox_new` kanalında bekler. Notify gelince tetiklenir + 5 sn fallback polling.
- Her tick'te:
  ```sql
  SELECT * FROM <module>_outbox
  WHERE is_synced = FALSE AND is_dead = FALSE
    AND (next_retry_at IS NULL OR next_retry_at <= now())
  ORDER BY aggregate_id, id
  FOR UPDATE SKIP LOCKED
  LIMIT 100;
  ```

### Aggregate-Based Sıralama

`aggregate_id` hash'i ile goroutine partition. Aynı aggregate için event'ler sıralı işlenir. Farklı aggregate'ler paralel.

### Publish

NATS JetStream'e `Nats-Msg-Id: <outbox.id>` header ile. NATS dedupe window (2 dakika) cross-crash koruma sağlar.

### Başarısızlık + Retry

```sql
UPDATE <module>_outbox SET
    retry_count = retry_count + 1,
    next_retry_at = now() + backoff(retry_count),
    last_error = $2
WHERE id = $1;
```
Backoff: `min(60s, 2^retry_count + random(0,1000ms))`.

### Poison Message

`retry_count > 10` → `is_dead = TRUE`, `dlq_events` tablosuna kopyalanır, Prometheus `outbox_dead_total` metric alarmı tetiklenir. Admin API: `GET /v1/ops/dlq`.

### Metrics (OpenTelemetry)

- `outbox_pending_total{module}`
- `outbox_dispatch_duration_seconds`
- `outbox_dispatch_failures_total{module, reason}`
- `outbox_dead_total{module}`

### Edge (Local Server)

SQLite'ta `LISTEN/NOTIFY` yok → 500ms interval polling. NATS upstream üzerinden cloud'a publish.

### Faz 2 Debezium Geçişi

Outbox tabloları `REPLICA IDENTITY FULL` ayarlanır. Dispatcher Debezium'a taşınır. Uygulama kodu değişmez.

## Sonuçlar

**İyi:**
- Crash-safe: event kalıcı DB'de, yeniden başladığında devam eder.
- Sıralama garantisi aggregate-başına; paralel throughput.
- Back-pressure: retry backoff downstream yük korur.

**Dikkat:**
- Her modülün kendi outbox tablosu var → migration + index yönetimi modül başına.
- LISTEN/NOTIFY uzun ömürlü ayrı connection gerektirir (pool'dan değil).
- Edge SQLite 500ms polling → cloud sync latency alt sınır 500ms.

**Risk:**
- Dispatcher ile app aynı binary → CPU spike. Ölçüm + HPA ayarı Faz 1'de.

## Değerlendirilen Alternatifler

- **Direct publish (outbox yok):** Reddedildi. Atomic değil; event kaybı/duplicate riski.
- **Debezium Faz 0'da:** Reddedildi. Operasyonel yük MVP için fazla.
- **pg_cron + polling:** Reddedildi. LISTEN/NOTIFY düşük latency; cron minimum 1dk.
- **Temporal workflow olarak dispatcher:** Reddedildi. Outbox Temporal'dan önce gelmeli.
