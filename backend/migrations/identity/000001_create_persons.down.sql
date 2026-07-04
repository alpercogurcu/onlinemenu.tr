-- Migration: identity/000001_create_persons (rollback)
-- Drops the persons table; its two unique indexes (persons_keycloak_sub_idx,
-- persons_email_idx), RLS enable/force flags and the persons_insert policy
-- are all owned by the table and are dropped automatically with it.
--
-- By the time this runs in the down chain, memberships (000003) has already
-- been dropped by its own down migration (down order: 000010 -> ... ->
-- 000003 -> 000002 -> 000001), so the memberships.person_id FK does not
-- block this drop.

DROP TABLE IF EXISTS persons;
