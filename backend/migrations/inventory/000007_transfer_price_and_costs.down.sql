-- Migration: inventory/000007_transfer_price_and_costs (rollback)
ALTER TABLE stock_levels
    DROP COLUMN IF EXISTS last_unit_cost,
    DROP COLUMN IF EXISTS last_cost_currency,
    DROP COLUMN IF EXISTS last_cost_source,
    DROP COLUMN IF EXISTS last_cost_at;

ALTER TABLE shipment_items
    DROP COLUMN IF EXISTS unit_price,
    DROP COLUMN IF EXISTS currency;

ALTER TABLE branch_transfer_order_items
    DROP COLUMN IF EXISTS unit_price,
    DROP COLUMN IF EXISTS currency;
