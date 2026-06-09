-- Migration: inventory/000001_create_inventory
-- Creates the core inventory tables: stock levels (current state) and
-- transactions (immutable ledger).
--
-- Design decisions:
--   - inventory_levels holds the materialized current quantity per branch+product
--   - inventory_transactions is the immutable ledger (DATA-002: events immutable)
--   - branch_id is NOT NULL: all stock is branch-scoped; tenant-wide stock lives
--     in catalog.products.stock_quantity (a separate concern)
--   - quantity uses NUMERIC(12,3): supports fractional units (kg, litre)
--   - quantity_delta in transactions: positive = stock in, negative = stock out
--   - No FK to catalog.products: cross-module FKs are forbidden (CLAUDE.md)
--   - FORCE RLS on both tables (ADR-SEC-001, ADR-SEC-002)
--
-- ADR references: ADR-SEC-001, ADR-SEC-002, DATA-002

-- ============================================================
-- inventory_levels
-- ============================================================
CREATE TABLE inventory_levels (
    id          UUID            PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   UUID            NOT NULL,
    branch_id   UUID            NOT NULL,
    product_id  UUID            NOT NULL,
    quantity    NUMERIC(12,3)   NOT NULL DEFAULT 0 CHECK (quantity >= 0),
    updated_at  TIMESTAMPTZ     NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, branch_id, product_id)
);

COMMENT ON TABLE inventory_levels IS
    'Materialized current stock per product per branch. Updated atomically with inventory_transactions.';
COMMENT ON COLUMN inventory_levels.quantity IS
    'Current stock quantity. Supports fractional units (kg, litre). Zero means out of stock.';

CREATE INDEX inventory_levels_branch_idx ON inventory_levels (tenant_id, branch_id);
CREATE INDEX inventory_levels_product_idx ON inventory_levels (tenant_id, product_id);

ALTER TABLE inventory_levels ENABLE ROW LEVEL SECURITY;
ALTER TABLE inventory_levels FORCE ROW LEVEL SECURITY;

CREATE POLICY inventory_levels_read ON inventory_levels FOR SELECT TO app_runtime
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid);

CREATE POLICY inventory_levels_write ON inventory_levels FOR ALL TO app_runtime
    USING  (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid);

-- ============================================================
-- inventory_transactions
-- ============================================================
CREATE TABLE inventory_transactions (
    id              UUID            PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID            NOT NULL,
    branch_id       UUID            NOT NULL,
    product_id      UUID            NOT NULL,
    type            TEXT            NOT NULL CHECK (type IN ('restock','consumption','waste','adjustment')),
    quantity_delta  NUMERIC(12,3)   NOT NULL CHECK (quantity_delta <> 0),
    reference_id    UUID,
    reference_type  TEXT,
    notes           TEXT,
    created_by      UUID,
    created_at      TIMESTAMPTZ     NOT NULL DEFAULT NOW()
);

COMMENT ON TABLE inventory_transactions IS
    'Immutable ledger of all stock movements. Never updated or deleted (DATA-002).';
COMMENT ON COLUMN inventory_transactions.type IS
    'restock=goods received, consumption=sold/used, waste=spoiled, adjustment=manual correction';
COMMENT ON COLUMN inventory_transactions.quantity_delta IS
    'Signed delta: positive for stock in, negative for stock out.';
COMMENT ON COLUMN inventory_transactions.reference_id IS
    'Optional foreign key to an external entity (e.g. order_id, purchase_order_id).';

CREATE INDEX inventory_tx_branch_product_idx ON inventory_transactions (tenant_id, branch_id, product_id, created_at DESC);
CREATE INDEX inventory_tx_reference_idx ON inventory_transactions (reference_id) WHERE reference_id IS NOT NULL;

ALTER TABLE inventory_transactions ENABLE ROW LEVEL SECURITY;
ALTER TABLE inventory_transactions FORCE ROW LEVEL SECURITY;

CREATE POLICY inventory_tx_read ON inventory_transactions FOR SELECT TO app_runtime
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid);

CREATE POLICY inventory_tx_insert ON inventory_transactions FOR INSERT TO app_runtime
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid);

-- No UPDATE or DELETE policy: transactions are immutable (DATA-002).
