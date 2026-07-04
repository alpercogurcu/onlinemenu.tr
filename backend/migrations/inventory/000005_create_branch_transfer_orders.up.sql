-- Migration: inventory/000005_create_branch_transfer_orders
-- Creates branch_transfer_orders + branch_transfer_order_items (ADR-DATA-006
-- Faz 1) and links shipments to them.
--
-- Design decisions:
--   - requesting_branch_id / source_branch_id / approved_by / created_by are
--     bare UUIDs (tenant.branches / identity.persons): cross-module FK
--     forbidden (CLAUDE.md).
--   - status is the BTO's own single allowedTransitions state machine
--     (backend/internal/modules/inventory/domain/branch_transfer_order.go).
--     'shipped' and 'received' edges are triggered ONLY by shipment status
--     changes (ADR-DATA-006 ownership rule) — the service layer, not this
--     schema, enforces that no HTTP action can set those two values directly.
--   - shipped_qty / received_qty on branch_transfer_order_items are
--     denormalized FROM shipment_items (ADR-DATA-006: "SHIPMENTS'ten
--     denormalize edilir, sahibi shipment"). They are written only by the
--     service-layer code path that reacts to a shipment transition, never by
--     a direct BTO item update endpoint.
--   - shipments.transfer_order_id is nullable: a shipment may exist without a
--     BTO (e.g. ad-hoc restock), matching migration 000004's comment.
--
-- ADR references: ADR-DATA-006, ADR-SEC-001, ADR-SEC-002

CREATE TABLE branch_transfer_orders (
    id                       UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id                UUID        NOT NULL,
    requesting_branch_id     UUID        NOT NULL,       -- tenant.branches(id); no FK (cross-module)
    source_branch_id         UUID        NOT NULL,       -- tenant.branches(id); no FK (cross-module)
    status                   TEXT        NOT NULL DEFAULT 'draft'
                                 CHECK (status IN (
                                     'draft', 'submitted', 'approved', 'fulfilling',
                                     'shipped', 'received', 'closed', 'rejected', 'cancelled'
                                 )),
    priority                 TEXT        NOT NULL DEFAULT 'normal'
                                 CHECK (priority IN ('normal', 'urgent')),
    requested_delivery_date  DATE,
    note                     TEXT,
    created_by               UUID,                       -- identity.persons(id); no FK (cross-module)
    submitted_at             TIMESTAMPTZ,
    approved_at              TIMESTAMPTZ,
    approved_by              UUID,                        -- identity.persons(id); no FK (cross-module)
    created_at               TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

COMMENT ON TABLE branch_transfer_orders IS
    'Requesting-side document (ADR-DATA-006): a franchise/branch requests stock '
    'from a source depo/imalat branch. Physical movement is executed by SHIPMENTS. '
    'status transitions to shipped/received are driven only by shipment events, '
    'never set directly by a caller (enforced in service, not SQL).';

CREATE INDEX bto_tenant_idx ON branch_transfer_orders (tenant_id);
CREATE INDEX bto_requesting_branch_idx ON branch_transfer_orders (requesting_branch_id);
CREATE INDEX bto_source_branch_idx ON branch_transfer_orders (source_branch_id);
CREATE INDEX bto_status_idx ON branch_transfer_orders (tenant_id, status);

ALTER TABLE branch_transfer_orders ENABLE ROW LEVEL SECURITY;
ALTER TABLE branch_transfer_orders FORCE ROW LEVEL SECURITY;

CREATE POLICY bto_read ON branch_transfer_orders FOR SELECT TO app_runtime
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid);

CREATE POLICY bto_write ON branch_transfer_orders FOR ALL TO app_runtime
    USING  (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid);

-- ============================================================
-- branch_transfer_order_items
-- ============================================================
CREATE TABLE branch_transfer_order_items (
    id                UUID            PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id         UUID            NOT NULL,
    transfer_order_id UUID            NOT NULL REFERENCES branch_transfer_orders (id) ON DELETE CASCADE,
    stock_item_id     UUID            NOT NULL REFERENCES stock_items (id),
    requested_qty     NUMERIC(18,3)   NOT NULL CHECK (requested_qty > 0),
    approved_qty      NUMERIC(18,3)   CHECK (approved_qty IS NULL OR approved_qty >= 0),
    shipped_qty       NUMERIC(18,3)   NOT NULL DEFAULT 0 CHECK (shipped_qty >= 0),
    received_qty      NUMERIC(18,3)   NOT NULL DEFAULT 0 CHECK (received_qty >= 0),
    unit              TEXT            NOT NULL,
    note              TEXT,

    UNIQUE (transfer_order_id, stock_item_id)
);

COMMENT ON TABLE branch_transfer_order_items IS
    'BTO line items. shipped_qty/received_qty are denormalized from shipment_items '
    '(ADR-DATA-006): SHIPMENTS is the sole writer, via the service layer reacting '
    'to shipment status transitions.';

CREATE INDEX bto_items_tenant_idx ON branch_transfer_order_items (tenant_id);
CREATE INDEX bto_items_order_idx ON branch_transfer_order_items (transfer_order_id);
CREATE INDEX bto_items_stock_item_idx ON branch_transfer_order_items (stock_item_id);

ALTER TABLE branch_transfer_order_items ENABLE ROW LEVEL SECURITY;
ALTER TABLE branch_transfer_order_items FORCE ROW LEVEL SECURITY;

CREATE POLICY bto_items_read ON branch_transfer_order_items FOR SELECT TO app_runtime
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid);

CREATE POLICY bto_items_write ON branch_transfer_order_items FOR ALL TO app_runtime
    USING  (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid);

-- ============================================================
-- shipments.transfer_order_id (ADR-DATA-006 link)
-- ============================================================
ALTER TABLE shipments ADD COLUMN transfer_order_id UUID REFERENCES branch_transfer_orders (id);
CREATE INDEX shipments_transfer_order_idx ON shipments (transfer_order_id) WHERE transfer_order_id IS NOT NULL;

COMMENT ON COLUMN shipments.transfer_order_id IS
    'Optional link to the requesting BTO (ADR-DATA-006). NULL = ad-hoc shipment '
    'with no prior transfer request. A single BTO may produce multiple shipments '
    '(partial fulfilment).';
