-- Migration: identity/000005_create_role_field_policies
-- Field-level visibility: a row's presence means the field IS visible for that role.
-- Absence = hidden (default deny). The projection layer omits hidden fields entirely
-- from the JSON response — it does NOT return null (null means "value is null").
--
-- Wildcard: resource='*' field='*' means all fields visible (used for manager role).
-- tenant_id mirrors parent role for direct RLS.

CREATE TABLE role_field_policies (
    id        UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    role_id   UUID        NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    tenant_id UUID        REFERENCES tenants(id) ON DELETE CASCADE,
    resource  TEXT        NOT NULL,
    field     TEXT        NOT NULL,

    CONSTRAINT role_field_policies_unique UNIQUE (role_id, resource, field)
);

CREATE INDEX role_field_policies_role_idx   ON role_field_policies (role_id);
CREATE INDEX role_field_policies_tenant_idx ON role_field_policies (tenant_id);

ALTER TABLE role_field_policies ENABLE ROW LEVEL SECURITY;
ALTER TABLE role_field_policies FORCE ROW LEVEL SECURITY;

CREATE POLICY role_field_policies_select ON role_field_policies FOR SELECT TO app_runtime
    USING (
        tenant_id IS NULL
        OR tenant_id = current_setting('app.tenant_id', TRUE)::uuid
    );

CREATE POLICY role_field_policies_write ON role_field_policies FOR ALL TO app_runtime
    USING  (tenant_id = current_setting('app.tenant_id', TRUE)::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id', TRUE)::uuid);
