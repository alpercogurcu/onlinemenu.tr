-- POS module: table_zones + tables (floor plan) — docs/db-schema.md TABLE_ZONES/TABLES.
-- checks.table_id is added as an optional, modul-internal FK (checks and tables both
-- live in pos). branch_id stays a bare UUID everywhere (cross-module FK forbidden).

-- table_zones: groups tables within a branch (a floor, a terrace, etc.)
CREATE TABLE table_zones (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   UUID        NOT NULL,
    branch_id   UUID        NOT NULL,
    name        TEXT        NOT NULL,
    floor       INT         NOT NULL DEFAULT 0,
    is_active   BOOLEAN     NOT NULL DEFAULT TRUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

ALTER TABLE table_zones ENABLE ROW LEVEL SECURITY;
ALTER TABLE table_zones FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation ON table_zones
    USING      (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid);

GRANT SELECT, INSERT, UPDATE ON table_zones TO app_runtime;

CREATE INDEX table_zones_tenant_branch_idx ON table_zones (tenant_id, branch_id) WHERE is_active;

-- tables: a physical dine-in table within a zone
CREATE TABLE tables (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID        NOT NULL,
    branch_id       UUID        NOT NULL,
    zone_id         UUID        NOT NULL REFERENCES table_zones (id) ON DELETE RESTRICT,
    name            TEXT        NOT NULL,
    capacity        INT         NOT NULL DEFAULT 0 CHECK (capacity >= 0),
    status          TEXT        NOT NULL DEFAULT 'empty'
                                CHECK (status IN ('empty', 'occupied', 'reserved', 'cleaning')),
    layout_position JSONB       NOT NULL DEFAULT '{}'::jsonb,
    is_active       BOOLEAN     NOT NULL DEFAULT TRUE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

ALTER TABLE tables ENABLE ROW LEVEL SECURITY;
ALTER TABLE tables FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation ON tables
    USING      (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid);

GRANT SELECT, INSERT, UPDATE ON tables TO app_runtime;

CREATE INDEX tables_tenant_branch_zone_idx ON tables (tenant_id, branch_id, zone_id) WHERE is_active;
CREATE INDEX tables_zone_id_idx ON tables (zone_id);

-- checks.table_id: optional link from an adisyon to the table it was opened
-- against. Nullable — takeaway/delivery "masasız satış" (paket servis) checks
-- never set it, and table_label keeps rendering the same for receipts/KDS.
ALTER TABLE checks ADD COLUMN table_id UUID REFERENCES tables (id) ON DELETE RESTRICT;

CREATE INDEX checks_table_id_idx ON checks (table_id) WHERE table_id IS NOT NULL;

-- Defense-in-depth backstop for CheckService.Open's row-lock-based
-- serialization (see check_service.go Open): at most one OPEN check may ever
-- reference the same table at a time.
CREATE UNIQUE INDEX checks_open_table_id_uidx ON checks (table_id) WHERE status = 'open' AND table_id IS NOT NULL;
