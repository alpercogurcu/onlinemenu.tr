-- b2b → Online Menu import (DRAFT — reviewed dry run only, never run against prod without approval).
--
-- Moves the Diver Street Food branch chain, the pos_sale menu and the per-branch sale prices from
-- the sister b2b system into ONE Online Menu tenant. Design: docs/b2b-import-plan.md.
--
-- Input : JSON produced by deploy/scripts/b2b-export.sql (no PII, no cost prices).
-- Run as: app_migrator (BYPASSRLS — no `SET LOCAL app.tenant_id` needed), psql >= 12.
--
--   B2B_JSON=b2b-export.json psql "$DSN" -v ON_ERROR_STOP=1 \
--     -v tenant_slug=diverstreetfood \
--     -v existing_branch_code=KRK        `# which b2b branch the placeholder "Ana Şube" becomes, or NONE` \
--     -v include_manufacturing=0         `# 1 = also import the İMALAT branch (operation_type=imalat)` \
--     -v dry_run=1                       `# 1 (default) = ROLLBACK at the end, 0 = COMMIT` \
--     -v rollback=0                      `# 1 = undo mode: delete only rows this import created` \
--     -v deactivate_demo=0               `# 1 = set the 3 seed demo products (Adana Kebap/Ayran/Lahmacun) inactive` \
--     -f deploy/scripts/import-from-b2b.sql
--   (or pass the document directly: -v payload="$(cat b2b-export.json)")
--
-- Idempotency (only ON CONFLICT targets that really exist are used):
--   branches         ON CONFLICT (id); ids are resolved by slug first, so a re-run never duplicates.
--                    The placeholder is adopted only while its slug is still NULL.
--   branch_settings  ON CONFLICT (branch_id) DO NOTHING (UNIQUE (branch_id)).
--   categories       NOT EXISTS on (tenant_id, name) + ON CONFLICT (id) — there is no unique key on name.
--   products         ON CONFLICT (id); id = uuid_v5(tenant_id, 'b2b:product:'||b2b_id), so every imported
--                    row is traceable and a re-run only refreshes price/tax/active/sku/unit.
--   overrides        ON CONFLICT (tenant_id, branch_id, product_id) (catalog/000003 PK). A re-run refreshes
--                    price_amount only; is_available is never overwritten (owner may close a product later).
-- Branch rules (owner decision 2026-09-20): ADA/IZM/KRK → ownership_type=franchise; SRD → sube; the
-- manufacturing branch is renamed 'İmalat Merkezi (Serdivan)' (operation_type=imalat, ownership sube).
-- Undo (-v rollback=1): deletes exactly the rows whose ids are the deterministic uuid_v5 ids
-- (overrides, products, new categories, new branches + their settings). The adopted placeholder
-- branch is NOT renamed back. Only valid before the first real sale — once orders reference the
-- products/branches the FK errors abort the undo (fail-closed); deactivate instead.
-- NOTE: rows are written directly, so no catalog.branch_override.changed.v1 outbox event is emitted
-- (ADR-DATA-009 §7). Effective prices are resolved at read time (CTE), so POS/QR see them at once;
-- any edge cache must be refreshed manually until DATA-004 catalog_version exists.
-- Fail-closed guards: unknown tenant, missing override table, same-name product not created by this
-- import, unknown b2b unit, placeholder that cannot be resolved — all ABORT instead of guessing.

\set ON_ERROR_STOP on

\if :{?tenant_slug}
\else
  \echo 'HATA: -v tenant_slug=<slug> gerekli'
  \quit
\endif
\if :{?existing_branch_code}
\else
  \echo 'HATA: -v existing_branch_code=<b2b şube kodu | NONE> gerekli ("Ana Şube" hangi şubeye dönüşecek?)'
  \quit
\endif
\if :{?include_manufacturing}
\else
  \set include_manufacturing 0
\endif
\if :{?dry_run}
\else
  \set dry_run 1
\endif
\if :{?rollback}
\else
  \set rollback 0
\endif
\if :{?deactivate_demo}
\else
  \set deactivate_demo 0
\endif
\if :{?payload}
\else
  \set payload `cat "$B2B_JSON"`
\endif

BEGIN;

CREATE TEMP TABLE imp_in ON COMMIT DROP AS
SELECT t.id                          AS tenant_id,
       :'existing_branch_code'::text AS existing_code,
       (:'include_manufacturing')::int AS incl_mfg,
       (:'deactivate_demo')::int AS deact_demo,
       :'payload'::jsonb             AS doc
FROM tenants t
WHERE t.slug = :'tenant_slug';

DO $$
BEGIN
    IF (SELECT count(*) FROM imp_in) <> 1 THEN
        RAISE EXCEPTION 'import: tenant slug bulunamadı';
    END IF;
    IF to_regclass('public.branch_product_overrides') IS NULL THEN
        RAISE EXCEPTION 'import: branch_product_overrides yok — DATA-009 migration önce koşulmalı';
    END IF;
    IF jsonb_array_length((SELECT doc->'products' FROM imp_in)) = 0 THEN
        RAISE EXCEPTION 'import: payload içinde ürün yok (b2b-export.json boş/bozuk?)';
    END IF;
    IF EXISTS (
        SELECT 1 FROM imp_in, jsonb_array_elements(doc->'products') p
        WHERE p->>'sale_unit' NOT IN ('piece', 'kg', 'l')
           OR (p->>'tax_rate')::int NOT BETWEEN 0 AND 100
    ) THEN
        RAISE EXCEPTION 'import: bilinmeyen birim veya KDV oranı — eşleme tablosunu genişletin';
    END IF;
END
$$;

-- ---------------------------------------------------------------------------------------------
-- Branches (b2b sales → fast_food, b2b manufacturing → imalat; ownership per the branch rules above)
-- ---------------------------------------------------------------------------------------------
CREATE TEMP TABLE imp_branch ON COMMIT DROP AS
WITH src AS (
    SELECT (b->>'b2b_id')::uuid AS b2b_id, b->>'code' AS code, btrim(b->>'name') AS name,
           b->>'type' AS type, NULLIF(btrim(b->>'address'), '') AS address,
           (b->>'is_active')::boolean AS is_active
    FROM imp_in, jsonb_array_elements(doc->'branches') b
), sel AS (
    SELECT s.b2b_id, s.code, s.type, s.address, s.is_active,
           CASE WHEN s.code = 'IMALAT' THEN 'İmalat Merkezi (Serdivan)' ELSE s.name END AS name,
           btrim(regexp_replace(lower(translate(CASE WHEN s.code = 'IMALAT' THEN 'İmalat Merkezi (Serdivan)' ELSE s.name END, 'İIıĞğÜüŞşÖöÇç', 'iiigguussoocc')),
                                '[^a-z0-9]+', '-', 'g'), '-') AS slug,
           CASE s.type WHEN 'manufacturing' THEN 'imalat' ELSE 'fast_food' END AS operation_type,
           CASE WHEN s.code IN ('ADA', 'IZM', 'KRK') THEN 'franchise' ELSE 'sube' END AS ownership_type
    FROM src s
    WHERE s.type = 'sales' OR (s.type = 'manufacturing' AND (SELECT incl_mfg FROM imp_in) = 1)
)
SELECT sel.*,
       COALESCE(
           (SELECT br.id FROM branches br, imp_in i
             WHERE br.tenant_id = i.tenant_id AND br.slug = sel.slug),
           (SELECT br.id FROM branches br, imp_in i
             WHERE i.existing_code = sel.code AND br.tenant_id = i.tenant_id AND br.slug IS NULL
               AND (SELECT count(*) FROM branches x
                     WHERE x.tenant_id = i.tenant_id AND x.slug IS NULL) = 1),
           uuid_generate_v5((SELECT tenant_id FROM imp_in), 'b2b:branch:' || sel.b2b_id)
       ) AS om_id
FROM sel;

DO $$
BEGIN
    IF (SELECT existing_code FROM imp_in) <> 'NONE' THEN
        IF NOT EXISTS (SELECT 1 FROM imp_branch b, imp_in i WHERE b.code = i.existing_code) THEN
            RAISE EXCEPTION 'import: existing_branch_code b2b şubeleri arasında yok / kapsam dışı';
        END IF;
        IF NOT EXISTS (SELECT 1 FROM imp_branch b, imp_in i, branches br
                        WHERE b.code = i.existing_code AND br.id = b.om_id) THEN
            RAISE EXCEPTION 'import: placeholder şube çözülemedi (slug''ı boş tam 1 şube olmalı)';
        END IF;
    END IF;
END
$$;

-- ---------------------------------------------------------------------------------------------
-- Categories + products (prices: b2b KDV-dahil TL → kuruş; tax_rate % → bps)
-- b2b category is meaningless for pos_sale (13 'meat' / 5 'other'), so it is derived from the name.
-- ---------------------------------------------------------------------------------------------
CREATE TEMP TABLE imp_product ON COMMIT DROP AS
SELECT (p->>'b2b_id')::uuid AS b2b_id,
       p->>'sku' AS sku,
       btrim(p->>'name') AS name,
       CASE p->>'sale_unit' WHEN 'kg' THEN 'kg' WHEN 'l' THEN 'lt' ELSE 'adet' END AS unit,
       round((p->>'sale_price_tl')::numeric * 100)::bigint AS price_amount,
       (p->>'tax_rate')::int * 100 AS tax_rate_bps,
       (p->>'is_active')::boolean AS is_active,
       CASE WHEN p->>'name' ~* '^k[ıi]ds' THEN 'Çocuk Burgerler'
            WHEN p->>'name' ~* 'tavuk'    THEN 'Tavuk Burgerler'
            ELSE 'Burgerler' END AS category_name,
       uuid_generate_v5(i.tenant_id, 'b2b:product:' || (p->>'b2b_id')) AS om_id
FROM imp_in i, jsonb_array_elements(i.doc->'products') p;

\if :rollback
  DELETE FROM branch_product_overrides
   WHERE product_id IN (SELECT om_id FROM imp_product)
     AND branch_id  IN (SELECT om_id FROM imp_branch);
  DELETE FROM products WHERE id IN (SELECT om_id FROM imp_product);
  DELETE FROM categories c
   USING imp_in i
   WHERE c.id IN (SELECT uuid_generate_v5(i.tenant_id, 'b2b:category:' || n)
                    FROM unnest(ARRAY['Burgerler','Tavuk Burgerler','Çocuk Burgerler']) AS n)
     AND NOT EXISTS (SELECT 1 FROM products x WHERE x.category_id = c.id);
  DELETE FROM branch_settings
   WHERE branch_id IN (SELECT om_id FROM imp_branch b, imp_in i
                        WHERE b.om_id = uuid_generate_v5(i.tenant_id, 'b2b:branch:' || b.b2b_id));
  DELETE FROM branches
   WHERE id IN (SELECT om_id FROM imp_branch b, imp_in i
                 WHERE b.om_id = uuid_generate_v5(i.tenant_id, 'b2b:branch:' || b.b2b_id));
\else
INSERT INTO branches (id, tenant_id, name, address, is_active, slug, ownership_type, operation_type)
SELECT b.om_id, i.tenant_id, b.name, b.address, b.is_active, b.slug, b.ownership_type, b.operation_type
FROM imp_branch b, imp_in i
ON CONFLICT (id) DO UPDATE
    SET name           = EXCLUDED.name,
        slug           = EXCLUDED.slug,
        ownership_type = EXCLUDED.ownership_type,
        operation_type = EXCLUDED.operation_type,
        address        = COALESCE(branches.address, EXCLUDED.address),
        updated_at     = NOW()
    WHERE branches.slug IS NULL;

-- 'none' is forbidden in production (ADR-FISCAL-001); prod currently runs the mock adapter.
INSERT INTO branch_settings (branch_id, tenant_id, fiscal_device_type, tax_rate_bps)
SELECT b.om_id, i.tenant_id, 'mock', 1000
FROM imp_branch b, imp_in i
ON CONFLICT (branch_id) DO NOTHING;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM imp_product p, imp_in i, products x
        WHERE x.tenant_id = i.tenant_id AND lower(x.name) = lower(p.name) AND x.id <> p.om_id
    ) THEN
        RAISE EXCEPTION 'import: OM''de aynı adlı (import dışı) ürün var — çift kayıt oluşmasın diye durduruldu';
    END IF;
END
$$;

INSERT INTO categories (id, tenant_id, name, sort_order)
SELECT uuid_generate_v5(i.tenant_id, 'b2b:category:' || c.name), i.tenant_id, c.name, c.sort_order
FROM (VALUES ('Burgerler', 30), ('Tavuk Burgerler', 40), ('Çocuk Burgerler', 50)) AS c(name, sort_order),
     imp_in i
WHERE c.name IN (SELECT category_name FROM imp_product)
  AND NOT EXISTS (SELECT 1 FROM categories x
                   WHERE x.tenant_id = i.tenant_id AND x.branch_id IS NULL AND x.name = c.name)
ON CONFLICT (id) DO NOTHING;

INSERT INTO products (id, tenant_id, category_id, name, price_amount, currency, sku, unit, tax_rate_bps, is_active)
SELECT p.om_id, i.tenant_id,
       (SELECT c.id FROM categories c
         WHERE c.tenant_id = i.tenant_id AND c.branch_id IS NULL AND c.name = p.category_name
         ORDER BY c.created_at LIMIT 1),
       p.name, p.price_amount, 'TRY', p.sku, p.unit, p.tax_rate_bps, p.is_active
FROM imp_product p, imp_in i
ON CONFLICT (id) DO UPDATE
    SET price_amount = EXCLUDED.price_amount,
        tax_rate_bps = EXCLUDED.tax_rate_bps,
        is_active    = EXCLUDED.is_active,
        sku          = EXCLUDED.sku,
        unit         = EXCLUDED.unit,
        updated_at   = NOW()
    WHERE (products.price_amount, products.tax_rate_bps, products.is_active, products.sku, products.unit)
          IS DISTINCT FROM
          (EXCLUDED.price_amount, EXCLUDED.tax_rate_bps, EXCLUDED.is_active, EXCLUDED.sku, EXCLUDED.unit);

-- Seed demo catalog (not b2b data) is deactivated, never deleted; only exactly these names, only rows
-- this import did not create.
UPDATE products x
   SET is_active = FALSE, updated_at = NOW()
  FROM imp_in i
 WHERE i.deact_demo = 1
   AND x.tenant_id = i.tenant_id
   AND x.is_active
   AND x.name IN ('Adana Kebap', 'Ayran', 'Lahmacun')
   AND x.id NOT IN (SELECT om_id FROM imp_product);

-- ---------------------------------------------------------------------------------------------
-- Per-branch prices → branch_product_overrides. Only rows that differ from the tenant price are
-- written (b2b has no availability table: every pos_sale product is sellable in every branch,
-- so is_available stays TRUE and no "closed" override rows are created).
-- ---------------------------------------------------------------------------------------------
INSERT INTO branch_product_overrides (tenant_id, branch_id, product_id, is_available, price_amount)
SELECT i.tenant_id, b.om_id, p.om_id, TRUE, round((x->>'sale_price_tl')::numeric * 100)::bigint
FROM imp_in i,
     jsonb_array_elements(i.doc->'branch_prices') x
     JOIN imp_branch  b ON b.b2b_id = (x->>'b2b_branch_id')::uuid
     JOIN imp_product p ON p.b2b_id = (x->>'b2b_product_id')::uuid
WHERE round((x->>'sale_price_tl')::numeric * 100)::bigint <> p.price_amount
ON CONFLICT (tenant_id, branch_id, product_id) DO UPDATE
    SET price_amount = EXCLUDED.price_amount,
        updated_at   = NOW()
    WHERE branch_product_overrides.price_amount IS DISTINCT FROM EXCLUDED.price_amount;

\endif

-- ---------------------------------------------------------------------------------------------
-- Proof of what this run touched
-- ---------------------------------------------------------------------------------------------
SELECT b.name AS branch, b.slug, b.ownership_type, b.operation_type,
       (SELECT count(*) FROM branch_product_overrides o
         WHERE o.branch_id = b.om_id AND o.product_id IN (SELECT om_id FROM imp_product)) AS price_overrides
FROM imp_branch b ORDER BY b.code;

SELECT (SELECT count(*) FROM imp_branch)  AS branches,
       (SELECT count(*) FROM categories c, imp_in i
         WHERE c.tenant_id = i.tenant_id AND c.name IN (SELECT category_name FROM imp_product)) AS categories,
       (SELECT count(*) FROM products WHERE id IN (SELECT om_id FROM imp_product)) AS products,
       (SELECT count(*) FROM branch_product_overrides
         WHERE product_id IN (SELECT om_id FROM imp_product)
           AND branch_id IN (SELECT om_id FROM imp_branch)) AS overrides,
       (SELECT count(*) FROM products x, imp_in i WHERE x.tenant_id = i.tenant_id AND x.is_active) AS active_products,
       (SELECT count(*) FROM products x, imp_in i WHERE x.tenant_id = i.tenant_id AND NOT x.is_active) AS inactive_products,
       (SELECT count(*) FROM branch_settings s, imp_in i WHERE s.tenant_id = i.tenant_id) AS branch_settings;

\if :dry_run
  ROLLBACK;
  \echo 'DRY RUN: ROLLBACK yapıldı, hiçbir değişiklik kalıcı değil. Gerçek koşu için -v dry_run=0'
\else
  COMMIT;
  \echo 'COMMIT yapıldı.'
\endif
