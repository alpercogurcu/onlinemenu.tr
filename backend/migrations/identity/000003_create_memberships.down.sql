-- Migration: identity/000003_create_memberships (rollback)
-- Reverses both halves of the up migration:
--
--   1. The deferred persons_select/persons_update RLS policies (which
--      reference memberships in a subquery) must be dropped BEFORE the
--      memberships table itself, otherwise DROP TABLE fails with a
--      dependent-object error. By this point in the down chain, 000008's
--      down has already restored these two policies to exactly the shape
--      000003 created (see 000008_..._scope.down.sql), so dropping them
--      here reverses precisely what this migration added.
--
--   2. The memberships table (its indexes and memberships_read/write
--      policies are owned by the table and dropped automatically with it).
--      By this point, 000007's down has already restored memberships_read
--      to its pre-000007 shape, i.e. exactly what this migration created.

DROP POLICY IF EXISTS persons_select ON persons;
DROP POLICY IF EXISTS persons_update ON persons;

DROP TABLE IF EXISTS memberships;
