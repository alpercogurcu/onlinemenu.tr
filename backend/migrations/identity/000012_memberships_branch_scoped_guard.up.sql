-- Migration: identity/000012_memberships_branch_scoped_guard
--
-- Problem: a branch-scoped role (cashier, kitchen, …) could be granted through a
-- membership with branch_id IS NULL, which the authorization chain reads as
-- "every branch of the tenant". The previous defence lived in Go
-- (domain.Role.Scope() == RoleScopeBranch) and never fired for tenant role
-- clones: identity/events/subscriber.go::seedTenantRoles copies system role
-- templates with system_key = NULL, branch_id = NULL and a fresh random id.
--
-- Fix: carry the branch-scope requirement on the role row itself
-- (roles.branch_scoped). Clones copy the flag, so the guard survives cloning.
-- No CHECK constraint may reference the fixed system role UUIDs — clones do not
-- have them. See docs/adr/SEC-005-branch-scoped-membership.md.

-- ============================================================
-- 1. roles.branch_scoped
-- ============================================================

ALTER TABLE roles ADD COLUMN branch_scoped BOOLEAN NOT NULL DEFAULT FALSE;

COMMENT ON COLUMN roles.branch_scoped IS
    'TRUE when a membership granting this role must name a concrete branch. '
    'Enforced by the memberships_branch_scope_guard trigger. Tenant clones copy '
    'this flag from their system template (identity/events/subscriber.go).';

-- System templates. Kept as a literal system_key list because templates always
-- retain their system_key; only clones lose it.
-- 'waiter' is not seeded today (no such template in 000006/000010); it is listed
-- forward-looking so a future seed inherits the correct default.
UPDATE roles
SET branch_scoped = TRUE
WHERE tenant_id IS NULL
  AND system_key IN ('cashier', 'shift_manager', 'driver', 'kitchen', 'bar', 'warehouse', 'waiter');

-- Existing tenant clones: best-effort backfill by template name. seedTenantRoles
-- clones with `name` copied verbatim, so the join holds for untouched clones.
-- LIMITATION: a tenant that renamed its clone is missed here. The post-deploy
-- fingerprint query in SEC-005 must be run to find such rows.
UPDATE roles AS clone
SET branch_scoped = TRUE
FROM roles AS tmpl
WHERE tmpl.tenant_id IS NULL
  AND tmpl.branch_scoped
  AND clone.tenant_id IS NOT NULL
  AND clone.name = tmpl.name;

-- Custom branch-specific roles are branch-scoped by construction.
UPDATE roles
SET branch_scoped = TRUE
WHERE branch_id IS NOT NULL;

-- ============================================================
-- 2. memberships uniqueness under NULL branch_id
-- ============================================================
-- The default UNIQUE treats NULLs as distinct, so the same person could hold
-- the same chain-wide role many times over. roles already uses NULLS NOT
-- DISTINCT (000002); align memberships with it.
-- PRE-DEPLOY: duplicate chain-wide rows abort this migration — run the dedup
-- detection query in SEC-005 first.

ALTER TABLE memberships DROP CONSTRAINT memberships_unique;

ALTER TABLE memberships ADD CONSTRAINT memberships_unique
    UNIQUE NULLS NOT DISTINCT (person_id, tenant_id, branch_id, role_id);

-- ============================================================
-- 3. Guard trigger
-- ============================================================
-- SECURITY INVOKER (default): the role lookup runs under the caller's RLS, so a
-- role_id belonging to another tenant is invisible and the insert fails closed.

CREATE FUNCTION memberships_branch_scope_guard() RETURNS TRIGGER
LANGUAGE plpgsql AS $$
DECLARE
    v_branch_scoped BOOLEAN;
    v_role_branch   UUID;
BEGIN
    -- A concrete branch can never over-grant.
    IF NEW.branch_id IS NOT NULL THEN
        RETURN NEW;
    END IF;

    SELECT r.branch_scoped, r.branch_id
      INTO v_branch_scoped, v_role_branch
      FROM roles r
     WHERE r.id = NEW.role_id;

    -- Fail closed: cross-tenant or dangling role_id.
    IF NOT FOUND THEN
        RAISE EXCEPTION
            'membership role % is not visible in the current tenant context', NEW.role_id
            USING ERRCODE = '23514';
    END IF;

    IF v_branch_scoped OR v_role_branch IS NOT NULL THEN
        RAISE EXCEPTION
            'role % is branch-scoped and requires a non-null membership branch_id', NEW.role_id
            USING ERRCODE = '23514';
    END IF;

    RETURN NEW;
END;
$$;

REVOKE ALL ON FUNCTION memberships_branch_scope_guard() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION memberships_branch_scope_guard() TO app_runtime;

CREATE TRIGGER memberships_branch_scope_guard
    BEFORE INSERT OR UPDATE OF role_id, branch_id ON memberships
    FOR EACH ROW EXECUTE FUNCTION memberships_branch_scope_guard();

-- NOTE: the trigger only guards INSERT/UPDATE. Rows written before this
-- migration are not re-validated — see the audit query in SEC-005.
