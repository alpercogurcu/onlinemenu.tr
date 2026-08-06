#!/bin/sh
# Restore procedure for a backup.sh dump directory (ADR-OPS-001).
#
# Intended targets:
#   - A throwaway Postgres instance for a restore drill (this is how this
#     script was verified — see README.md "Test edilen restore").
#   - A real disaster-recovery restore, onto a freshly initialised cluster.
#
# This script does NOT touch a running production cluster's live databases in
# place: it creates <APP_DB> and <KEYCLOAK_DB> only if they do not already
# exist, and refuses to run against a database that already has the sanity
# table populated, so it cannot be pointed at a live system by mistake.
#
# POSIX sh — no bash assumed.
set -eu

: "${PGHOST:?PGHOST is required}"
: "${PGPORT:=5432}"
: "${BACKUP_SUPERUSER:?BACKUP_SUPERUSER is required (must be able to CREATE DATABASE / CREATE ROLE)}"
: "${BACKUP_SUPERUSER_PASSWORD:?BACKUP_SUPERUSER_PASSWORD is required}"
: "${APP_DB:=onlinemenu}"
: "${KEYCLOAK_DB:=keycloak}"
: "${DUMP_DIR:?DUMP_DIR is required — the timestamped directory backup.sh produced, e.g. /backups/20260803T120000Z}"
: "${BACKUP_SANITY_TABLE:=tenants}"

export PGPASSWORD="$BACKUP_SUPERUSER_PASSWORD"

log() { echo "[restore] $(date -u +%H:%M:%SZ) $*"; }

app_dump="${DUMP_DIR}/${APP_DB}.dump"
kc_dump="${DUMP_DIR}/${KEYCLOAK_DB}.dump"
globals_dump="${DUMP_DIR}/globals.sql"

for f in "$app_dump" "$kc_dump" "$globals_dump"; do
  [ -f "$f" ] || { log "ABORT: missing $f"; exit 1; }
done

# --- Safety guard: refuse to run against a database that clearly already has
# live data under the same name, so this can't be pointed at production by
# accident. ---
existing="$(psql -h "$PGHOST" -p "$PGPORT" -U "$BACKUP_SUPERUSER" -d postgres -tAc \
  "SELECT 1 FROM pg_database WHERE datname = '${APP_DB}'" || true)"
if [ "$existing" = "1" ]; then
  count="$(psql -h "$PGHOST" -p "$PGPORT" -U "$BACKUP_SUPERUSER" -d "$APP_DB" -tAc \
    "SELECT count(*) FROM ${BACKUP_SANITY_TABLE}" 2>/dev/null || echo "0")"
  if [ "${count:-0}" != "0" ]; then
    log "ABORT: database '${APP_DB}' already exists and ${BACKUP_SANITY_TABLE} has ${count} row(s)."
    log "This script only restores into an empty/absent database. Point it at a throwaway target."
    exit 1
  fi
fi

# --- 1. Cluster globals first: app_migrator / app_runtime roles + grants
# (ADR-SEC-002). A fresh cluster has neither role, so pg_restore of the
# per-database dumps would fail on ownership/grants without this step. ---
log "restoring cluster globals from ${globals_dump}"
psql -h "$PGHOST" -p "$PGPORT" -U "$BACKUP_SUPERUSER" -d postgres -v ON_ERROR_STOP=0 -f "$globals_dump" >/dev/null

# --- 2. Create target databases if absent ---
for db in "$APP_DB" "$KEYCLOAK_DB"; do
  present="$(psql -h "$PGHOST" -p "$PGPORT" -U "$BACKUP_SUPERUSER" -d postgres -tAc \
    "SELECT 1 FROM pg_database WHERE datname = '${db}'")"
  if [ "$present" != "1" ]; then
    log "creating database ${db}"
    psql -h "$PGHOST" -p "$PGPORT" -U "$BACKUP_SUPERUSER" -d postgres -c "CREATE DATABASE ${db}"
  fi
done

# --- 3. pg_restore each database ---
log "restoring ${APP_DB} from ${app_dump}"
pg_restore -h "$PGHOST" -p "$PGPORT" -U "$BACKUP_SUPERUSER" -d "$APP_DB" --no-owner --role="$BACKUP_SUPERUSER" "$app_dump"

log "restoring ${KEYCLOAK_DB} from ${kc_dump}"
pg_restore -h "$PGHOST" -p "$PGPORT" -U "$BACKUP_SUPERUSER" -d "$KEYCLOAK_DB" --no-owner --role="$BACKUP_SUPERUSER" "$kc_dump"

# --- 4. Verify: the whole point of testing a restore is to assert real data
# came back, not just that the commands exited 0. ---
count="$(psql -h "$PGHOST" -p "$PGPORT" -U "$BACKUP_SUPERUSER" -d "$APP_DB" -tAc \
  "SELECT count(*) FROM ${BACKUP_SANITY_TABLE}" | tr -d '[:space:]')"
if [ "${count:-0}" = "0" ]; then
  log "ABORT: restore completed but ${APP_DB}.${BACKUP_SANITY_TABLE} has 0 rows — restore is NOT verified good."
  exit 1
fi
log "verified: ${APP_DB}.${BACKUP_SANITY_TABLE} has ${count} row(s) after restore"
log "restore complete and verified."
