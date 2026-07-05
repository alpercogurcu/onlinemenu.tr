-- Migration: hr-core/000002_drop_cross_module_fk
-- Modül izolasyonu — cross-module FK yasak; bütünlük RLS + servis katmanı +
-- OPS-002 offboarding ile korunur (karar: task #14, 2026-07-05).
--
-- 000001 shipped employee_profiles.person_id with an inline FK to identity's
-- persons(id) table. persons belongs to the identity module, not hr-core, so
-- this is a cross-module DB coupling forbidden by CLAUDE.md's module
-- isolation rule (same rationale as catalog/000002 and identity/000011:
-- each module's migrations run independently in their own
-- schema_migrations_<module> chain and must not assume another module's
-- tables exist in the same database session/deployment order).
--
-- Behavioral note: the constraint was ON DELETE RESTRICT, so it previously
-- blocked deleting a person who has an employee_profiles row. After this
-- migration that guard no longer exists at the DB level; the hr-core
-- service layer (via identity's public PersonReader/service, not SQL) is
-- responsible for referential integrity to persons(id) going forward.

SET LOCAL role = app_migrator;

ALTER TABLE employee_profiles DROP CONSTRAINT IF EXISTS employee_profiles_person_id_fkey;
