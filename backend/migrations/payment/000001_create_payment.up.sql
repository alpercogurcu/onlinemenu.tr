-- Payment module tables: payments, fiscal_receipts, payment_outbox.
-- All tables carry FORCE ROW LEVEL SECURITY so app_runtime cannot read
-- another tenant's rows regardless of the calling code.

SET LOCAL role = app_migrator;

-- ─── payments ───────────────────────────────────────────────────────────────
-- One row per payment transaction.  check_id and order_ids are bare UUIDs
-- (no FK references) to keep payment independent of the POS module.
CREATE TABLE IF NOT EXISTS payments (
    id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id           UUID        NOT NULL,
    branch_id           UUID        NOT NULL,
    check_id            UUID,                           -- nullable; delivery orders may have no check
    idempotency_key     TEXT        NOT NULL,
    method              TEXT        NOT NULL            -- cash | terminal
        CHECK (method IN ('cash', 'terminal')),
    status              TEXT        NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'completed', 'failed')),
    amount_total        BIGINT      NOT NULL CHECK (amount_total > 0),
    currency            TEXT        NOT NULL DEFAULT 'TRY',
    fiscal_receipt_id   UUID,                           -- set after fiscal registration
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at        TIMESTAMPTZ
);

-- Uniqueness per tenant prevents the same idempotency key from creating two
-- payments even under concurrent retries.
CREATE UNIQUE INDEX IF NOT EXISTS payments_tenant_idempotency_key
    ON payments (tenant_id, idempotency_key);

CREATE INDEX IF NOT EXISTS payments_tenant_check_idx ON payments (tenant_id, check_id)
    WHERE check_id IS NOT NULL;

ALTER TABLE payments ENABLE ROW LEVEL SECURITY;
ALTER TABLE payments FORCE ROW LEVEL SECURITY;

CREATE POLICY payments_tenant_isolation ON payments
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid);

-- ─── fiscal_receipts ────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS fiscal_receipts (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID        NOT NULL,
    payment_id      UUID        NOT NULL,
    device_type     TEXT        NOT NULL DEFAULT 'mock',
    receipt_number  TEXT        NOT NULL,
    receipt_data    JSONB       NOT NULL DEFAULT '{}',
    issued_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS fiscal_receipts_payment_idx
    ON fiscal_receipts (tenant_id, payment_id);

ALTER TABLE fiscal_receipts ENABLE ROW LEVEL SECURITY;
ALTER TABLE fiscal_receipts FORCE ROW LEVEL SECURITY;

CREATE POLICY fiscal_receipts_tenant_isolation ON fiscal_receipts
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid);

-- ─── payment_outbox ─────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS payment_outbox (
    event_id        UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID        NOT NULL,
    aggregate_type  TEXT        NOT NULL,
    aggregate_id    TEXT        NOT NULL,
    event_type      TEXT        NOT NULL,
    payload         JSONB       NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    processed_at    TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS payment_outbox_unprocessed_idx
    ON payment_outbox (created_at) WHERE processed_at IS NULL;

ALTER TABLE payment_outbox ENABLE ROW LEVEL SECURITY;
ALTER TABLE payment_outbox FORCE ROW LEVEL SECURITY;

CREATE POLICY payment_outbox_tenant_isolation ON payment_outbox
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid);

-- Grant app_runtime DML on all three tables.
GRANT SELECT, INSERT, UPDATE ON payments         TO app_runtime;
GRANT SELECT, INSERT          ON fiscal_receipts TO app_runtime;
GRANT SELECT, INSERT, UPDATE  ON payment_outbox  TO app_runtime;
