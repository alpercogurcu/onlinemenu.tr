#!/usr/bin/env bash
# Offsite copy of the postgres-backup sidecar's local dumps to Google Drive.
#
# Why: BACKUP_S3_* is empty on this host (no S3 target), so dumps only live in
# the postgres-backups named volume — a host loss would take the backups with
# it. The b2b project already ships its dumps to the same Drive account through
# rclone's "gdrive:" remote (/root/.config/rclone/rclone.conf); reusing it
# avoids a second credential. Retention on Drive is independent of the
# sidecar's local BACKUP_RETENTION_DAYS.
#
# Runs from root's crontab on the server (installed 2026-09-15):
#   45 3 * * * /opt/onlinemenu/deploy/scripts/offsite-backup.sh >> /var/log/onlinemenu-offsite.log 2>&1
#
# Env overrides: OFFSITE_REMOTE (default gdrive:onlinemenu-backups),
#                OFFSITE_KEEP_DAYS (default 30).
set -uo pipefail

REMOTE="${OFFSITE_REMOTE:-gdrive:onlinemenu-backups}"
KEEP_DAYS="${OFFSITE_KEEP_DAYS:-30}"
VOLUME="onlinemenu-prod_postgres-backups"

log() { echo "[offsite] $(date -u +%Y-%m-%dT%H:%M:%SZ) $*"; }

command -v rclone >/dev/null 2>&1 || { log "ERROR: rclone not installed"; exit 1; }
rclone listremotes | grep -q "^${REMOTE%%:*}:$" || { log "ERROR: rclone remote '${REMOTE%%:*}:' not configured"; exit 1; }

SRC="$(docker volume inspect "$VOLUME" -f '{{.Mountpoint}}' 2>/dev/null)" \
  || { log "ERROR: volume $VOLUME not found"; exit 1; }

# Empty dump directories are left behind by aborted sidecar runs (e.g. the
# RLS-trap guard before the first tenant existed); rclone skips them because
# --create-empty-src-dirs is not given.
if ! rclone copy "$SRC" "$REMOTE" --transfers 4 --stats-one-line --stats 0; then
  log "ERROR: rclone copy failed — Drive copy is STALE"
  exit 1
fi

# Retention: delete files older than KEEP_DAYS, then prune the emptied
# per-run directories. Failures here are logged but do not mask a successful
# copy (retention drift is recoverable, a missing copy is not).
rclone delete "$REMOTE" --min-age "${KEEP_DAYS}d" || log "WARN: retention delete failed"
rclone rmdirs "$REMOTE" --leave-root || log "WARN: rmdirs failed"

log "OK $(rclone size "$REMOTE" 2>/dev/null | tr '\n' ' ') (retention ${KEEP_DAYS}d) → $REMOTE"
