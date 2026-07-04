-- Migration: inventory/000003_rekey_levels_movements_to_warehouse (rollback)

-- ============================================================
-- stock_movements -> inventory_transactions
-- ============================================================
DROP POLICY IF EXISTS stock_movements_read ON stock_movements;
DROP POLICY IF EXISTS stock_movements_insert ON stock_movements;
DROP INDEX IF EXISTS stock_movements_warehouse_item_idx;
DROP INDEX IF EXISTS stock_movements_reference_idx;

-- FK constraints must be dropped explicitly: stock_item_id is renamed (not
-- dropped) below, so DROP COLUMN never fires for it and the constraint would
-- otherwise survive the rename and block 000002's down (stock_items/
-- warehouses cannot be dropped while still referenced).
ALTER TABLE stock_movements DROP CONSTRAINT IF EXISTS stock_movements_stock_item_fk;
ALTER TABLE stock_movements DROP CONSTRAINT IF EXISTS stock_movements_warehouse_fk;

ALTER TABLE stock_movements DROP CONSTRAINT IF EXISTS stock_movements_quantity_check;
ALTER TABLE stock_movements DROP CONSTRAINT IF EXISTS stock_movements_movement_type_check;
ALTER TABLE stock_movements RENAME COLUMN quantity TO quantity_delta;
ALTER TABLE stock_movements ADD CONSTRAINT inventory_transactions_quantity_delta_check CHECK (quantity_delta <> 0);
ALTER TABLE stock_movements RENAME COLUMN movement_type TO type;
ALTER TABLE stock_movements ADD CONSTRAINT inventory_transactions_type_check
    CHECK (type IN ('restock', 'consumption', 'waste', 'adjustment'));

ALTER TABLE stock_movements ADD COLUMN branch_id UUID NOT NULL DEFAULT '00000000-0000-0000-0000-000000000000'::uuid;
ALTER TABLE stock_movements ALTER COLUMN branch_id DROP DEFAULT;
ALTER TABLE stock_movements DROP COLUMN warehouse_id;
ALTER TABLE stock_movements RENAME COLUMN stock_item_id TO product_id;

ALTER TABLE stock_movements RENAME TO inventory_transactions;

CREATE INDEX inventory_tx_branch_product_idx ON inventory_transactions (tenant_id, branch_id, product_id, created_at DESC);
CREATE INDEX inventory_tx_reference_idx ON inventory_transactions (reference_id) WHERE reference_id IS NOT NULL;

CREATE POLICY inventory_tx_read ON inventory_transactions FOR SELECT TO app_runtime
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid);

CREATE POLICY inventory_tx_insert ON inventory_transactions FOR INSERT TO app_runtime
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid);

-- ============================================================
-- stock_levels -> inventory_levels
-- ============================================================
DROP POLICY IF EXISTS stock_levels_read ON stock_levels;
DROP POLICY IF EXISTS stock_levels_write ON stock_levels;
DROP INDEX IF EXISTS stock_levels_warehouse_idx;
DROP INDEX IF EXISTS stock_levels_item_idx;

-- See the stock_movements FK note above: stock_item_id is renamed (not
-- dropped) below, so the FK must be dropped explicitly here.
ALTER TABLE stock_levels DROP CONSTRAINT IF EXISTS stock_levels_stock_item_fk;
ALTER TABLE stock_levels DROP CONSTRAINT IF EXISTS stock_levels_warehouse_fk;

ALTER TABLE stock_levels DROP CONSTRAINT IF EXISTS stock_levels_tenant_warehouse_item_key;

ALTER TABLE stock_levels DROP COLUMN unit;
ALTER TABLE stock_levels DROP COLUMN reorder_point;
ALTER TABLE stock_levels DROP COLUMN available;
ALTER TABLE stock_levels DROP COLUMN reserved;
ALTER TABLE stock_levels ADD COLUMN branch_id UUID NOT NULL DEFAULT '00000000-0000-0000-0000-000000000000'::uuid;
ALTER TABLE stock_levels ALTER COLUMN branch_id DROP DEFAULT;
ALTER TABLE stock_levels DROP COLUMN warehouse_id;
ALTER TABLE stock_levels RENAME COLUMN on_hand TO quantity;
ALTER TABLE stock_levels RENAME COLUMN stock_item_id TO product_id;

ALTER TABLE stock_levels RENAME TO inventory_levels;

ALTER TABLE inventory_levels ADD CONSTRAINT inventory_levels_tenant_id_branch_id_product_id_key
    UNIQUE (tenant_id, branch_id, product_id);

CREATE INDEX inventory_levels_branch_idx ON inventory_levels (tenant_id, branch_id);
CREATE INDEX inventory_levels_product_idx ON inventory_levels (tenant_id, product_id);

CREATE POLICY inventory_levels_read ON inventory_levels FOR SELECT TO app_runtime
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid);

CREATE POLICY inventory_levels_write ON inventory_levels FOR ALL TO app_runtime
    USING  (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid);
