-- =============================================================================
-- Online Menu — PostgreSQL Init Script
-- ADR-SEC-001: pgBouncer transaction mode + SET LOCAL
-- ADR-SEC-002: FORCE RLS + iki ayrı rol (app_migrator, app_runtime)
-- =============================================================================

-- Keycloak için ayrı veritabanı
CREATE DATABASE keycloak;

-- Uygulama rolleri (ADR-SEC-002)
-- app_migrator: migration sahipliği, DDL yetkisi
CREATE ROLE app_migrator WITH LOGIN PASSWORD 'migrator_dev_password' CREATEDB;
-- app_runtime: DML only, RLS zorunlu (SET LOCAL app.tenant_id ile)
CREATE ROLE app_runtime WITH LOGIN PASSWORD 'runtime_dev_password';

-- onlinemenu_dev için bağlantı yetkileri
GRANT CONNECT ON DATABASE onlinemenu_dev TO app_migrator;
GRANT CONNECT ON DATABASE onlinemenu_dev TO app_runtime;

-- onlinemenu_dev bağlamında schema yetkileri
\c onlinemenu_dev

-- pgvector extension (embedding / semantic search hazırlığı)
CREATE EXTENSION IF NOT EXISTS vector;
-- uuid_generate_v4 kullanımı için (alternatif: gen_random_uuid() built-in PG14+)
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
-- pg_trgm: LIKE sorgularında GIN index desteği
CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- app_runtime için public schema erişimi
GRANT USAGE ON SCHEMA public TO app_runtime;

-- app_migrator migration'larından sonra otomatik grant (DEFAULT PRIVILEGES)
-- app_migrator tarafından oluşturulan nesnelere app_runtime erişimi
ALTER DEFAULT PRIVILEGES FOR ROLE app_migrator IN SCHEMA public
    GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO app_runtime;

ALTER DEFAULT PRIVILEGES FOR ROLE app_migrator IN SCHEMA public
    GRANT USAGE ON SEQUENCES TO app_runtime;

-- app_migrator schema ownership
ALTER SCHEMA public OWNER TO app_migrator;
GRANT ALL ON SCHEMA public TO app_migrator;

-- Keycloak DB için postgres kullanıcısına tam yetki (Keycloak kendi şemasını yönetir)
\c keycloak
GRANT ALL PRIVILEGES ON DATABASE keycloak TO postgres;
