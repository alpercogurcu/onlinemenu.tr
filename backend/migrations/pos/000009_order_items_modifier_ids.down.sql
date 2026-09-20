-- Revert pos/000009_order_items_modifier_ids.
--
-- The selection survives in order_items.note as text, so rolling back loses
-- the machine-readable form only.
ALTER TABLE order_items DROP COLUMN modifier_ids;
