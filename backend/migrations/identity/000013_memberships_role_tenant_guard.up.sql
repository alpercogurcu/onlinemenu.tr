-- Migration: identity/000013_memberships_role_tenant_guard
--
-- Problem: nothing states "a membership's role must belong to the membership's
-- tenant". Migration 000012 closes part of it by accident: its guard reads the
-- role under the caller's RLS, so a cross-tenant role_id is invisible and the
-- insert fails closed. But that cover has a hole and an implicit dependency.
--
-- The hole: 000012's guard returns early when NEW.branch_id IS NOT NULL — the
-- role row is never read at all. The memberships.role_id FK is validated by the
-- system, which bypasses RLS, so a concrete-branch membership pointing at
-- another tenant's role is accepted at the DB layer today. Only
-- MembershipService.Create (which pre-fetches the role via RoleRepo.GetByID,
-- tenant-filtered) stands in the way; direct SQL or a future code path does not.
--
-- The implicit dependency: "invisible ⇒ rejected" is a property of the roles
-- RLS policy set, not of this rule. 000009 already added roles_all_scope_read
-- (app.tenant_scope = 'all_tenants' sees every tenant's roles); any future
-- widening of role visibility silently erodes the guarantee.
--
-- Fix: read the role row unconditionally (before the branch_id early return)
-- and assert the tenant relation with an explicit predicate:
--   roles.tenant_id IS NULL (system template, grantable by every tenant)
--   OR roles.tenant_id = memberships.tenant_id
--
-- Why a trigger and not a declarative constraint:
--   * Composite FK (role_id, tenant_id) -> roles (id, tenant_id) cannot express
--     it. MATCH SIMPLE only skips the check when a *referencing* column is NULL;
--     here both referencing columns are non-null and it is the *referenced*
--     roles.tenant_id that is NULL for system templates. Every membership on a
--     system role would be rejected.
--   * A generated column cannot help either — it may not read another table.
--   * A CHECK constraint may not contain a subquery.
-- The trigger from 000012 is therefore the only place the rule can live; this
-- migration extends it rather than adding a second one.
--
-- See docs/adr/SEC-005-branch-scoped-membership.md (§ "000013 eki").

CREATE OR REPLACE FUNCTION memberships_branch_scope_guard() RETURNS TRIGGER
LANGUAGE plpgsql AS $$
DECLARE
    v_role_tenant   UUID;
    v_branch_scoped BOOLEAN;
    v_role_branch   UUID;
BEGIN
    -- Unconditional: unlike 000012, the concrete-branch case is checked too.
    SELECT r.tenant_id, r.branch_scoped, r.branch_id
      INTO v_role_tenant, v_branch_scoped, v_role_branch
      FROM roles r
     WHERE r.id = NEW.role_id;

    -- Fail closed: dangling role_id, or a role hidden by the caller's RLS.
    IF NOT FOUND THEN
        RAISE EXCEPTION
            'membership role % is not visible in the current tenant context', NEW.role_id
            USING ERRCODE = '23514';
    END IF;

    -- Explicit tenant integrity. Under app_runtime this is normally unreachable
    -- (a tenant-mismatched role is also RLS-invisible and already rejected
    -- above), but it makes the rule independent of the roles policy set: it
    -- still holds for platform-scope sessions (app.tenant_scope = 'all_tenants',
    -- identity/000009) and for app_migrator, neither of which is RLS-restricted
    -- on roles.
    IF v_role_tenant IS NOT NULL AND v_role_tenant <> NEW.tenant_id THEN
        RAISE EXCEPTION
            'membership role % belongs to tenant %, not to membership tenant %',
            NEW.role_id, v_role_tenant, NEW.tenant_id
            USING ERRCODE = '23514';
    END IF;

    -- A concrete branch can never over-grant (ADR-SEC-005).
    IF NEW.branch_id IS NOT NULL THEN
        RETURN NEW;
    END IF;

    IF v_branch_scoped OR v_role_branch IS NOT NULL THEN
        RAISE EXCEPTION
            'role % is branch-scoped and requires a non-null membership branch_id', NEW.role_id
            USING ERRCODE = '23514';
    END IF;

    RETURN NEW;
END;
$$;

-- The trigger definition (BEFORE INSERT OR UPDATE OF role_id, branch_id) is
-- unchanged: CREATE OR REPLACE FUNCTION keeps the existing binding.
--
-- tenant_id is deliberately absent from the UPDATE OF list. Moving a membership
-- row across tenants is already impossible for app_runtime — memberships_write
-- (000003) carries the tenant predicate in both USING and WITH CHECK, so the
-- old row would be invisible and the new one rejected. Adding tenant_id here
-- would force a DROP/CREATE TRIGGER pair in both directions for no reachable
-- gain.
