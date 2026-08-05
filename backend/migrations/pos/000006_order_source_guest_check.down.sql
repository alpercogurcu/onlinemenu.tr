-- DİKKAT: bu rollback YIKICIDIR. opened_by NOT NULL'a geri dönebilmek için
-- misafir kaynaklı adisyonlar ve bağlı siparişler silinir; bu veriye
-- ihtiyaç varsa rollback öncesi yedek alın (deploy/backup/backup.sh).
DELETE FROM order_items WHERE order_id IN (
    SELECT id FROM orders WHERE check_id IN (
        SELECT id FROM checks WHERE opened_by_kind = 'guest_qr'));
DELETE FROM orders WHERE check_id IN (
    SELECT id FROM checks WHERE opened_by_kind = 'guest_qr');
DELETE FROM checks WHERE opened_by_kind = 'guest_qr';

ALTER TABLE checks DROP CONSTRAINT checks_opened_by_kind_chk;
ALTER TABLE checks ALTER COLUMN opened_by SET NOT NULL;
ALTER TABLE checks DROP COLUMN opened_by_kind;
ALTER TABLE checks DROP COLUMN source;
DROP INDEX orders_source_idx;
ALTER TABLE orders DROP COLUMN source;
