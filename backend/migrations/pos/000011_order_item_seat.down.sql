-- Revert pos/000011_order_item_seat. Dropping the column loses which seat
-- each line was ordered for; the lines themselves (and checks.pax, which
-- OrderService.Place may have raised from these values) survive, so bills
-- and reports keep rendering — only the per-person split basis is gone.
ALTER TABLE order_items DROP COLUMN seat_no;
