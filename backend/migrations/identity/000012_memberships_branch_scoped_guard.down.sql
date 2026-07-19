-- Migration: identity/000012_memberships_branch_scoped_guard (down)
-- Symmetric rollback: trigger, function, uniqueness semantics, column.

DROP TRIGGER IF EXISTS memberships_branch_scope_guard ON memberships;
DROP FUNCTION IF EXISTS memberships_branch_scope_guard();

ALTER TABLE memberships DROP CONSTRAINT memberships_unique;

ALTER TABLE memberships ADD CONSTRAINT memberships_unique
    UNIQUE (person_id, tenant_id, branch_id, role_id);

ALTER TABLE roles DROP COLUMN branch_scoped;
