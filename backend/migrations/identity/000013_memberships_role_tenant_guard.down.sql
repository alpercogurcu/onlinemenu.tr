-- Migration: identity/000013_memberships_role_tenant_guard (down)
--
-- Symmetric rollback: restores the 000012 function body verbatim (early return
-- on a concrete branch_id, no explicit tenant predicate). The trigger binding
-- is untouched in both directions, so CREATE OR REPLACE is sufficient.

CREATE OR REPLACE FUNCTION memberships_branch_scope_guard() RETURNS TRIGGER
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
