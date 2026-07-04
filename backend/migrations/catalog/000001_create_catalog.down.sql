-- Migration: catalog/000001_create_catalog (rollback)
-- Drops tables in reverse FK-dependency order (children before parents):
--   product_channel_availability -> products
--   menu_items                   -> menus, products
--   menus                        (independent)
--   product_modifier_groups      -> products, modifier_groups
--   modifiers                    -> modifier_groups
--   modifier_groups              (independent)
--   products                     -> categories
--   categories                   (self-referencing parent_id; DROP TABLE
--                                  handles the self-FK without issue)
--
-- Indexes and RLS policies are owned by their tables and dropped
-- automatically with them. By the time this runs, catalog/000002's down has
-- already dropped the source_stock_item_id column it added to products, but
-- DROP TABLE products would remove it regardless.

DROP TABLE IF EXISTS product_channel_availability;
DROP TABLE IF EXISTS menu_items;
DROP TABLE IF EXISTS menus;
DROP TABLE IF EXISTS product_modifier_groups;
DROP TABLE IF EXISTS modifiers;
DROP TABLE IF EXISTS modifier_groups;
DROP TABLE IF EXISTS products;
DROP TABLE IF EXISTS categories;
