-- Migration: hr-core/000002_drop_cross_module_fk (rollback)
-- Re-adds employee_profiles.person_id's FK to persons(id) exactly as 000001
-- originally created it.

SET LOCAL role = app_migrator;

ALTER TABLE employee_profiles
    ADD CONSTRAINT employee_profiles_person_id_fkey FOREIGN KEY (person_id) REFERENCES persons(id) ON DELETE RESTRICT;
