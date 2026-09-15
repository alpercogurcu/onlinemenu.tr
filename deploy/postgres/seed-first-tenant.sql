-- First tenant seed — bootstraps a single pilot tenant + branch + manager
-- membership before self-service onboarding exists.
--
-- Run as app_migrator (BYPASSRLS — no `SET LOCAL app.tenant_id` needed) and
-- only through deploy/scripts/seed-first-tenant.sh, which passes ON_ERROR_STOP=1
-- and these psql variables from environment variables:
--
--   psql "$DSN" -v ON_ERROR_STOP=1 \
--     -v tenant_name=...   -v tenant_slug=... -v branch_name=... \
--     -v admin_email=...   -v admin_name=...  -v keycloak_sub=... \
--     -f deploy/postgres/seed-first-tenant.sql
--
-- Scope: one tenant + one branch + one manager membership (chain-wide,
-- branch_id NULL — mirrors backend/deploy/dev-seed.sql) + the admin person
-- row. No product/table/menu data — the pilot tenant enters its own catalog.
--
-- Idempotent AND fail-closed: a re-run with the same inputs is a no-op; a
-- re-run whose inputs disagree with what is already stored (same slug but a
-- different tenant name, same admin e-mail but a different Keycloak sub)
-- ABORTS instead of silently rewriting production data — a typo in a CLI
-- variable must never re-point the admin account to another Keycloak user.
--
-- Constraints relied on (see the identity/tenant migrations):
--   * tenants.slug UNIQUE, persons.email UNIQUE, persons.keycloak_sub UNIQUE
--   * branches: no unique key beyond the PK → matched by (tenant_id, name)
--   * memberships UNIQUE (person_id, tenant_id, branch_id, role_id) is a plain
--     btree index, so NULL branch_id rows never conflict with each other →
--     matched explicitly with NOT EXISTS.
--
-- Manager role UUID ('00000001-0000-0000-0000-000000000006') is seeded in
-- backend/migrations/identity/000006_seed_system_roles.up.sql and is a
-- platform-wide constant (not tenant-scoped) — safe to hardcode.
--
-- psql variables are not interpolated inside dollar-quoted bodies, so they
-- are handed to the DO block through session settings (set_config).

SELECT set_config('seed.tenant_name',  :'tenant_name',  false),
       set_config('seed.tenant_slug',  :'tenant_slug',  false),
       set_config('seed.branch_name',  :'branch_name',  false),
       set_config('seed.admin_email',  :'admin_email',  false),
       set_config('seed.admin_name',   :'admin_name',   false),
       set_config('seed.keycloak_sub', :'keycloak_sub', false);

DO $$
DECLARE
    v_tenant_name  TEXT := current_setting('seed.tenant_name');
    v_tenant_slug  TEXT := current_setting('seed.tenant_slug');
    v_branch_name  TEXT := current_setting('seed.branch_name');
    v_admin_email  TEXT := current_setting('seed.admin_email');
    v_admin_name   TEXT := current_setting('seed.admin_name');
    v_keycloak_sub TEXT := current_setting('seed.keycloak_sub');
    v_manager_role UUID := '00000001-0000-0000-0000-000000000006';
    v_tenant_id    UUID;
    v_branch_id    UUID;
    v_person_id    UUID;
    v_existing     TEXT;
BEGIN
    IF v_tenant_slug = '' OR v_admin_email = '' OR v_keycloak_sub = '' THEN
        RAISE EXCEPTION 'seed: tenant_slug, admin_email and keycloak_sub must be non-empty';
    END IF;

    -- tenant: create once; on re-run the stored name must match the input
    SELECT id, name INTO v_tenant_id, v_existing FROM tenants WHERE slug = v_tenant_slug;
    IF v_tenant_id IS NULL THEN
        INSERT INTO tenants (name, slug, plan, enabled_modules, is_active)
        VALUES (v_tenant_name, v_tenant_slug, 'starter',
                '["pos","catalog","inventory","storefront"]'::jsonb, TRUE)
        RETURNING id INTO v_tenant_id;
    ELSIF v_existing <> v_tenant_name THEN
        RAISE EXCEPTION 'seed: tenant slug % already exists with name "%" (input: "%") — refusing to rename',
            v_tenant_slug, v_existing, v_tenant_name;
    END IF;

    -- branch: matched by (tenant_id, name); no rename semantics
    SELECT id INTO v_branch_id FROM branches
    WHERE tenant_id = v_tenant_id AND name = v_branch_name;
    IF v_branch_id IS NULL THEN
        INSERT INTO branches (tenant_id, name, is_active)
        VALUES (v_tenant_id, v_branch_name, TRUE)
        RETURNING id INTO v_branch_id;
    END IF;

    -- admin person: e-mail ↔ keycloak_sub pair must be consistent with what
    -- is stored; a different sub for a known e-mail is an operator error
    SELECT id, keycloak_sub INTO v_person_id, v_existing FROM persons WHERE email = v_admin_email;
    IF v_person_id IS NULL THEN
        IF EXISTS (SELECT 1 FROM persons WHERE keycloak_sub = v_keycloak_sub) THEN
            RAISE EXCEPTION 'seed: keycloak_sub % already belongs to another person — refusing',
                v_keycloak_sub;
        END IF;
        INSERT INTO persons (keycloak_sub, email, full_name)
        VALUES (v_keycloak_sub, v_admin_email, v_admin_name)
        RETURNING id INTO v_person_id;
    ELSIF v_existing <> v_keycloak_sub THEN
        RAISE EXCEPTION 'seed: % is already linked to keycloak_sub % (input: %) — refusing to re-link',
            v_admin_email, v_existing, v_keycloak_sub;
    END IF;

    -- manager membership (chain-wide: branch_id NULL)
    INSERT INTO memberships (person_id, tenant_id, branch_id, role_id, status)
    SELECT v_person_id, v_tenant_id, NULL, v_manager_role, 'active'
    WHERE NOT EXISTS (
        SELECT 1 FROM memberships
        WHERE person_id = v_person_id AND tenant_id = v_tenant_id
          AND branch_id IS NULL AND role_id = v_manager_role
    );

    RAISE NOTICE 'First tenant seed OK — tenant: % branch: % admin person: %',
        v_tenant_id, v_branch_id, v_person_id;
END
$$;
