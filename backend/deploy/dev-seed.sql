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
    INSERT INTO tenants (id, name, slug, plan, enabled_modules, is_active)
    VALUES (v_tenant_id, 'Test Restoran', 'test-restoran', 'starter', '["pos","catalog","inventory","billing","party","hr"]'::jsonb, TRUE)
    ON CONFLICT (id) DO NOTHING;

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

    RAISE NOTICE 'Dev seed OK — admin@onlinemenu.tr | tenant: %', v_tenant_id;
END$$;
