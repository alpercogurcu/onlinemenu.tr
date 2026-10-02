-- Migration: catalog/000005_product_cost
--
-- Unit cost in kurus, EXCLUDING VAT. NULL means "cost unknown" and must never
-- be read as 0: a report that treats unknown as free would overstate profit.
--
--   products.cost_amount                 -> tenant-wide cost
--   branch_product_overrides.cost_amount -> branch cost (e.g. franchise transfer price)
--
-- Resolution order at order time: branch override -> product -> NULL.
ALTER TABLE products
    ADD COLUMN cost_amount BIGINT CHECK (cost_amount IS NULL OR cost_amount >= 0);

ALTER TABLE branch_product_overrides
    ADD COLUMN cost_amount BIGINT CHECK (cost_amount IS NULL OR cost_amount >= 0);
