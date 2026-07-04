-- Migration: inventory/000006_create_supply_policies
-- Creates supply_policies (ADR-DATA-007): the commercial procurement rule
-- that determines how a branch may source a stock item/category — from HQ
-- exclusively, from an approved supplier list, or freely (pazar/market).
--
-- Design decisions:
--   - Policy changes are NEVER an UPDATE (DATA-002 immutability ruhu): a
--     change inserts a NEW row with a later effective_from. The active
--     policy for a resolution key is the row with the greatest
--     effective_from <= now() (see domain.ResolvePolicy, the pure resolver
--     that reads the full candidate set and picks the winner in Go — there
--     is no "is_active" flag or partial index enforcing this in SQL).
--   - branch_id is a bare nullable UUID (tenant.branches; no FK, cross-module
--     FK forbidden by CLAUDE.md), mirroring branch_transfer_orders'
--     requesting_branch_id/source_branch_id convention (migration 000005).
--     NULL = tenant-wide rule; non-NULL = a per-branch (franchise) override.
--     v1 only ever WRITES tenant-wide rows (branch_id IS NULL) — see
--     service/supply_policy_service.go — but the column and the resolver's
--     priority order are in place from day one so a later Faz can start
--     writing branch overrides with no migration or resolver change.
--   - category is TEXT, matching stock_items.category (also TEXT, no FK):
--     scope=category resolves by exact text equality against
--     stock_items.category, not against a separate category table (ADR
--     Open Decision #4 — deferred; a relational stock_categories table is a
--     later refactor if/when needed).
--   - stock_item_id DOES get a real intra-module FK to stock_items(id):
--     unlike branch_id/created_by, this is the same module.
--   - approved_supplier_ids is JSONB (party.suppliers id list): a jsonb
--     array of UUID strings, only meaningful when mode='approved_suppliers'.
--     No FK (party is a different module; also jsonb arrays cannot carry a
--     relational FK regardless).
--   - id has no DB-side DEFAULT: generated client-side as a UUIDv7, mirroring
--     stock_items' convention (migration 000002) — see
--     service/supply_policy_service.go.
--   - created_by is a bare nullable UUID (identity.persons; no FK,
--     cross-module), same convention as branch_transfer_orders.created_by.
--
-- ADR references: ADR-DATA-007, ADR-DATA-002, ADR-SEC-001, ADR-SEC-002

CREATE TABLE supply_policies (
    id                     UUID            PRIMARY KEY,
    tenant_id              UUID            NOT NULL,
    branch_id              UUID,                                -- tenant.branches(id); no FK (cross-module). NULL = tenant-wide.
    scope                  TEXT            NOT NULL
                               CHECK (scope IN ('stock_item', 'category', 'tenant_default')),
    stock_item_id          UUID            REFERENCES stock_items (id),
    category               TEXT,
    mode                   TEXT            NOT NULL
                               CHECK (mode IN ('exclusive_hq', 'approved_suppliers', 'free')),
    approved_supplier_ids  JSONB,                                -- party.suppliers(id) list; only meaningful when mode='approved_suppliers'
    effective_from         TIMESTAMPTZ     NOT NULL,
    created_by             UUID,                                 -- identity.persons(id); no FK (cross-module)
    created_at             TIMESTAMPTZ     NOT NULL DEFAULT NOW(),

    CHECK (
        (scope = 'stock_item'     AND stock_item_id IS NOT NULL AND category IS NULL) OR
        (scope = 'category'       AND category IS NOT NULL AND stock_item_id IS NULL) OR
        (scope = 'tenant_default' AND stock_item_id IS NULL AND category IS NULL)
    )
);

COMMENT ON TABLE supply_policies IS
    'Time-versioned commercial procurement rule (ADR-DATA-007): decides whether '
    'a branch sources a stock item/category exclusively from HQ, from an '
    'approved supplier list, or freely. Lives separately from stock_items — '
    'this is a contract, not an item property. Rows are immutable: a policy '
    'change is a new row with a later effective_from, never an UPDATE.';
COMMENT ON COLUMN supply_policies.branch_id IS
    'NULL = tenant-wide default; non-NULL = per-branch (franchise) override. '
    'v1 only writes tenant-wide rows; the column exists so a later Faz can '
    'start writing branch overrides without a schema change.';
COMMENT ON COLUMN supply_policies.category IS
    'Exact-text match against stock_items.category (also TEXT, no FK). Only '
    'set when scope=category.';
COMMENT ON COLUMN supply_policies.effective_from IS
    'Resolution key: the winning row for a given (branch_id, scope, ref) is '
    'the one with the greatest effective_from <= now(). See '
    'domain.ResolvePolicy.';

CREATE INDEX supply_policies_tenant_idx ON supply_policies (tenant_id);
CREATE INDEX supply_policies_lookup_idx
    ON supply_policies (tenant_id, branch_id, scope, stock_item_id, category, effective_from DESC);

ALTER TABLE supply_policies ENABLE ROW LEVEL SECURITY;
ALTER TABLE supply_policies FORCE ROW LEVEL SECURITY;

CREATE POLICY supply_policies_read ON supply_policies FOR SELECT TO app_runtime
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid);

CREATE POLICY supply_policies_write ON supply_policies FOR ALL TO app_runtime
    USING  (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid);
