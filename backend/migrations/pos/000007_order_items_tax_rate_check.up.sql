-- M5 (final review, pilot-mvp): order_items.tax_rate_bps had no CHECK, unlike
-- its source column catalog.products.tax_rate_bps (catalog/000001:62) and
-- tenant.tenants.tax_rate_bps (tenant/000001:86). domain.NewTaxLine divides
-- by (10000 + rateBPS), so rateBPS = -10000 is a division-by-zero panic —
-- unreachable today only because the value is copied from a CHECK-constrained
-- column; this constraint makes that a guarantee of the schema, not just of
-- the current code paths.
ALTER TABLE order_items ADD CONSTRAINT order_items_tax_rate_bps_check
    CHECK (tax_rate_bps >= 0 AND tax_rate_bps <= 10000);
