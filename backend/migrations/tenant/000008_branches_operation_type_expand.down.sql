-- Migration: tenant/000008_branches_operation_type_expand (rollback)
--
-- Kısıtı 000002'deki altı değere geri daraltır. kafe / fast_food /
-- bulut_mutfak kullanan bir şube varsa ADD CONSTRAINT bilerek başarısız olur:
-- satırı sessizce başka bir işletme türüne çevirmek veri kaybı olurdu, önce
-- ilgili şubelerin türü elle düzeltilmeli.

ALTER TABLE branches
    DROP CONSTRAINT branches_operation_type_check,
    ADD CONSTRAINT branches_operation_type_check CHECK (operation_type IN (
        'restoran', 'bar', 'market',
        'food_truck', 'imalat', 'depo'
    ));
