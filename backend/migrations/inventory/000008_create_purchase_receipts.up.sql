-- Migration: inventory/000008_create_purchase_receipts
-- Creates purchase_receipts + purchase_receipt_items (ADR-DATA-007 karar 3):
-- the "elden fiş" / faturasız alım belgesi (pazar, market) — the second of
-- the two purchase-side documents that establish branch-local cost
-- (migration 000007 already added stock_levels.last_cost_source with
-- 'purchase_receipt' as an accepted value; this migration is what actually
-- writes it).
--
-- Design decisions:
--   - id has no DB-side DEFAULT: generated client-side as a UUIDv7,
--     mirroring supply_policies' and stock_items' convention (see
--     service/purchase_receipt_service.go).
--   - warehouse_id is a real intra-module FK to warehouses(id) — the receipt
--     records where the goods physically landed; branch-scope authorization
--     (requireBranch) is derived from the warehouse's branch_id, exactly
--     like shipments' from_warehouse_id, not from a separate branch_id
--     column on this table.
--   - supplier_party_id is a bare nullable UUID (party.suppliers(id)): no FK
--     (cross-module FK forbidden, CLAUDE.md). NULL is a valid, expected
--     value — a pazar/market purchase has no registered supplier party at
--     all, not merely an unrecorded one. supplier_name carries the free-text
--     identification ("X Pazarı") for that case; both may be NULL for an
--     entirely anonymous cash purchase.
--   - receipt_no is a free-text, NULLable field: an elden fiş frequently has
--     no printed/assigned number at all (a market stall stub), unlike an
--     invoice.
--   - The document is immutable (DATA-002 immutability ruhu, applied here by
--     convention rather than trigger): there is deliberately no UPDATE
--     endpoint in service/purchase_receipt_service.go and no business need
--     for one — a correction is a new receipt, mirroring purchase document
--     conventions elsewhere in this schema (stock_movements, supply_policies).
--   - purchase_receipt_items carries its own tenant_id column (rather than
--     relying on a join through receipt_id for RLS), mirroring
--     shipment_items' convention (migration 000004) — FORCE RLS policies on
--     a child table are simplest and fastest when they can filter on a
--     column of their own rather than an EXISTS subquery against the parent.
--   - unit is the write-time canonical unit (ADR-DATA-005: quantities are
--     always converted to the stock item's canonical unit before being
--     persisted) — same convention as shipment_items.unit and
--     branch_transfer_order_items.unit.
--   - brand is nullable, line-level (not item-level): ADR-DATA-007 point 5 —
--     a canonical stock_item (e.g. "BBQ sos") may be sourced from different
--     brands per purchase; brand diversity is recorded on the purchase line,
--     never by opening a second stock_item per brand.
--
-- ADR references: ADR-DATA-007, ADR-DATA-002, ADR-DATA-005, ADR-SEC-001, ADR-SEC-002

CREATE TABLE purchase_receipts (
    id                 UUID            PRIMARY KEY,
    tenant_id          UUID            NOT NULL,
    warehouse_id       UUID            NOT NULL REFERENCES warehouses (id),
    supplier_party_id  UUID,                                 -- party.suppliers(id); no FK (cross-module). NULL = no registered supplier (pazar/market).
    supplier_name      TEXT,                                 -- free-text identification when there is no party record ("X Pazarı"); NULL for an anonymous purchase
    receipt_no         TEXT,                                 -- printed/assigned receipt number, if any; an elden fiş frequently has none
    receipt_date       DATE            NOT NULL,
    total              NUMERIC(18,4)   NOT NULL CHECK (total >= 0),
    currency           CHAR(3)         NOT NULL,
    note               TEXT,
    created_by         UUID,                                 -- identity.persons(id); no FK (cross-module)
    created_at         TIMESTAMPTZ     NOT NULL DEFAULT NOW()
);

COMMENT ON TABLE purchase_receipts IS
    'Elden fiş / faturasız alım belgesi (ADR-DATA-007 karar 3): a cash/no-invoice '
    'purchase document (market, pazar) distinct from the (future, Faz 2+) invoiced '
    'purchase_orders path. Immutable by convention -- no UPDATE endpoint; a '
    'correction is a new receipt.';
COMMENT ON COLUMN purchase_receipts.supplier_party_id IS
    'party.suppliers(id); no FK (cross-module, CLAUDE.md). NULL is a valid, '
    'expected value for a pazar/market purchase with no registered supplier party.';
COMMENT ON COLUMN purchase_receipts.supplier_name IS
    'Free-text supplier identification when there is no party record. NULL for '
    'an entirely anonymous cash purchase.';
COMMENT ON COLUMN purchase_receipts.receipt_date IS
    'The date printed/assigned on the physical receipt (may differ from '
    'created_at, the system entry time).';

CREATE INDEX purchase_receipts_tenant_idx ON purchase_receipts (tenant_id);
CREATE INDEX purchase_receipts_warehouse_idx ON purchase_receipts (warehouse_id);
CREATE INDEX purchase_receipts_supplier_idx ON purchase_receipts (supplier_party_id) WHERE supplier_party_id IS NOT NULL;

ALTER TABLE purchase_receipts ENABLE ROW LEVEL SECURITY;
ALTER TABLE purchase_receipts FORCE ROW LEVEL SECURITY;

CREATE POLICY purchase_receipts_read ON purchase_receipts FOR SELECT TO app_runtime
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid);

CREATE POLICY purchase_receipts_write ON purchase_receipts FOR ALL TO app_runtime
    USING  (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid);

-- ============================================================
-- purchase_receipt_items
-- ============================================================
CREATE TABLE purchase_receipt_items (
    id             UUID            PRIMARY KEY,
    tenant_id      UUID            NOT NULL,
    receipt_id     UUID            NOT NULL REFERENCES purchase_receipts (id) ON DELETE CASCADE,
    stock_item_id  UUID            NOT NULL REFERENCES stock_items (id),
    quantity       NUMERIC(18,3)   NOT NULL CHECK (quantity > 0),
    unit           TEXT            NOT NULL,
    unit_price     NUMERIC(18,4)   NOT NULL CHECK (unit_price >= 0),
    line_total     NUMERIC(18,4)   NOT NULL CHECK (line_total >= 0),
    brand          TEXT
);

COMMENT ON TABLE purchase_receipt_items IS
    'Line items of a purchase_receipt. unit_price is the branch-local cost '
    'source written to stock_levels.last_unit_cost (ADR-DATA-007, '
    'source=''purchase_receipt'') on receipt create.';
COMMENT ON COLUMN purchase_receipt_items.brand IS
    'Line-level brand of the purchased goods (ADR-DATA-007 point 5, BBQ sos '
    'scenario) -- never modeled as a second stock_item per brand.';

CREATE INDEX purchase_receipt_items_tenant_idx ON purchase_receipt_items (tenant_id);
CREATE INDEX purchase_receipt_items_receipt_idx ON purchase_receipt_items (receipt_id);
CREATE INDEX purchase_receipt_items_item_idx ON purchase_receipt_items (stock_item_id);

ALTER TABLE purchase_receipt_items ENABLE ROW LEVEL SECURITY;
ALTER TABLE purchase_receipt_items FORCE ROW LEVEL SECURITY;

CREATE POLICY purchase_receipt_items_read ON purchase_receipt_items FOR SELECT TO app_runtime
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid);

CREATE POLICY purchase_receipt_items_write ON purchase_receipt_items FOR ALL TO app_runtime
    USING  (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid);
