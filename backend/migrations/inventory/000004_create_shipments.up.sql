-- Migration: inventory/000004_create_shipments
-- Creates shipments + shipment_items (db-schema.md SHIPMENTS / SHIPMENT_ITEMS).
--
-- Design decisions:
--   - from_warehouse_id is a real intra-module FK to warehouses(id).
--   - to_branch_id is a bare UUID (tenant.branches(id)): cross-module FK
--     forbidden (CLAUDE.md). Same for created_by (identity.persons(id)).
--   - transfer_order_id (link to ADR-DATA-006 branch_transfer_orders) is
--     added in migration 000005, once that table exists — this migration
--     only creates the shipment itself, which is usable standalone (a
--     depot can ship without a BTO, e.g. manual restock of a franchise).
--   - status is the single source of truth for "received" (ADR-DATA-006
--     ownership rule): branch_transfer_orders.status is NEVER set to
--     'received' directly by application code — only derived from this
--     table's status transition (enforced in the service layer, not SQL).
--   - shipment_items has no surrogate id (mirrors catalog.menu_items):
--     the natural key (shipment_id, stock_item_id) is the PK — one line
--     per stock item per shipment.
--
-- ADR references: ADR-DATA-005, ADR-DATA-006, ADR-SEC-001, ADR-SEC-002

CREATE TABLE shipments (
    id                UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id         UUID        NOT NULL,
    from_warehouse_id UUID        NOT NULL REFERENCES warehouses (id),
    to_branch_id      UUID        NOT NULL,             -- tenant.branches(id); no FK (cross-module)
    status            TEXT        NOT NULL DEFAULT 'draft'
                          CHECK (status IN ('draft', 'approved', 'in_transit', 'received', 'cancelled')),
    priority          TEXT        NOT NULL DEFAULT 'normal'
                          CHECK (priority IN ('normal', 'urgent')),
    note              TEXT,
    created_by        UUID,                             -- identity.persons(id); no FK (cross-module)
    shipped_at        TIMESTAMPTZ,
    received_at       TIMESTAMPTZ,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

COMMENT ON TABLE shipments IS
    'Physical stock movement from a warehouse to a branch. Sole owner of the '
    '"received" fact (ADR-DATA-006): branch_transfer_orders.status never sets '
    'received directly, it is derived from this table''s transition to received.';

CREATE INDEX shipments_tenant_idx ON shipments (tenant_id);
CREATE INDEX shipments_from_warehouse_idx ON shipments (from_warehouse_id);
CREATE INDEX shipments_to_branch_idx ON shipments (to_branch_id);
CREATE INDEX shipments_status_idx ON shipments (tenant_id, status);

ALTER TABLE shipments ENABLE ROW LEVEL SECURITY;
ALTER TABLE shipments FORCE ROW LEVEL SECURITY;

CREATE POLICY shipments_read ON shipments FOR SELECT TO app_runtime
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid);

CREATE POLICY shipments_write ON shipments FOR ALL TO app_runtime
    USING  (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid);

-- ============================================================
-- shipment_items
-- ============================================================
CREATE TABLE shipment_items (
    shipment_id     UUID            NOT NULL REFERENCES shipments (id) ON DELETE CASCADE,
    stock_item_id   UUID            NOT NULL REFERENCES stock_items (id),
    tenant_id       UUID            NOT NULL,
    requested_qty   NUMERIC(18,3)   NOT NULL CHECK (requested_qty >= 0),
    shipped_qty     NUMERIC(18,3)   NOT NULL DEFAULT 0 CHECK (shipped_qty >= 0),
    received_qty    NUMERIC(18,3)   NOT NULL DEFAULT 0 CHECK (received_qty >= 0),
    unit            TEXT            NOT NULL,

    PRIMARY KEY (shipment_id, stock_item_id)
);

COMMENT ON TABLE shipment_items IS
    'Line items of a shipment. shipped_qty/received_qty are updated by the '
    'shipment status transitions (in_transit / received), never edited directly.';

CREATE INDEX shipment_items_tenant_idx ON shipment_items (tenant_id);
CREATE INDEX shipment_items_item_idx ON shipment_items (stock_item_id);

ALTER TABLE shipment_items ENABLE ROW LEVEL SECURITY;
ALTER TABLE shipment_items FORCE ROW LEVEL SECURITY;

CREATE POLICY shipment_items_read ON shipment_items FOR SELECT TO app_runtime
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid);

CREATE POLICY shipment_items_write ON shipment_items FOR ALL TO app_runtime
    USING  (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid);
