DROP INDEX IF EXISTS checks_open_table_id_uidx;
DROP INDEX IF EXISTS checks_table_id_idx;
ALTER TABLE checks DROP COLUMN IF EXISTS table_id;
DROP TABLE IF EXISTS tables;
DROP TABLE IF EXISTS table_zones;
