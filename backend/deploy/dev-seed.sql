-- Dev seed: test işletmesi, admin kullanıcı, şube ve üyelik
-- Yalnızca development ortamında çalıştırılır.
-- app_migrator rolü ile (BYPASSRLS) çalıştırılmalıdır.

DO $$
DECLARE
    v_tenant_id   UUID := 'aaaaaaaa-0000-0000-0000-000000000001';
    v_branch_id   UUID := 'bbbbbbbb-0000-0000-0000-000000000001';
    v_person_id   UUID := 'cccccccc-0000-0000-0000-000000000001';
    v_manager_role UUID := '00000001-0000-0000-0000-000000000006';
BEGIN
    -- Tenant
    -- Seed'in tekrar koşturulması bilinçli olarak enabled_modules'ü buradaki
    -- sabit kümeye sıfırlar (R1): elle genişletilmiş bir dev tenant'ın
    -- modülleri de bu satır çalıştığında tekrar daralır. Bu, "seed her zaman
    -- aynı ortamı üretir" garantisi için kabul edilen bir davranıştır.
    INSERT INTO tenants (id, name, slug, plan, enabled_modules, is_active)
    VALUES (v_tenant_id, 'Test Restoran', 'test-restoran', 'starter', '["pos","catalog","inventory","storefront"]'::jsonb, TRUE)
    ON CONFLICT (id) DO UPDATE SET enabled_modules = EXCLUDED.enabled_modules;

    -- Branch
    INSERT INTO branches (id, tenant_id, name, is_active)
    VALUES (v_branch_id, v_tenant_id, 'Ana Şube', TRUE)
    ON CONFLICT (id) DO NOTHING;

    -- Person (admin@onlinemenu.tr)
    INSERT INTO persons (id, keycloak_sub, email, full_name)
    VALUES (v_person_id, 'dev-admin-sub', 'admin@onlinemenu.tr', 'Admin Kullanıcı')
    ON CONFLICT (id) DO NOTHING;

    -- Membership (chain-wide: branch_id = NULL for tenant-level access)
    INSERT INTO memberships (person_id, tenant_id, branch_id, role_id, status)
    VALUES (v_person_id, v_tenant_id, NULL, v_manager_role, 'active')
    ON CONFLICT (person_id, tenant_id, branch_id, role_id) DO NOTHING;

    -- Rol kullanıcıları (e2e / rol bazlı ekran testleri): sabit UUID'ler,
    -- dev login e-posta ile girer (APP_ENV=dev). Şube kapsamlı roller Ana Şube'ye bağlı.
    -- Yeniden koşturulabilirlik: kişi id çakışırsa ya da aynı e-posta farklı
    -- bir id ile zaten varsa insert sessizce atlanır; üyelikler sabit id yerine
    -- e-postadan çözülen id'ye bağlanır, böylece FK zinciri hiçbir durumda kopmaz.
    INSERT INTO persons (id, keycloak_sub, email, full_name)
    VALUES
        ('ffffffff-0000-0000-0000-000000000001', 'dev-shift-sub',   'shift@dev.onlinemenu.tr',   'Selin Vardiya'),
        ('ffffffff-0000-0000-0000-000000000002', 'dev-kasiyer-sub', 'kasiyer@dev.onlinemenu.tr', 'Kerem Kasa'),
        ('ffffffff-0000-0000-0000-000000000003', 'dev-garson-sub',  'garson@dev.onlinemenu.tr',  'Gamze Garson'),
        ('ffffffff-0000-0000-0000-000000000004', 'dev-mutfak-sub',  'mutfak@dev.onlinemenu.tr',  'Murat Mutfak')
    ON CONFLICT DO NOTHING;

    INSERT INTO memberships (person_id, tenant_id, branch_id, role_id, status)
    SELECT p.id, v_tenant_id, v_branch_id, r.role_id, 'active'
    FROM (VALUES
        ('shift@dev.onlinemenu.tr',   '00000001-0000-0000-0000-000000000002'::uuid),
        ('kasiyer@dev.onlinemenu.tr', '00000001-0000-0000-0000-000000000001'::uuid),
        ('garson@dev.onlinemenu.tr',  '00000001-0000-0000-0000-000000000008'::uuid),
        ('mutfak@dev.onlinemenu.tr',  '00000001-0000-0000-0000-000000000004'::uuid)
    ) AS r(email, role_id)
    JOIN persons p ON p.email = r.email
    ON CONFLICT (person_id, tenant_id, branch_id, role_id) DO NOTHING;

    RAISE NOTICE 'Dev seed OK — admin@onlinemenu.tr (+ shift/kasiyer/garson/mutfak@dev.onlinemenu.tr) | tenant: %', v_tenant_id;
END$$;

-- Storefront (QR menü) için asgari veri: masa planı + menü + modifier.
-- Bunlar olmadan /menu uygulaması boş menü gösterir ve QR kodu üretilecek
-- masa bulunamaz. Sabit UUID'ler kullanılır; script tekrar çalıştırılabilir.
DO $$
DECLARE
    v_tenant_id UUID := 'aaaaaaaa-0000-0000-0000-000000000001';
    v_branch_id UUID := 'bbbbbbbb-0000-0000-0000-000000000001';
    v_zone_id   UUID := 'dddddddd-0000-0000-0000-000000000001';
    v_menu_id   UUID := 'dddddddd-0000-0000-0000-000000000002';
    -- Kategoriler
    v_cat_main  UUID := 'dddddddd-0000-0000-0000-000000000011';
    v_cat_drink UUID := 'dddddddd-0000-0000-0000-000000000012';
    -- Modifier grupları
    v_grp_cook  UUID := 'dddddddd-0000-0000-0000-000000000021';
    v_grp_extra UUID := 'dddddddd-0000-0000-0000-000000000022';
BEGIN
    -- Masa planı: tek salon + 3 masa
    INSERT INTO table_zones (id, tenant_id, branch_id, name, floor, is_active)
    VALUES (v_zone_id, v_tenant_id, v_branch_id, 'Salon', 0, TRUE)
    ON CONFLICT (id) DO NOTHING;

    INSERT INTO tables (id, tenant_id, branch_id, zone_id, name, capacity, status, is_active)
    VALUES
        ('dddddddd-0000-0000-0000-000000000101', v_tenant_id, v_branch_id, v_zone_id, 'Masa 1', 4, 'empty', TRUE),
        ('dddddddd-0000-0000-0000-000000000102', v_tenant_id, v_branch_id, v_zone_id, 'Masa 2', 2, 'empty', TRUE),
        ('dddddddd-0000-0000-0000-000000000103', v_tenant_id, v_branch_id, v_zone_id, 'Masa 3', 6, 'empty', TRUE)
    ON CONFLICT (id) DO NOTHING;

    -- Kategoriler (sort_order storefront'ta görüntü sırasını belirler)
    INSERT INTO categories (id, tenant_id, name, is_active, sort_order)
    VALUES
        (v_cat_main,  v_tenant_id, 'Ana Yemekler', TRUE, 10),
        (v_cat_drink, v_tenant_id, 'İçecekler',    TRUE, 20)
    ON CONFLICT (id) DO NOTHING;

    -- Ürünler (price_amount kuruş cinsinden)
    INSERT INTO products (id, tenant_id, category_id, name, description, price_amount, currency, tax_rate_bps, is_active, sort_order)
    VALUES
        ('dddddddd-0000-0000-0000-000000000201', v_tenant_id, v_cat_main,  'Adana Kebap',   'Acılı, közlenmiş biber ile', 32000, 'TRY', 1000, TRUE, 10),
        ('dddddddd-0000-0000-0000-000000000202', v_tenant_id, v_cat_main,  'Tavuk Şiş',     'Pilav ve salata ile',        28000, 'TRY', 1000, TRUE, 20),
        ('dddddddd-0000-0000-0000-000000000203', v_tenant_id, v_cat_main,  'Lahmacun',      'Maydanoz ve limon ile',       9000, 'TRY', 1000, TRUE, 30),
        ('dddddddd-0000-0000-0000-000000000204', v_tenant_id, v_cat_main,  'Mercimek Çorba','Günün çorbası',               7500, 'TRY', 1000, TRUE, 40),
        ('dddddddd-0000-0000-0000-000000000205', v_tenant_id, v_cat_drink, 'Ayran',         '300 ml',                      4000, 'TRY', 1000, TRUE, 10),
        ('dddddddd-0000-0000-0000-000000000206', v_tenant_id, v_cat_drink, 'Şalgam',        'Acılı / acısız',              4500, 'TRY', 1000, TRUE, 20)
    ON CONFLICT (id) DO NOTHING;

    -- Menü: şubeye özel, süresiz geçerli
    INSERT INTO menus (id, tenant_id, branch_id, name, is_active, sort_order)
    VALUES (v_menu_id, v_tenant_id, v_branch_id, 'QR Menü', TRUE, 0)
    ON CONFLICT (id) DO NOTHING;

    INSERT INTO menu_items (menu_id, product_id, tenant_id, is_active, sort_order)
    SELECT v_menu_id, p.id, v_tenant_id, TRUE, p.sort_order
    FROM products p
    WHERE p.tenant_id = v_tenant_id
      AND p.id BETWEEN 'dddddddd-0000-0000-0000-000000000201' AND 'dddddddd-0000-0000-0000-000000000206'
    ON CONFLICT (menu_id, product_id) DO NOTHING;

    -- Modifier grupları: biri zorunlu-tekli, biri opsiyonel-çoklu.
    -- Storefront sepet doğrulaması (catalog PriceCart) her iki tipi de bu
    -- veriyle sınayabilsin diye ikisi birden seed'lenir.
    INSERT INTO modifier_groups (id, tenant_id, name, selection_type, min_selections, max_selections, is_required, sort_order)
    VALUES
        (v_grp_cook,  v_tenant_id, 'Pişirme Şekli', 'single',   1, 1,    TRUE,  10),
        (v_grp_extra, v_tenant_id, 'Ekstralar',     'multiple', 0, NULL, FALSE, 20)
    ON CONFLICT (id) DO NOTHING;

    INSERT INTO modifiers (id, tenant_id, group_id, name, price_delta, is_active, sort_order)
    VALUES
        ('dddddddd-0000-0000-0000-000000000301', v_tenant_id, v_grp_cook,  'Az pişmiş',    0,    TRUE, 10),
        ('dddddddd-0000-0000-0000-000000000302', v_tenant_id, v_grp_cook,  'Orta',         0,    TRUE, 20),
        ('dddddddd-0000-0000-0000-000000000303', v_tenant_id, v_grp_cook,  'İyi pişmiş',   0,    TRUE, 30),
        ('dddddddd-0000-0000-0000-000000000311', v_tenant_id, v_grp_extra, 'Ekstra sos',   1500, TRUE, 10),
        ('dddddddd-0000-0000-0000-000000000312', v_tenant_id, v_grp_extra, 'Ekstra pilav', 3000, TRUE, 20)
    ON CONFLICT (id) DO NOTHING;

    INSERT INTO product_modifier_groups (product_id, group_id, tenant_id, sort_order)
    VALUES
        ('dddddddd-0000-0000-0000-000000000201', v_grp_cook,  v_tenant_id, 10),
        ('dddddddd-0000-0000-0000-000000000201', v_grp_extra, v_tenant_id, 20),
        ('dddddddd-0000-0000-0000-000000000202', v_grp_cook,  v_tenant_id, 10)
    ON CONFLICT (product_id, group_id) DO NOTHING;

    RAISE NOTICE 'Storefront seed OK — 3 masa, 6 ürün, 2 modifier grubu | şube: %', v_branch_id;
END$$;
