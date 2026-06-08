-- Migration: identity/000004_create_role_permissions
-- Controller-level authorization: which (resource, action) pairs a role may perform.
-- tenant_id mirrors the parent role's tenant_id for direct RLS without a join.
-- Wildcard: resource='*' action='*' means unrestricted (used for manager/owner roles).
--
-- Default deny: absence of a row = permission denied.

CREATE TABLE role_permissions (
    id        UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    role_id   UUID        NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    tenant_id UUID        REFERENCES tenants(id) ON DELETE CASCADE,
    resource  TEXT        NOT NULL,
    action    TEXT        NOT NULL,

    CONSTRAINT role_permissions_unique UNIQUE (role_id, resource, action)
);

CREATE INDEX role_permissions_role_idx   ON role_permissions (role_id);
CREATE INDEX role_permissions_tenant_idx ON role_permissions (tenant_id);

ALTER TABLE role_permissions ENABLE ROW LEVEL SECURITY;
ALTER TABLE role_permissions FORCE ROW LEVEL SECURITY;

-- System role permissions (tenant_id IS NULL) readable by all tenants.
CREATE POLICY role_permissions_select ON role_permissions FOR SELECT TO app_runtime
    USING (
        tenant_id IS NULL
        OR tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid
    );

CREATE POLICY role_permissions_write ON role_permissions FOR ALL TO app_runtime
    USING  (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid);
