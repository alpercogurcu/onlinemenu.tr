#!/bin/sh
# Scheduled runner for backup.sh — the ONE scheduler for ADR-OPS-001 (an
# in-compose sidecar loop, not host cron; do not add a second scheduler).
# POSIX sh — the postgres:*-alpine base image has no bash.
set -eu

MC_VERSION="RELEASE.2025-08-13T08-35-41Z"
MC_SHA256="01f866e9c5f9b87c2b09116fa5d7c06695b106242d829a8bb32990c00312e891"
MC_BIN="${MC_BIN_PATH:-/opt/tools/mc}"

log() { echo "[entrypoint] $(date -u +%H:%M:%SZ) $*"; }

# mc is fetched once into a named volume (see docker-compose.prod.yml) so a
# container restart does not require network egress to dl.min.io again.
# Version and checksum are pinned — never "latest".
if [ ! -x "$MC_BIN" ]; then
  log "fetching mc ${MC_VERSION} (pinned + checksum-verified)..."
  mkdir -p "$(dirname "$MC_BIN")"
  wget -q -O "$MC_BIN" "https://dl.min.io/client/mc/release/linux-amd64/archive/mc.${MC_VERSION}"
  echo "${MC_SHA256}  ${MC_BIN}" | sha256sum -c -
  chmod +x "$MC_BIN"
  log "mc installed at ${MC_BIN}"
fi
export PATH="$(dirname "$MC_BIN"):${PATH}"

: "${BACKUP_INTERVAL_SECONDS:=21600}" # 6h default — see README.md for the RPO this implies vs ADR-OPS-001's target

log "postgres-backup sidecar starting. Interval: ${BACKUP_INTERVAL_SECONDS}s (see README.md for the RPO this implies)"

# Run once immediately on startup, then on the configured interval. A failed
# run logs loudly and is retried on the next tick — it does not crash the
# loop, so a transient DB hiccup doesn't take backups offline entirely.
while true; do
  if ! /scripts/backup.sh; then
    log "backup run FAILED — see output above. Will retry in ${BACKUP_INTERVAL_SECONDS}s."
  fi
  sleep "$BACKUP_INTERVAL_SECONDS"
done
