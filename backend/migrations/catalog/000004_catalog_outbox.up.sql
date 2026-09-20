-- Migration: catalog/000004_catalog_outbox (ADR-DATA-001, ADR-DATA-002)
--
-- Catalog modulunun ilk outbox tablosu. Sube override degisiklikleri
-- (catalog.branch_override.changed.v1) buradan yayimlanir.
--
-- Kolonlar pos_outbox'un NIHAI haliyle birebir ayni: temel alanlar +
-- retry/dead-letter (000002) + claim (000003). Dispatcher'in claim sorgusu bu
-- kolonlarin hepsini bekler, bu yuzden tablo ilk gunden tam olarak yaratilir.

CREATE TABLE catalog_outbox (
    event_id        UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID        NOT NULL,
    aggregate_type  TEXT        NOT NULL,
    aggregate_id    UUID        NOT NULL,
    event_type      TEXT        NOT NULL,
    payload         JSONB       NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    processed_at    TIMESTAMPTZ,
    retry_count     INT         NOT NULL DEFAULT 0,
    next_retry_at   TIMESTAMPTZ,
    last_error      TEXT,
    is_dead         BOOLEAN     NOT NULL DEFAULT FALSE,
    claimed_at      TIMESTAMPTZ
);

ALTER TABLE catalog_outbox ENABLE ROW LEVEL SECURITY;
ALTER TABLE catalog_outbox FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation ON catalog_outbox
    USING      (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid);

GRANT SELECT, INSERT, UPDATE ON catalog_outbox TO app_runtime;

CREATE INDEX catalog_outbox_dispatchable_idx ON catalog_outbox (created_at)
    WHERE processed_at IS NULL AND is_dead = FALSE;
