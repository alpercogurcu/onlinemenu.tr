-- Migration: inventory/000003_rekey_levels_movements_to_warehouse
--
-- Re-keys the Faz 0 skeleton (branch+product scoped inventory_levels /
-- inventory_transactions) onto the ADR-DATA-005 canonical model: warehouse +
-- stock_item scoped stock_levels / stock_movements.
--
-- Why ALTER, not DROP+CREATE: these two tables are a Faz 0 skeleton with no
-- production data (confirmed with team-lead before writing this migration).
-- ALTER preserves the tables' identity (same physical relation, same OID) so
-- a rollback is a true rollback rather than a second data-loss event, and it
-- keeps the migration diff readable as "what changed" rather than "delete
-- everything, recreate everything". If this repo ever carried real rows here,
-- DROP+CREATE would be wrong regardless (silent data loss); ALTER is correct
-- in both cases, so it is the only defensible choice.
--
-- Design decisions:
--   - inventory_levels -> stock_levels: branch_id column removed entirely
--     (stock is warehouse-scoped, never branch-scoped — ADR-DATA-005 "Mevcut
--     stok temsili ile uzlaştırma"), warehouse_id added, product_id renamed
--     to stock_item_id, quantity renamed to on_hand. `reserved` is added as a
--     live column (reserve/release movement types write to it in Faz 2+
--     order-reservation flows). `available` is a STORED GENERATED column
--     (on_hand - reserved), never written directly by application code — this
--     is the b2b lesson applied at the schema level: a derived quantity must
--     never be a second column an app maintains by hand (that is exactly how
--     b2b's atomic_per_sale_unit math drifted in three different places).
--   - inventory_transactions -> stock_movements: product_id renamed to
--     stock_item_id, branch_id replaced by warehouse_id, the old `type` enum
--     (restock|consumption|waste|adjustment) replaced by `movement_type`
--     (in|out|adjust|transfer|reserve|release) per db-schema.md STOCK_MOVEMENTS.
--     quantity_delta renamed to quantity. Sign convention (documented on the
--     column): quantity is a positive magnitude for in/out/transfer/reserve/
--     release (direction comes from movement_type, standard ledger design —
--     this is NOT the b2b "column meaning changes per discriminator" anti-
--     pattern, because every movement_type still means the exact same thing
--     for the quantity column: "how much moved"); movement_type='adjust' is
--     the sole exception and may be signed, because a manual correction can
--     go either direction and has no separate "adjust-in"/"adjust-out" type.
--   - Old CHECK constraints/enums (`type IN (...)`) are dropped and replaced;
--     old indexes/policies are dropped and recreated with names that match
--     the new table names for clarity going forward.
--
-- ADR references: ADR-DATA-005, ADR-DATA-006, ADR-SEC-001, ADR-SEC-002

-- ============================================================
-- stock_levels (was inventory_levels)
-- ============================================================
ALTER TABLE inventory_levels RENAME TO stock_levels;

DROP POLICY IF EXISTS inventory_levels_read ON stock_levels;
DROP POLICY IF EXISTS inventory_levels_write ON stock_levels;
DROP INDEX IF EXISTS inventory_levels_branch_idx;
DROP INDEX IF EXISTS inventory_levels_product_idx;
ALTER TABLE stock_levels DROP CONSTRAINT IF EXISTS inventory_levels_tenant_id_branch_id_product_id_key;

ALTER TABLE stock_levels RENAME COLUMN product_id TO stock_item_id;
ALTER TABLE stock_levels RENAME COLUMN quantity TO on_hand;
-- No backfill UPDATE: the table is empty (Faz 0 skeleton, no production data —
-- confirmed with team-lead), so ADD COLUMN ... NOT NULL can be applied directly.
ALTER TABLE stock_levels ADD COLUMN warehouse_id UUID NOT NULL;
ALTER TABLE stock_levels DROP COLUMN branch_id;
ALTER TABLE stock_levels ADD COLUMN reserved NUMERIC(18,3) NOT NULL DEFAULT 0 CHECK (reserved >= 0);
ALTER TABLE stock_levels ADD COLUMN available NUMERIC(18,3) GENERATED ALWAYS AS (on_hand - reserved) STORED;
ALTER TABLE stock_levels ADD COLUMN reorder_point NUMERIC(18,3);
ALTER TABLE stock_levels ADD COLUMN unit TEXT NOT NULL DEFAULT '';

ALTER TABLE stock_levels ADD CONSTRAINT stock_levels_tenant_warehouse_item_key
    UNIQUE (tenant_id, warehouse_id, stock_item_id);

-- Real FKs within the inventory module (not cross-module): warehouses and
-- stock_items are owned by this same module, so referential integrity here
-- is a plain intra-module invariant, not the forbidden cross-module FK.
ALTER TABLE stock_levels ADD CONSTRAINT stock_levels_warehouse_fk
    FOREIGN KEY (warehouse_id) REFERENCES warehouses (id);
ALTER TABLE stock_levels ADD CONSTRAINT stock_levels_stock_item_fk
    FOREIGN KEY (stock_item_id) REFERENCES stock_items (id);

COMMENT ON TABLE stock_levels IS
    'Materialized current stock per stock item per warehouse (ADR-DATA-005). '
    'available is a STORED GENERATED column (on_hand - reserved) — never written '
    'directly, so it can never drift from its inputs (b2b lesson).';
COMMENT ON COLUMN stock_levels.on_hand IS
    'Physical quantity in the warehouse. Updated atomically with stock_movements.';
COMMENT ON COLUMN stock_levels.reserved IS
    'Quantity held against a pending commitment (order/transfer) but not yet moved out.';
COMMENT ON COLUMN stock_levels.available IS
    'Generated: on_hand - reserved. Never set directly by application code.';

CREATE INDEX stock_levels_warehouse_idx ON stock_levels (tenant_id, warehouse_id);
CREATE INDEX stock_levels_item_idx ON stock_levels (tenant_id, stock_item_id);

ALTER TABLE stock_levels ENABLE ROW LEVEL SECURITY;
ALTER TABLE stock_levels FORCE ROW LEVEL SECURITY;

CREATE POLICY stock_levels_read ON stock_levels FOR SELECT TO app_runtime
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid);

CREATE POLICY stock_levels_write ON stock_levels FOR ALL TO app_runtime
    USING  (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid);

-- ============================================================
-- stock_movements (was inventory_transactions)
-- ============================================================
ALTER TABLE inventory_transactions RENAME TO stock_movements;

DROP POLICY IF EXISTS inventory_tx_read ON stock_movements;
DROP POLICY IF EXISTS inventory_tx_insert ON stock_movements;
DROP INDEX IF EXISTS inventory_tx_branch_product_idx;
DROP INDEX IF EXISTS inventory_tx_reference_idx;
ALTER TABLE stock_movements DROP CONSTRAINT IF EXISTS inventory_transactions_type_check;
ALTER TABLE stock_movements DROP CONSTRAINT IF EXISTS inventory_transactions_quantity_delta_check;

ALTER TABLE stock_movements RENAME COLUMN product_id TO stock_item_id;
-- No backfill UPDATE: the table is empty (Faz 0 skeleton, no production data).
ALTER TABLE stock_movements ADD COLUMN warehouse_id UUID NOT NULL;
ALTER TABLE stock_movements DROP COLUMN branch_id;

ALTER TABLE stock_movements RENAME COLUMN type TO movement_type;
ALTER TABLE stock_movements ADD CONSTRAINT stock_movements_movement_type_check
    CHECK (movement_type IN ('in', 'out', 'adjust', 'transfer', 'reserve', 'release'));

ALTER TABLE stock_movements RENAME COLUMN quantity_delta TO quantity;
ALTER TABLE stock_movements ADD CONSTRAINT stock_movements_quantity_check
    CHECK (
        (movement_type = 'adjust' AND quantity <> 0)
        OR (movement_type <> 'adjust' AND quantity > 0)
    );

COMMENT ON TABLE stock_movements IS
    'Immutable ledger of all stock movements, warehouse+stock_item scoped (ADR-DATA-005). '
    'Never updated or deleted (DATA-002).';
COMMENT ON COLUMN stock_movements.movement_type IS
    'in|out|adjust|transfer|reserve|release. in/out change on_hand directly; '
    'reserve/release change reserved only; adjust is a signed manual correction '
    'to on_hand; transfer is reserved for direct warehouse-to-warehouse moves '
    'outside the shipment/BTO flow (not used by Faz 1 code paths).';
COMMENT ON COLUMN stock_movements.quantity IS
    'Positive magnitude for in/out/transfer/reserve/release (direction from '
    'movement_type). May be signed only for movement_type=adjust.';

CREATE INDEX stock_movements_warehouse_item_idx ON stock_movements (tenant_id, warehouse_id, stock_item_id, created_at DESC);
CREATE INDEX stock_movements_reference_idx ON stock_movements (reference_id) WHERE reference_id IS NOT NULL;

ALTER TABLE stock_movements ADD CONSTRAINT stock_movements_warehouse_fk
    FOREIGN KEY (warehouse_id) REFERENCES warehouses (id);
ALTER TABLE stock_movements ADD CONSTRAINT stock_movements_stock_item_fk
    FOREIGN KEY (stock_item_id) REFERENCES stock_items (id);

ALTER TABLE stock_movements ENABLE ROW LEVEL SECURITY;
ALTER TABLE stock_movements FORCE ROW LEVEL SECURITY;

CREATE POLICY stock_movements_read ON stock_movements FOR SELECT TO app_runtime
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid);

CREATE POLICY stock_movements_insert ON stock_movements FOR INSERT TO app_runtime
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid);

-- No UPDATE or DELETE policy: movements are immutable (DATA-002).
