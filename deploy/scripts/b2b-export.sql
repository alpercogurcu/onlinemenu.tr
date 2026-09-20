-- b2b → Online Menu aktarımı: KAYNAK tarafı dışa aktarımı (YALNIZ SELECT).
--
-- b2b prod DB'sine hiçbir şey yazmaz; read-only oturumla koşulur. Çıktı tek satırlık JSON'dur
-- (deploy/scripts/import-from-b2b.sql bunu okur). Kişisel veri (telefon, e-posta, kullanıcı)
-- ve maliyet fiyatı (cost_price_tl) BİLEREK dışarıda bırakılır.
--
--   ssh diverserver "docker exec -i -e PGOPTIONS='-c default_transaction_read_only=on' \
--     b2b_postgres psql -U b2b_prod -d b2b_production -X -q -A -t -f -" \
--     < deploy/scripts/b2b-export.sql > b2b-export.json
--
-- Çıktı dosyası repoya girmez (.gitignore dışı bir yerde tutun).

SELECT jsonb_build_object(
    'exported_at', now(),
    'branches', (
        SELECT COALESCE(jsonb_agg(jsonb_build_object(
                   'b2b_id',    b.id,
                   'code',      b.code,
                   'name',      b.name,
                   'type',      b.type,
                   'address',   b.address,
                   'is_active', b.is_active
               ) ORDER BY b.code), '[]'::jsonb)
        FROM branches b
        WHERE b.deleted_at IS NULL
    ),
    'products', (
        SELECT COALESCE(jsonb_agg(jsonb_build_object(
                   'b2b_id',        p.id,
                   'sku',           NULLIF(btrim(p.sku), ''),
                   'name',          btrim(p.name),
                   'sale_unit',     p.sale_unit,
                   'sale_price_tl', p.sale_price_tl,
                   'tax_rate',      p.tax_rate,
                   'is_active',     p.is_active
               ) ORDER BY p.name), '[]'::jsonb)
        FROM products p
        WHERE p.product_type = 'pos_sale' AND p.deleted_at IS NULL
    ),
    'branch_prices', (
        SELECT COALESCE(jsonb_agg(jsonb_build_object(
                   'b2b_branch_id',  x.branch_id,
                   'b2b_product_id', x.product_id,
                   'sale_price_tl',  x.sale_price_tl
               ) ORDER BY x.branch_id, x.product_id), '[]'::jsonb)
        FROM branch_product_prices x
        JOIN products p ON p.id = x.product_id
                       AND p.product_type = 'pos_sale' AND p.deleted_at IS NULL
        JOIN branches b ON b.id = x.branch_id AND b.deleted_at IS NULL
        WHERE x.deleted_at IS NULL
    )
);
