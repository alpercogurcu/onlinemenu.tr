-- Migration: inventory/000002_create_stock_items_warehouses
-- Adds the canonical stock-item and warehouse model (ADR-DATA-005 Faz 1 slice).
--
-- Design decisions:
--   - stock_items is the canonical "stocked entity" table: raw materials,
--     packaging, intermediates and finished goods. It is NOT the same table as
--     catalog.products (sellable items) — see ADR-DATA-005 İlke 1. There is no
--     product_type-style discriminator: `kind` is a plain classification where
--     every column means the same thing for every row (no per-kind meaning
--     shift, unlike the b2b `product_type` post-mortem).
--   - stock_items carries NO cost column (ADR-DATA-005 İlke 3): cost cascade is
--     impossible by construction. Cost snapshots land in manufacturing's
--     work_order_costs in Faz 2.
--   - canonical_unit is the ONLY unit column (ADR-DATA-005 İlke 2): no parallel
--     order_unit/sale_unit/atomic_unit family. Conversion happens once, at
--     write time, via unit_conversions; reads never convert.
--   - warehouses models both depo (distribution warehouse) and imalat
--     (manufacturing site) locations, per db-schema.md WAREHOUSES. branch_id is
--     a bare UUID (no FK): it points at tenant.branches, a different module
--     (cross-module FKs are forbidden by CLAUDE.md).
--   - unit_conversions is a tenant-INDEPENDENT reference table (kg<->g,
--     l<->ml, ...): conversion factors are physical constants, not a tenant
--     concern, so there is no tenant_id column and no RLS. Because
--     app_migrator (table owner) already has full access but app_runtime does
--     not implicitly get anything on a table it doesn't own, an explicit
--     GRANT SELECT to app_runtime is required so the runtime role can resolve
--     conversions at write time (ADR-SEC-002 two-role split: app_runtime has
--     no DDL/owner rights and only ever gets what is explicitly granted).
--
-- ADR references: ADR-DATA-005, ADR-SEC-001, ADR-SEC-002

-- ============================================================
-- warehouses
-- ============================================================
CREATE TABLE warehouses (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID        NOT NULL,
    branch_id       UUID        NOT NULL,                 -- tenant.branches(id); no FK (cross-module)
    name            TEXT        NOT NULL,
    warehouse_type  TEXT        NOT NULL
                        CHECK (warehouse_type IN ('depo', 'imalat')),
    is_active       BOOLEAN     NOT NULL DEFAULT TRUE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

COMMENT ON TABLE warehouses IS
    'Depo (distribution) or imalat (manufacturing site) location operated by a branch. '
    'Stock is always warehouse-scoped, never branch-scoped directly (ADR-DATA-005).';
COMMENT ON COLUMN warehouses.warehouse_type IS
    'depo = distribution warehouse, imalat = manufacturing site.';

CREATE INDEX warehouses_tenant_idx ON warehouses (tenant_id) WHERE is_active;
CREATE INDEX warehouses_branch_idx ON warehouses (branch_id);

ALTER TABLE warehouses ENABLE ROW LEVEL SECURITY;
ALTER TABLE warehouses FORCE ROW LEVEL SECURITY;

CREATE POLICY warehouses_read ON warehouses FOR SELECT TO app_runtime
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid);

CREATE POLICY warehouses_write ON warehouses FOR ALL TO app_runtime
    USING  (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid);

-- ============================================================
-- stock_items
-- ============================================================
CREATE TABLE stock_items (
    -- No DB-side DEFAULT: the id is generated client-side as a UUIDv7
    -- (time-ordered) by the service layer, mirroring the tenant module's
    -- Create() convention. PostgreSQL's native uuidv7() builtin is a PG18+
    -- feature and the project's test/dev Postgres image is pinned to pg17
    -- (see inventory/repo/integration_test.go), so a DB-side default would be
    -- environment-dependent; client-side generation via github.com/google/uuid
    -- works identically on every supported Postgres version.
    id              UUID        PRIMARY KEY,
    tenant_id       UUID        NOT NULL,
    sku             TEXT        NOT NULL,
    name            TEXT        NOT NULL,
    kind            TEXT        NOT NULL
                        CHECK (kind IN ('raw', 'intermediate', 'packaging', 'finished')),
    canonical_unit  TEXT        NOT NULL,
    category        TEXT,
    is_active       BOOLEAN     NOT NULL DEFAULT TRUE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE (tenant_id, sku)
);

COMMENT ON TABLE stock_items IS
    'Canonical stocked entity: raw material, packaging, intermediate or finished good. '
    'Never a sellable-product row (that stays in catalog.products) — ADR-DATA-005 İlke 1. '
    'No cost column: cascade is impossible by construction (İlke 3).';
COMMENT ON COLUMN stock_items.kind IS
    'Plain classification, not a discriminator: every column means the same thing for '
    'every kind. raw|intermediate|packaging|finished.';
COMMENT ON COLUMN stock_items.canonical_unit IS
    'The single unit of record for this item. All quantities in stock_levels and '
    'stock_movements for this item are in this unit; conversion happens once, at '
    'write time, via unit_conversions (ADR-DATA-005 İlke 2).';

CREATE INDEX stock_items_tenant_idx ON stock_items (tenant_id) WHERE is_active;
CREATE INDEX stock_items_kind_idx ON stock_items (tenant_id, kind);

ALTER TABLE stock_items ENABLE ROW LEVEL SECURITY;
ALTER TABLE stock_items FORCE ROW LEVEL SECURITY;

CREATE POLICY stock_items_read ON stock_items FOR SELECT TO app_runtime
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid);

CREATE POLICY stock_items_write ON stock_items FOR ALL TO app_runtime
    USING  (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid);

-- ============================================================
-- unit_conversions
-- Tenant-independent reference table: physical unit conversion factors are
-- constants, not tenant data. No tenant_id column, no RLS — but app_runtime
-- needs an explicit SELECT grant because it is not the table owner
-- (app_migrator) and PostgreSQL grants nothing implicitly across roles.
-- ============================================================
CREATE TABLE unit_conversions (
    from_unit   TEXT            NOT NULL,
    to_unit     TEXT            NOT NULL,
    factor      NUMERIC(18,6)   NOT NULL CHECK (factor > 0),  -- 1 from_unit = factor * to_unit
    PRIMARY KEY (from_unit, to_unit)
);

COMMENT ON TABLE unit_conversions IS
    'Tenant-independent reference table of unit conversion factors (ADR-DATA-005 İlke 2). '
    'Global physical constants: no tenant_id, no RLS. app_runtime is granted explicit '
    'SELECT (see GRANT below) since it is not the owning role.';
COMMENT ON COLUMN unit_conversions.factor IS
    '1 unit of from_unit equals `factor` units of to_unit.';

GRANT SELECT ON unit_conversions TO app_runtime;

INSERT INTO unit_conversions (from_unit, to_unit, factor) VALUES
    ('kg', 'g', 1000),
    ('g', 'kg', 0.001),
    ('l', 'ml', 1000),
    ('ml', 'l', 0.001),
    ('adet', 'adet', 1),
    ('kg', 'kg', 1),
    ('g', 'g', 1),
    ('l', 'l', 1),
    ('ml', 'ml', 1)
ON CONFLICT (from_unit, to_unit) DO NOTHING;
