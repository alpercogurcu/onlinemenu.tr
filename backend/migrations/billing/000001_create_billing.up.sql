-- Billing module: invoices, invoice_items, billing_outbox.
-- All tables enforce RLS.  Cross-module references are bare UUIDs (no FK).

SET LOCAL role = app_migrator;

-- ─── invoices ────────────────────────────────────────────────────────────────
-- One row per e-invoice / e-archive document issued to a customer.
-- Amounts are stored in the smallest currency unit (kuruş for TRY).
-- check_id / payment_id are bare UUIDs — no FK reference to pos/payment modules.
CREATE TABLE IF NOT EXISTS invoices (
    id                      UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id               UUID        NOT NULL,
    branch_id               UUID        NOT NULL,
    invoice_type            TEXT        NOT NULL
        CHECK (invoice_type IN ('e_fatura', 'e_arsiv')),
    status                  TEXT        NOT NULL DEFAULT 'draft'
        CHECK (status IN ('draft', 'pending_submission', 'submitted', 'accepted', 'rejected', 'cancelled')),
    -- reference to POS session (bare UUID; no cross-module FK)
    check_id                UUID,
    payment_id              UUID,
    -- idempotency — uniquely identifies the generation request per tenant
    idempotency_key         TEXT        NOT NULL,
    -- invoice numbering (set on submission)
    invoice_number          TEXT        NOT NULL DEFAULT '',
    -- GİB / provider
    gib_uuid                UUID        NOT NULL DEFAULT gen_random_uuid(),
    external_id             TEXT        NOT NULL DEFAULT '',  -- INTL_TXN_ID from provider
    -- supplier (tenant side — snapshotted at invoice time)
    supplier_vkn            TEXT        NOT NULL,
    supplier_name           TEXT        NOT NULL,
    supplier_alias          TEXT        NOT NULL DEFAULT '',  -- GİB posta kutusu alias
    -- customer (snapshotted)
    customer_vkn            TEXT        NOT NULL DEFAULT '',
    customer_name           TEXT        NOT NULL DEFAULT '',
    customer_alias          TEXT        NOT NULL DEFAULT '',  -- GİB posta kutusu alias (if e-fatura)
    -- amounts (kuruş)
    amount_excluding_tax    BIGINT      NOT NULL DEFAULT 0,
    tax_amount              BIGINT      NOT NULL DEFAULT 0,
    amount_total            BIGINT      NOT NULL DEFAULT 0,
    currency                TEXT        NOT NULL DEFAULT 'TRY',
    -- lifecycle timestamps
    issue_date              DATE        NOT NULL DEFAULT CURRENT_DATE,
    submitted_at            TIMESTAMPTZ,
    accepted_at             TIMESTAMPTZ,
    rejected_at             TIMESTAMPTZ,
    rejection_reason        TEXT        NOT NULL DEFAULT '',
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS invoices_tenant_idempotency
    ON invoices (tenant_id, idempotency_key);

CREATE INDEX IF NOT EXISTS invoices_tenant_branch_status
    ON invoices (tenant_id, branch_id, status);

CREATE INDEX IF NOT EXISTS invoices_check_id_idx
    ON invoices (tenant_id, check_id) WHERE check_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS invoices_created_at_idx
    ON invoices (tenant_id, created_at DESC);

ALTER TABLE invoices ENABLE ROW LEVEL SECURITY;
ALTER TABLE invoices FORCE ROW LEVEL SECURITY;

CREATE POLICY invoices_tenant_isolation ON invoices
    USING      (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid);

GRANT SELECT, INSERT, UPDATE ON invoices TO app_runtime;

-- ─── invoice_items ───────────────────────────────────────────────────────────
-- Line items; product data is snapshotted at invoice generation time.
CREATE TABLE IF NOT EXISTS invoice_items (
    id                  UUID    PRIMARY KEY DEFAULT gen_random_uuid(),
    invoice_id          UUID    NOT NULL,
    tenant_id           UUID    NOT NULL,
    -- product snapshot
    product_id          UUID,
    product_name        TEXT    NOT NULL,
    quantity            INT     NOT NULL CHECK (quantity > 0),
    unit_price_amount   BIGINT  NOT NULL,   -- KDV Hariç kuruş
    tax_rate_bps        INT     NOT NULL DEFAULT 0,  -- basis points (800 = 8%)
    line_total          BIGINT  NOT NULL,   -- KDV Hariç satır toplamı
    tax_amount          BIGINT  NOT NULL DEFAULT 0,  -- satır KDV tutarı
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS invoice_items_invoice_idx
    ON invoice_items (invoice_id);

ALTER TABLE invoice_items ENABLE ROW LEVEL SECURITY;
ALTER TABLE invoice_items FORCE ROW LEVEL SECURITY;

CREATE POLICY invoice_items_tenant_isolation ON invoice_items
    USING      (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid);

GRANT SELECT, INSERT ON invoice_items TO app_runtime;

-- ─── billing_outbox ──────────────────────────────────────────────────────────
-- Transactional outbox for billing domain events (ADR-DATA-001).
CREATE TABLE IF NOT EXISTS billing_outbox (
    event_id        UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID        NOT NULL,
    aggregate_type  TEXT        NOT NULL,
    aggregate_id    UUID        NOT NULL,
    event_type      TEXT        NOT NULL,
    payload         JSONB       NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    processed_at    TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS billing_outbox_unprocessed_idx
    ON billing_outbox (created_at) WHERE processed_at IS NULL;

ALTER TABLE billing_outbox ENABLE ROW LEVEL SECURITY;
ALTER TABLE billing_outbox FORCE ROW LEVEL SECURITY;

CREATE POLICY billing_outbox_tenant_isolation ON billing_outbox
    USING      (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid);

GRANT SELECT, INSERT, UPDATE ON billing_outbox TO app_runtime;
