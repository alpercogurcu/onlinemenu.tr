-- Per-branch POS preferences: today the waiter screen's category layout and
-- the branch's order flow ("full" keeps the accept/advance lifecycle, "simple"
-- is for branches without a kitchen display — orders are born accepted and a
-- check close delivers whatever is still live).
--
-- One row per branch, written lazily on the first admin change — the absence
-- of a row means every default applies (top/full), so existing branches keep
-- their behaviour without a backfill. Mirrors storefront/000002_branch_settings.
--
-- branch_id is a bare UUID on purpose: it belongs to the tenant module, and
-- cross-module foreign keys are forbidden.
CREATE TABLE pos_branch_settings (
    tenant_id              UUID        NOT NULL,
    branch_id              UUID        NOT NULL,
    waiter_category_layout TEXT        NOT NULL DEFAULT 'top'
                                       CHECK (waiter_category_layout IN ('top', 'side')),
    order_flow             TEXT        NOT NULL DEFAULT 'full'
                                       CHECK (order_flow IN ('full', 'simple')),
    updated_by             UUID        NOT NULL,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (branch_id)
);

ALTER TABLE pos_branch_settings ENABLE ROW LEVEL SECURITY;
ALTER TABLE pos_branch_settings FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation ON pos_branch_settings
    USING      (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid);

GRANT SELECT, INSERT, UPDATE ON pos_branch_settings TO app_runtime;
