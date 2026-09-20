-- Migration: catalog/000003_branch_product_overrides (ADR-DATA-009)
--
-- Sube bazli satis fiyati ve satilabilirlik override'i. Tenant geneli katalog
-- (products) tek satir kalir; sube farki yalnizca burada yasar.
--
--   satir yok                          -> tenant varsayilani
--   price_amount IS NULL               -> tenant fiyati, sube yalnizca acik/kapali
--   is_available = FALSE               -> urun o subede satilmaz
--
-- Fiyat onceligi (ADR-DATA-009 karar 2):
--   branch_product_overrides.price_amount > menu_items.price_override > products.price_amount

CREATE TABLE branch_product_overrides (
    tenant_id    UUID        NOT NULL,
    branch_id    UUID        NOT NULL,
    product_id   UUID        NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    is_available BOOLEAN     NOT NULL DEFAULT TRUE,
    price_amount BIGINT      CHECK (price_amount IS NULL OR price_amount >= 0),  -- kurus; NULL = tenant fiyati
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, branch_id, product_id)
);

-- Sube listeleme sorgusu PK prefiksini (tenant_id, branch_id) kullanir; ayri
-- indeks gerekmez. product_id uzerindeki indeks FK'nin ON DELETE CASCADE
-- taramasi icindir (PK product_id ile baslamiyor).
CREATE INDEX branch_product_overrides_product_idx ON branch_product_overrides (product_id);

ALTER TABLE branch_product_overrides ENABLE ROW LEVEL SECURITY;
ALTER TABLE branch_product_overrides FORCE ROW LEVEL SECURITY;

CREATE POLICY branch_product_overrides_read ON branch_product_overrides FOR SELECT TO app_runtime
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid);

CREATE POLICY branch_product_overrides_write ON branch_product_overrides FOR ALL TO app_runtime
    USING      (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid);

GRANT SELECT, INSERT, UPDATE, DELETE ON branch_product_overrides TO app_runtime;
