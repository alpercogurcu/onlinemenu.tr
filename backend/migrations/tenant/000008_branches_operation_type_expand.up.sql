-- Migration: tenant/000008_branches_operation_type_expand
--
-- Pilot işletmeleri restoran dışında kafe, fast food ve bulut mutfak olarak da
-- şube açıyor; admin panelin "Şube Ekle" formu bu üç değeri zaten sunuyordu ama
-- CHECK kısıtı (000002) yalnız altı değere izin verdiği için kayıt 500 ile
-- düşüyordu. Kısıt genişletilir; mevcut satırlar etkilenmez.
--
-- Değer adları snake_case ve mevcut food_truck ile tutarlı: fast_food,
-- bulut_mutfak. Aynı liste tenant/public.OperationType.Valid() içinde tutulur.

ALTER TABLE branches
    DROP CONSTRAINT branches_operation_type_check,
    ADD CONSTRAINT branches_operation_type_check CHECK (operation_type IN (
        'restoran', 'kafe', 'fast_food', 'bulut_mutfak',
        'bar', 'market', 'food_truck', 'imalat', 'depo'
    ));
