-- Migration: catalog/000001_create_catalog
-- Creates the core catalog tables: categories, products, modifier_groups, modifiers,
-- menus, and omnichannel availability.
--
-- Design decisions:
--   - All tables are tenant-scoped with FORCE RLS (ADR-SEC-001, ADR-SEC-002)
--   - product_channel_availability decouples product visibility from channels
--     (dine_in, takeaway, delivery) and delivery integrators (getir, trendyol, etc.)
--   - auto_close_on_zero_stock: POS stops taking orders when stock hits 0
--   - sort_order on all list tables: explicit ordering, not insertion-time ordering
--   - No cross-module FKs; tenant_id is bare UUID (no FK to tenants table)
--
-- ADR references: ADR-SEC-001, ADR-SEC-002, DATA-004 (delta sync)

-- ============================================================
-- categories
-- ============================================================
CREATE TABLE categories (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID        NOT NULL,
    branch_id       UUID,                               -- NULL = tenant-wide; non-NULL = branch-specific override
    parent_id       UUID        REFERENCES categories(id) ON DELETE SET NULL,
    name            TEXT        NOT NULL,
    description     TEXT,
    image_key       TEXT,                               -- MinIO object key
    is_active       BOOLEAN     NOT NULL DEFAULT TRUE,
    sort_order      SMALLINT    NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX categories_tenant_idx ON categories (tenant_id) WHERE is_active;
CREATE INDEX categories_branch_idx ON categories (branch_id) WHERE branch_id IS NOT NULL;
CREATE INDEX categories_parent_idx ON categories (parent_id) WHERE parent_id IS NOT NULL;

ALTER TABLE categories ENABLE ROW LEVEL SECURITY;
ALTER TABLE categories FORCE ROW LEVEL SECURITY;

CREATE POLICY categories_read ON categories FOR SELECT TO app_runtime
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid);

CREATE POLICY categories_write ON categories FOR ALL TO app_runtime
    USING  (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid);

-- ============================================================
-- products
-- ============================================================
CREATE TABLE products (
    id                      UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id               UUID        NOT NULL,
    category_id             UUID        REFERENCES categories(id) ON DELETE SET NULL,
    name                    TEXT        NOT NULL,
    description             TEXT,
    image_key               TEXT,
    price_amount            BIGINT      NOT NULL CHECK (price_amount >= 0),  -- kuruş (1/100 TL)
    currency                CHAR(3)     NOT NULL DEFAULT 'TRY',
    sku                     TEXT,
    barcode                 TEXT,
    unit                    TEXT        NOT NULL DEFAULT 'adet',             -- adet, kg, lt, porsiyon
    tax_rate_bps            INT         NOT NULL DEFAULT 1800                -- 1800 = %18
                                CHECK (tax_rate_bps >= 0 AND tax_rate_bps <= 10000),
    is_active               BOOLEAN     NOT NULL DEFAULT TRUE,
    auto_close_on_zero_stock BOOLEAN    NOT NULL DEFAULT FALSE,              -- POS'ta stok 0'a düşünce satışa kapat
    stock_quantity          INT,                                             -- NULL = sınırsız stok
    sort_order              SMALLINT    NOT NULL DEFAULT 0,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX products_tenant_idx    ON products (tenant_id) WHERE is_active;
CREATE INDEX products_category_idx  ON products (category_id);
CREATE INDEX products_sku_idx       ON products (tenant_id, sku) WHERE sku IS NOT NULL;
CREATE INDEX products_barcode_idx   ON products (tenant_id, barcode) WHERE barcode IS NOT NULL;

ALTER TABLE products ENABLE ROW LEVEL SECURITY;
ALTER TABLE products FORCE ROW LEVEL SECURITY;

CREATE POLICY products_read ON products FOR SELECT TO app_runtime
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid);

CREATE POLICY products_write ON products FOR ALL TO app_runtime
    USING  (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid);

-- ============================================================
-- modifier_groups
-- Logical groupings of modifiers (e.g. "Soslar", "Pişirme Şekli").
-- A product may have multiple modifier groups.
-- ============================================================
CREATE TABLE modifier_groups (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID        NOT NULL,
    name            TEXT        NOT NULL,
    selection_type  TEXT        NOT NULL DEFAULT 'single'
                        CHECK (selection_type IN ('single', 'multiple')),
    min_selections  SMALLINT    NOT NULL DEFAULT 0,
    max_selections  SMALLINT,                                               -- NULL = unlimited
    is_required     BOOLEAN     NOT NULL DEFAULT FALSE,
    sort_order      SMALLINT    NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX modifier_groups_tenant_idx ON modifier_groups (tenant_id);

ALTER TABLE modifier_groups ENABLE ROW LEVEL SECURITY;
ALTER TABLE modifier_groups FORCE ROW LEVEL SECURITY;

CREATE POLICY modifier_groups_read ON modifier_groups FOR SELECT TO app_runtime
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid);

CREATE POLICY modifier_groups_write ON modifier_groups FOR ALL TO app_runtime
    USING  (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid);

-- ============================================================
-- modifiers
-- Individual modifier options within a group (e.g. "Acı Sos", "İyi Pişmiş").
-- ============================================================
CREATE TABLE modifiers (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID        NOT NULL,
    group_id        UUID        NOT NULL REFERENCES modifier_groups(id) ON DELETE CASCADE,
    name            TEXT        NOT NULL,
    price_delta     BIGINT      NOT NULL DEFAULT 0,                         -- ek ücret, kuruş cinsinden (negatif olabilir)
    is_active       BOOLEAN     NOT NULL DEFAULT TRUE,
    sort_order      SMALLINT    NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX modifiers_group_idx  ON modifiers (group_id) WHERE is_active;
CREATE INDEX modifiers_tenant_idx ON modifiers (tenant_id);

ALTER TABLE modifiers ENABLE ROW LEVEL SECURITY;
ALTER TABLE modifiers FORCE ROW LEVEL SECURITY;

CREATE POLICY modifiers_read ON modifiers FOR SELECT TO app_runtime
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid);

CREATE POLICY modifiers_write ON modifiers FOR ALL TO app_runtime
    USING  (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid);

-- ============================================================
-- product_modifier_groups
-- Many-to-many: which modifier groups apply to which products.
-- ============================================================
CREATE TABLE product_modifier_groups (
    product_id      UUID        NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    group_id        UUID        NOT NULL REFERENCES modifier_groups(id) ON DELETE CASCADE,
    tenant_id       UUID        NOT NULL,
    sort_order      SMALLINT    NOT NULL DEFAULT 0,
    PRIMARY KEY (product_id, group_id)
);

CREATE INDEX pmg_group_idx  ON product_modifier_groups (group_id);
CREATE INDEX pmg_tenant_idx ON product_modifier_groups (tenant_id);

ALTER TABLE product_modifier_groups ENABLE ROW LEVEL SECURITY;
ALTER TABLE product_modifier_groups FORCE ROW LEVEL SECURITY;

CREATE POLICY pmg_read ON product_modifier_groups FOR SELECT TO app_runtime
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid);

CREATE POLICY pmg_write ON product_modifier_groups FOR ALL TO app_runtime
    USING  (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid);

-- ============================================================
-- menus
-- A menu groups products for a specific context (lunch, dinner, seasonal, delivery).
-- Products can appear in multiple menus.
-- ============================================================
CREATE TABLE menus (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID        NOT NULL,
    branch_id       UUID,                               -- NULL = all branches
    name            TEXT        NOT NULL,
    description     TEXT,
    is_active       BOOLEAN     NOT NULL DEFAULT TRUE,
    valid_from      DATE,
    valid_until     DATE,
    sort_order      SMALLINT    NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX menus_tenant_idx ON menus (tenant_id) WHERE is_active;
CREATE INDEX menus_branch_idx ON menus (branch_id) WHERE branch_id IS NOT NULL;

ALTER TABLE menus ENABLE ROW LEVEL SECURITY;
ALTER TABLE menus FORCE ROW LEVEL SECURITY;

CREATE POLICY menus_read ON menus FOR SELECT TO app_runtime
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid);

CREATE POLICY menus_write ON menus FOR ALL TO app_runtime
    USING  (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid);

-- ============================================================
-- menu_items
-- Products assigned to menus with per-menu price overrides.
-- ============================================================
CREATE TABLE menu_items (
    menu_id         UUID        NOT NULL REFERENCES menus(id) ON DELETE CASCADE,
    product_id      UUID        NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    tenant_id       UUID        NOT NULL,
    price_override  BIGINT,                             -- NULL = use products.price_amount
    is_active       BOOLEAN     NOT NULL DEFAULT TRUE,
    sort_order      SMALLINT    NOT NULL DEFAULT 0,
    PRIMARY KEY (menu_id, product_id)
);

CREATE INDEX menu_items_product_idx ON menu_items (product_id);
CREATE INDEX menu_items_tenant_idx  ON menu_items (tenant_id);

ALTER TABLE menu_items ENABLE ROW LEVEL SECURITY;
ALTER TABLE menu_items FORCE ROW LEVEL SECURITY;

CREATE POLICY menu_items_read ON menu_items FOR SELECT TO app_runtime
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid);

CREATE POLICY menu_items_write ON menu_items FOR ALL TO app_runtime
    USING  (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid);

-- ============================================================
-- product_channel_availability
-- Controls per-product visibility by order channel and delivery integrator.
--
-- order_channel: dine_in | takeaway | delivery
-- integrator_slug: NULL = all integrators; or e.g. 'getir', 'trendyol', 'yemeksepeti'
-- is_available: FALSE = hidden/closed on this channel
-- ============================================================
CREATE TABLE product_channel_availability (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID        NOT NULL,
    product_id      UUID        NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    order_channel   TEXT        NOT NULL
                        CHECK (order_channel IN ('dine_in', 'takeaway', 'delivery')),
    integrator_slug TEXT,                               -- NULL = all integrators for this channel
    is_available    BOOLEAN     NOT NULL DEFAULT TRUE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- One row per (product, channel, integrator) combination
    UNIQUE (product_id, order_channel, integrator_slug)
);

CREATE INDEX pca_product_idx  ON product_channel_availability (product_id);
CREATE INDEX pca_tenant_idx   ON product_channel_availability (tenant_id);
CREATE INDEX pca_channel_idx  ON product_channel_availability (order_channel, integrator_slug);

ALTER TABLE product_channel_availability ENABLE ROW LEVEL SECURITY;
ALTER TABLE product_channel_availability FORCE ROW LEVEL SECURITY;

CREATE POLICY pca_read ON product_channel_availability FOR SELECT TO app_runtime
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid);

CREATE POLICY pca_write ON product_channel_availability FOR ALL TO app_runtime
    USING  (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid);

COMMENT ON TABLE product_channel_availability IS
    'Ürünün hangi sipariş kanalında (dine_in/takeaway/delivery) ve hangi entegratörde '
    'aktif olduğunu kontrol eder. NULL integrator_slug = kanalın tüm entegratörleri. '
    'ADR-DATA-004 delta sync kataloğu bu tablodan okur.';

COMMENT ON TABLE products IS
    'Tenant''ın satışa çıkardığı tüm ürünler. auto_close_on_zero_stock POS''un stok '
    'sıfırlandığında ürünü otomatik kapatmasını sağlar.';
