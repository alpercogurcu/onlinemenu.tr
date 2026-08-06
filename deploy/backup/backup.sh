#!/bin/sh
# ADR-OPS-001 — logical backup of the two Postgres databases (app + Keycloak)
# plus cluster globals (roles/grants). Runs inside the postgres-backup sidecar
# (see deploy/docker-compose.prod.yml). POSIX sh — the postgres:*-alpine base
# image has no bash.
set -eu

: "${PGHOST:?PGHOST is required}"
: "${PGPORT:=5432}"
# Must be the cluster superuser (`postgres` in this repo's init.sql — see
# deploy/postgres/init.sql), not app_runtime and not app_migrator:
#   - app_runtime has no BYPASSRLS: FORCE ROW LEVEL SECURITY (ADR-SEC-002)
#     makes a dump under this role succeed with exit 0 and silently empty
#     tenant-scoped tables, because it has no app.tenant_id set outside a
#     request-scoped SET LOCAL. That failure mode is exactly what the sanity
#     check below exists to catch.
#   - app_migrator has BYPASSRLS and would dump the app DB fine, but has no
#     grant on the `keycloak` database (owned by `postgres`, see init.sql) —
#     pg_dump on it fails outright with "permission denied for table
#     databasechangeloglock". One role needs to read both databases.
# A real superuser bypasses every permission check, including this one.
: "${BACKUP_SUPERUSER:?BACKUP_SUPERUSER is required (the cluster superuser — must read both databases and bypass RLS)}"
: "${BACKUP_SUPERUSER_PASSWORD:?BACKUP_SUPERUSER_PASSWORD is required}"
: "${APP_DB:=onlinemenu}"
: "${KEYCLOAK_DB:=keycloak}"
: "${BACKUP_DIR:=/backups}"
: "${RETENTION_DAYS:=14}"
# A table in APP_DB that must always hold at least one row for an onboarded
# pilot tenant. Used purely as a role-visibility sanity check, not a data
# integrity check.
: "${BACKUP_SANITY_TABLE:=tenants}"

ts="$(date -u +%Y%m%dT%H%M%SZ)"
outdir="${BACKUP_DIR}/${ts}"
mkdir -p "$outdir"

export PGPASSWORD="$BACKUP_SUPERUSER_PASSWORD"

log() { echo "[backup] $(date -u +%H:%M:%SZ) $*"; }

log "run ${ts} starting (host=${PGHOST}:${PGPORT} role=${BACKUP_SUPERUSER})"

# --- 1. RLS-bypass sanity check (see comment on BACKUP_SUPERUSER above) ---
row_count="$(psql -h "$PGHOST" -p "$PGPORT" -U "$BACKUP_SUPERUSER" -d "$APP_DB" -tAc \
  "SELECT count(*) FROM ${BACKUP_SANITY_TABLE}" | tr -d '[:space:]')"
if [ "$row_count" = "0" ] || [ -z "$row_count" ]; then
  log "ABORT: ${APP_DB}.${BACKUP_SANITY_TABLE} returned 0 rows for role '${BACKUP_SUPERUSER}'."
  log "This is the FORCE RLS trap (ADR-SEC-002): a role without BYPASSRLS and no"
  log "app.tenant_id set produces an empty-but-'successful' dump. Refusing to write"
  log "a backup that looks fine and restores empty. Check BACKUP_SUPERUSER."
  exit 1
fi
log "sanity check OK: ${row_count} row(s) visible in ${APP_DB}.${BACKUP_SANITY_TABLE}"

# --- 2. Dump both databases (custom format: compressed, pg_restore-selective) ---
log "dumping ${APP_DB} -> ${outdir}/${APP_DB}.dump"
pg_dump -h "$PGHOST" -p "$PGPORT" -U "$BACKUP_SUPERUSER" -d "$APP_DB" -Fc -f "${outdir}/${APP_DB}.dump"

log "dumping ${KEYCLOAK_DB} -> ${outdir}/${KEYCLOAK_DB}.dump"
pg_dump -h "$PGHOST" -p "$PGPORT" -U "$BACKUP_SUPERUSER" -d "$KEYCLOAK_DB" -Fc -f "${outdir}/${KEYCLOAK_DB}.dump"

# --- 3. Cluster globals (roles, GRANTs — app_migrator/app_runtime are cluster-
# level, not part of any single database dump) ---
log "dumping cluster globals -> ${outdir}/globals.sql"
pg_dumpall -h "$PGHOST" -p "$PGPORT" -U "$BACKUP_SUPERUSER" --globals-only -f "${outdir}/globals.sql"

# --- 4. Non-emptiness / truncation sanity on the files themselves ---
for f in "${outdir}/${APP_DB}.dump" "${outdir}/${KEYCLOAK_DB}.dump" "${outdir}/globals.sql"; do
  size="$(stat -c%s "$f" 2>/dev/null || stat -f%z "$f")"
  if [ "${size:-0}" -lt 100 ]; then
    log "ABORT: $f is suspiciously small (${size:-0} bytes) — treating as a failed dump"
    exit 1
  fi
done
log "local dump complete: ${outdir} ($(du -sh "$outdir" | cut -f1))"

# --- 5. Optional S3-compatible upload. No provider assumed: any S3-compatible
# endpoint works via mc (MinIO Client), configured entirely through env vars.
# Unset BACKUP_S3_* -> local-only backup, which is the default. ---
if [ -n "${BACKUP_S3_ENDPOINT:-}" ] && [ -n "${BACKUP_S3_BUCKET:-}" ]; then
  if ! command -v mc >/dev/null 2>&1; then
    log "BACKUP_S3_* is set but mc is not installed (see entrypoint.sh) — skipping upload"
  else
    mc alias set backup-target "$BACKUP_S3_ENDPOINT" "$BACKUP_S3_ACCESS_KEY" "$BACKUP_S3_SECRET_KEY" >/dev/null
    mc cp -r "$outdir" "backup-target/${BACKUP_S3_BUCKET}/$(basename "$outdir")" >/dev/null
    log "uploaded to s3://${BACKUP_S3_BUCKET}/$(basename "$outdir")"
  fi
else
  log "BACKUP_S3_* not configured — local-only backup (default; see README.md)"
fi

# --- 6. Retention: local dumps only. S3 lifecycle rules (if any) are the
# bucket owner's responsibility — this script does not manage remote retention. ---
log "applying local retention: deleting dump directories older than ${RETENTION_DAYS} days"
find "$BACKUP_DIR" -mindepth 1 -maxdepth 1 -type d -mtime "+${RETENTION_DAYS}" -print -exec rm -rf {} \;

log "run ${ts} done"
