-- backend/loadtest/seed.sql
--
-- k6 yük testi için terminal (kasiyer) havuzu.
--
-- dev-seed.sql (backend/deploy/dev-seed.sql) yalnızca tek bir yönetici
-- kullanıcısı oluşturur — 500 VU'yu tek bir person/token ile simüle etmek,
-- gerçek POS filosunu (çok terminal, çok kasiyer) temsil etmez ve tüm
-- isteklerin aynı Authorization header'ıyla gitmesi authz/log ayrımını da
-- anlamsızlaştırır. Bu script dev-seed'in üstüne LOADTEST_CASHIER_COUNT
-- (varsayılan 50) adet "kasiyer" rolünde person + membership ekler; k6
-- setup() bu havuzdan round-robin token alır.
--
-- Idempotent: deterministik UUID'ler (namespace: eeeeeeee-...) + ON CONFLICT
-- DO NOTHING — tekrar tekrar çalıştırılabilir, dev-seed.sql gibi
-- app_migrator (BYPASSRLS) ile çalıştırılmalıdır.
--
-- Kullanım:
--   docker exec -i onlinemenu-dev-postgres-1 \
--     psql -U app_migrator -d onlinemenu_dev < backend/loadtest/seed.sql

DO $$
DECLARE
    v_tenant_id    UUID := 'aaaaaaaa-0000-0000-0000-000000000001';
    v_branch_id    UUID := 'bbbbbbbb-0000-0000-0000-000000000001';
    v_cashier_role UUID := '00000001-0000-0000-0000-000000000001';
    v_count        INT  := 50;
    i              INT;
    v_person_id    UUID;
BEGIN
    FOR i IN 1..v_count LOOP
        -- Deterministic UUID: eeeeeeee-0000-0000-0000-{i as 12-hex}.
        v_person_id := ('eeeeeeee-0000-0000-0000-' || lpad(i::text, 12, '0'))::uuid;

        INSERT INTO persons (id, keycloak_sub, email, full_name)
        VALUES (
            v_person_id,
            'loadtest-cashier-sub-' || i,
            'loadtest-cashier-' || i || '@onlinemenu.tr',
            'Yük Testi Kasiyer ' || i
        )
        ON CONFLICT (id) DO NOTHING;

        INSERT INTO memberships (person_id, tenant_id, branch_id, role_id, status)
        VALUES (v_person_id, v_tenant_id, v_branch_id, v_cashier_role, 'active')
        ON CONFLICT (person_id, tenant_id, branch_id, role_id) DO NOTHING;
    END LOOP;

    RAISE NOTICE 'Loadtest seed OK — % kasiyer, tenant: %, branch: %', v_count, v_tenant_id, v_branch_id;
END$$;
