-- order_items.unit_cost_amount: snapshot of the line's unit cost (kurus,
-- EXCLUDING VAT) taken when the order is placed, resolved as branch override
-- -> product cost -> NULL. A snapshot because later cost edits must not
-- rewrite past profit, and a day missed is never recoverable.
--
-- NULL means "cost was unknown at order time" and is never coerced to 0;
-- every pre-existing row stays NULL. No cross-module FK or catalog read is
-- involved: the value arrives with the priced line.
ALTER TABLE order_items
    ADD COLUMN unit_cost_amount BIGINT
        CONSTRAINT order_items_unit_cost_chk
        CHECK (unit_cost_amount IS NULL OR unit_cost_amount >= 0);
