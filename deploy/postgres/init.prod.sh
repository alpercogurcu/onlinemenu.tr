#!/bin/bash
# Online Menu — Postgres bootstrap for PRODUCTION (docker-compose.prod.yml).
#
# Deliberately NOT a copy-paste of init.sql (the dev-only script, mounted by
# docker-compose.dev.yml): that file hardcodes both the "onlinemenu_dev"
# database name (via `\c onlinemenu_dev` and two literal
# `GRANT CONNECT ON DATABASE onlinemenu_dev`) and two literal dev passwords in
# plain SQL. Neither can vary per environment, because Postgres's
# docker-entrypoint only performs env-var interpolation for *.sh init scripts,
# not *.sql ones — a *.sql file is fed to psql byte-for-byte. Trying to reuse
# init.sql for prod with a differently-named POSTGRES_DB was tried and fails
# outright (GRANT CONNECT ON DATABASE onlinemenu_dev errors: database does not
# exist). This script is the *.sh equivalent: it reads POSTGRES_DB from the
# environment the postgres image already sets, and reads role passwords from
# bootstrap-class secrets (.env.sops) instead of hardcoding them.
set -euo pipefail

: "${APP_MIGRATOR_PASSWORD:?APP_MIGRATOR_PASSWORD is required}"
: "${APP_RUNTIME_PASSWORD:?APP_RUNTIME_PASSWORD is required}"

# docker-entrypoint-initdb.d scripts run after POSTGRES_DB already exists;
# POSTGRES_USER/POSTGRES_DB are exported by the postgres image itself.
psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<-EOSQL
  -- Keycloak için ayrı veritabanı
  CREATE DATABASE keycloak;

  -- Uygulama rolleri (ADR-SEC-002)
  -- app_migrator: migration sahipliği, DDL yetkisi + BYPASSRLS (sistem seed verisi için).
  -- BYPASSRLS superuser olmadan RLS'yi atlar; tenant_id=NULL sistem verisini yazabilmek için gerekli.
  CREATE ROLE app_migrator WITH LOGIN PASSWORD '$APP_MIGRATOR_PASSWORD' CREATEDB BYPASSRLS;
  -- app_runtime: DML only, RLS zorunlu (SET LOCAL app.tenant_id ile)
  CREATE ROLE app_runtime WITH LOGIN PASSWORD '$APP_RUNTIME_PASSWORD';

  -- \$POSTGRES_DB için bağlantı yetkileri (bu script zaten o veritabanına bağlı çalışıyor)
  GRANT CONNECT ON DATABASE "$POSTGRES_DB" TO app_migrator;
  GRANT CONNECT ON DATABASE "$POSTGRES_DB" TO app_runtime;

  -- pgvector extension (embedding / semantic search hazırlığı)
  CREATE EXTENSION IF NOT EXISTS vector;
  -- uuid_generate_v4 kullanımı için (alternatif: gen_random_uuid() built-in PG14+)
  CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
  -- pg_trgm: LIKE sorgularında GIN index desteği
  CREATE EXTENSION IF NOT EXISTS pg_trgm;

  -- app_runtime için public schema erişimi
  GRANT USAGE ON SCHEMA public TO app_runtime;

  -- app_migrator migration'larından sonra otomatik grant (DEFAULT PRIVILEGES)
  ALTER DEFAULT PRIVILEGES FOR ROLE app_migrator IN SCHEMA public
      GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO app_runtime;

  ALTER DEFAULT PRIVILEGES FOR ROLE app_migrator IN SCHEMA public
      GRANT USAGE ON SEQUENCES TO app_runtime;

  -- app_migrator schema ownership
  ALTER SCHEMA public OWNER TO app_migrator;
  GRANT ALL ON SCHEMA public TO app_migrator;
EOSQL

# Keycloak DB için postgres kullanıcısına tam yetki (Keycloak kendi şemasını yönetir)
psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname keycloak <<-EOSQL
  GRANT ALL PRIVILEGES ON DATABASE keycloak TO postgres;
EOSQL
