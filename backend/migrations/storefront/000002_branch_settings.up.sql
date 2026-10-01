-- Per-branch storefront settings: today only "may guests place orders here".
-- One row per branch, written lazily on the first admin change — the absence
-- of a row means every default applies (ordering enabled), so existing
-- branches keep their behaviour without a backfill.
--
-- branch_id is a bare UUID on purpose: it belongs to pos, and cross-module
-- foreign keys are forbidden (see migrations/storefront/000001 header).
CREATE TABLE storefront_branch_settings (
    tenant_id        UUID        NOT NULL,
    branch_id        UUID        NOT NULL,
    ordering_enabled BOOLEAN     NOT NULL DEFAULT TRUE,
    updated_by       UUID        NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (branch_id)
);

ALTER TABLE storefront_branch_settings ENABLE ROW LEVEL SECURITY;
ALTER TABLE storefront_branch_settings FORCE ROW LEVEL SECURITY;

-- No token-hash bootstrap branch here (unlike qr_codes_read): this table is
-- only ever read AFTER a guest session is resolved, inside a tenant-scoped
-- transaction, so the plain tenant_isolation predicate covers every reader.
CREATE POLICY tenant_isolation ON storefront_branch_settings
    USING      (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid);

GRANT SELECT, INSERT, UPDATE ON storefront_branch_settings TO app_runtime;
