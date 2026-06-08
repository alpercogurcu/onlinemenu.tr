-- Migration: identity/000002_create_roles
-- Three-tier role hierarchy:
--   System roles  : tenant_id IS NULL, is_system = true  (seeded in 000006)
--   Tenant roles  : tenant_id NOT NULL, branch_id IS NULL (chain-wide custom)
--   Branch roles  : tenant_id NOT NULL, branch_id NOT NULL (branch-specific custom)
--
-- RLS: system roles (tenant_id IS NULL) are visible to all tenants.

CREATE TABLE roles (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id  UUID        REFERENCES tenants(id) ON DELETE CASCADE,
    branch_id  UUID        REFERENCES branches(id) ON DELETE CASCADE,
    name       TEXT        NOT NULL,
    system_key TEXT,
    is_system  BOOLEAN     NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT roles_name_unique UNIQUE NULLS NOT DISTINCT (tenant_id, branch_id, name),
    -- system roles identified by system_key must be globally unique
    CONSTRAINT roles_system_key_unique UNIQUE (system_key)
);

CREATE INDEX roles_tenant_idx ON roles (tenant_id);
CREATE INDEX roles_branch_idx ON roles (branch_id);

ALTER TABLE roles ENABLE ROW LEVEL SECURITY;
ALTER TABLE roles FORCE ROW LEVEL SECURITY;

-- System roles (tenant_id IS NULL) are readable by every tenant.
-- Tenant and branch roles are only visible within their own tenant.
CREATE POLICY roles_select ON roles FOR SELECT TO app_runtime
    USING (
        tenant_id IS NULL
        OR tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid
    );

-- Only tenant's own roles can be inserted/updated/deleted.
CREATE POLICY roles_write ON roles FOR ALL TO app_runtime
    USING  (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid);
