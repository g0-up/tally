#!/usr/bin/env bash
# wal-g sidecar of the production database.
#
#   walg-sidecar run      prepare the volumes, then loop: push spooled WAL every WALG_PUSH_INTERVAL
#                         seconds and take a base backup daily at BACKUP_BASE_TIME (container TZ),
#                         keeping BACKUP_RETAIN_FULL full backups and the WAL they need
#   walg-sidecar health   exit 1 when WAL waits too long, the archiver fails or the base backup is stale
#   walg-sidecar push     push the spooled WAL once
#   walg-sidecar base     take a base backup now (and apply the retention)
#   walg-sidecar list     list the base backups in storage
#
# postgres' archive_command (infra/postgres/postgresql.prod.conf) copies each finished segment
# into the spool; this process uploads it with `wal-g wal-push` and deletes it afterwards. Storage
# and encryption come from the WALG_* / AWS_* environment (infra/docker-compose.backup.yml).
set -euo pipefail

SPOOL=${WALG_SPOOL:-/var/lib/postgresql/wal-spool}
STATE=${WALG_STATE_DIR:-/var/lib/walg}
PGDATA=${PGDATA:-/var/lib/postgresql/data}
BASE_TIME=${BACKUP_BASE_TIME:-02:30}
RETAIN=${BACKUP_RETAIN_FULL:-7}
PUSH_INTERVAL=${WALG_PUSH_INTERVAL:-30}
MAX_WAL_AGE=${WALG_MAX_WAL_AGE:-900}
MAX_BASE_AGE=${WALG_MAX_BASE_AGE:-93600}
FIRST_BASE_GRACE=${WALG_FIRST_BASE_GRACE:-21600}
PG_UID=${PG_UID:-70}
PG_GID=${PG_GID:-70}

log() {
  printf '%s walg-sidecar: %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*" >&2
}

die() {
  log "$*"
  exit 1
}

# An empty key would make wal-g try to encrypt with it; unset means no client-side encryption.
if [[ -z "${WALG_LIBSODIUM_KEY:-}" ]]; then
  unset WALG_LIBSODIUM_KEY WALG_LIBSODIUM_KEY_TRANSFORM
fi

validate() {
  [[ "$BASE_TIME" =~ ^([01][0-9]|2[0-3]):[0-5][0-9]$ ]] || die "BACKUP_BASE_TIME must be HH:MM, got '$BASE_TIME'"
  [[ "$RETAIN" =~ ^[1-9][0-9]*$ ]] || die "BACKUP_RETAIN_FULL must be a positive integer, got '$RETAIN'"
  [[ "$PUSH_INTERVAL" =~ ^[1-9][0-9]*$ ]] || die "WALG_PUSH_INTERVAL must be a positive integer"
  [[ -n "${WALG_S3_PREFIX:-}${WALG_FILE_PREFIX:-}" ]] || die "set WALG_S3_PREFIX (or WALG_FILE_PREFIX for a local drill)"
}

# as_postgres re-runs this script with the uid/gid that owns the data volume when started as root,
# so every file written to the spool and state volumes keeps one owner.
as_postgres() {
  if [[ "$(id -u)" == "0" ]]; then
    exec setpriv --reuid="$PG_UID" --regid="$PG_GID" --clear-groups -- "$0" "$@"
  fi
}

prepare() {
  install -d -m 0700 -o "$PG_UID" -g "$PG_GID" "$SPOOL" "$STATE"
  chown "$PG_UID:$PG_GID" "$SPOOL" "$STATE"
  chmod 0700 "$SPOOL" "$STATE"
}

wait_for_postgres() {
  until pg_isready -q; do
    log "waiting for postgres at ${PGHOST:-local socket}"
    sleep 5
  done
}

# push_spool uploads the spooled files oldest name first (segment names sort in WAL order) and
# stops at the first failure so nothing is skipped.
push_spool() {
  local f
  while IFS= read -r -d '' f; do
    if wal-g wal-push "$f"; then
      rm -f -- "$f"
      date +%s >"$STATE/last-wal-push"
    else
      log "wal-push failed for $(basename "$f"); retrying in the next round"
      return 1
    fi
  done < <(find "$SPOOL" -maxdepth 1 -type f ! -name '*.part' -print0 | sort -z)
  # A copy torn by a postgres crash is retried under the same name; drop the stale leftovers.
  find "$SPOOL" -maxdepth 1 -type f -name '*.part' -mmin +60 -delete
}

base_backup() {
  log "base backup starting"
  if ! wal-g backup-push "$PGDATA"; then
    log "base backup failed"
    return 1
  fi
  date +%s >"$STATE/last-base"
  log "base backup done; keeping the last $RETAIN full backups"
  wal-g delete retain FULL "$RETAIN" --confirm || log "retention failed; old backups are kept"
}

next_base_at() {
  local t
  t=$(date -d "today $BASE_TIME" +%s)
  if ((t <= $(date +%s))); then
    t=$(date -d "tomorrow $BASE_TIME" +%s)
  fi
  echo "$t"
}

run_loop() {
  validate
  wait_for_postgres
  date +%s >"$STATE/started"
  local next
  if [[ -f "$STATE/last-base" ]]; then
    next=$(next_base_at)
  else
    next=0
  fi
  trap 'log "stopping"; exit 0' TERM INT
  log "running: push every ${PUSH_INTERVAL}s, base backup daily at $BASE_TIME ($(date +%Z)), retain $RETAIN full"
  while true; do
    push_spool || true
    if (($(date +%s) >= next)); then
      if base_backup; then
        next=$(next_base_at)
      else
        next=$(($(date +%s) + 1800))
      fi
    fi
    sleep "$PUSH_INTERVAL" &
    wait $! || true
  done
}

health() {
  local now oldest age failing ready started base
  now=$(date +%s)
  oldest=$(find "$SPOOL" -maxdepth 1 -type f ! -name '*.part' -printf '%T@\n' 2>/dev/null | sort -n | head -n 1)
  if [[ -n "$oldest" ]]; then
    age=$((now - ${oldest%.*}))
    if ((age > MAX_WAL_AGE)); then
      echo "WAL has waited ${age}s in the spool (limit ${MAX_WAL_AGE}s): uploads are failing"
      return 1
    fi
  fi
  ready=$(find "$PGDATA/pg_wal/archive_status" -maxdepth 1 -name '*.ready' -mmin +$((MAX_WAL_AGE / 60)) 2>/dev/null | head -n 1)
  if [[ -n "$ready" ]]; then
    echo "postgres holds WAL not archived for over ${MAX_WAL_AGE}s: archive_command is failing"
    return 1
  fi
  if ! failing=$(psql -XAtq -c "SELECT coalesce(last_failed_time > coalesce(last_archived_time, '-infinity'), false) FROM pg_stat_archiver" 2>&1); then
    echo "cannot read pg_stat_archiver: $failing"
    return 1
  fi
  if [[ "$failing" == "t" ]]; then
    echo "the last archive_command failed after the last success"
    return 1
  fi
  if [[ -f "$STATE/last-base" ]]; then
    base=$(<"$STATE/last-base")
    if ((now - base > MAX_BASE_AGE)); then
      echo "the last base backup is $(((now - base) / 3600))h old (limit $((MAX_BASE_AGE / 3600))h)"
      return 1
    fi
  else
    started=$now
    if [[ -f "$STATE/started" ]]; then
      started=$(<"$STATE/started")
    fi
    if ((now - started > FIRST_BASE_GRACE)); then
      echo "no base backup since the sidecar started $(((now - started) / 3600))h ago"
      return 1
    fi
  fi
  echo "ok"
}

cmd=${1:-run}
case "$cmd" in
  run)
    if [[ "$(id -u)" == "0" ]]; then
      prepare
    fi
    as_postgres run
    run_loop
    ;;
  health)
    health
    ;;
  push)
    as_postgres push
    validate
    push_spool
    ;;
  base)
    as_postgres base
    validate
    wait_for_postgres
    base_backup
    ;;
  list)
    as_postgres list
    wal-g backup-list --detail
    ;;
  -h | --help | help)
    sed -n '2,13p' "$0" | sed 's/^# \{0,1\}//'
    ;;
  *)
    die "unknown command '$cmd' (run, health, push, base, list)"
    ;;
esac
