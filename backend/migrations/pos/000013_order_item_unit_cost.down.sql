-- Revert pos/000013_order_item_unit_cost. Dropping the column loses the cost
-- snapshot of already placed lines; sales amounts are unaffected.
ALTER TABLE order_items DROP COLUMN unit_cost_amount;
