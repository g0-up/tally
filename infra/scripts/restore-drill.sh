#!/usr/bin/env bash
# Measured restore drill: restores the wal-g backups of the production database into a throw-away
# scratch stack, measures the recovery point (RPO) and recovery time (RTO), runs `tally ledger
# verify` and `tally tenant restore-check` on the restored copy, and writes a timing report.
#
# Usage: infra/scripts/restore-drill.sh [options]
#
#   --target-time TIME    point-in-time recovery: stop replay at TIME (any `date -d` format, e.g.
#                         "2026-10-19 09:30:00+07"); default: replay every archived WAL segment
#   --incident-time TIME  when the primary is taken as lost, for the RPO (default: drill start)
#   --backup NAME         base backup to restore (default: the newest one finished before the
#                         target time, or the newest one)
#   --primary CONTAINER   running production postgres (e.g. tally-prod-postgres-1); read-only probe
#                         of its WAL position at the incident, to tell a quiet database from lost data
#   --tenant ID           run restore-check for this tenant only (repeatable; default: every tenant)
#   --env-file FILE       production settings (default: infra/.env)
#   --report-dir DIR      where the report and log go (default: test-results/restore-drill)
#   --db-only             restore and measure the database only (no API image needed); the drill
#                         is then reported as PARTIAL, never PASS
#   --keep                leave the scratch stack running for inspection (prints how to remove it)
#   -h, --help            show this help
#
# Environment: VERSION (image tag, default from the env file or "dev"), DRILL_WALG_IMAGE (default
# tally-walg:$VERSION), DRILL_API_IMAGE (default tally-api:$VERSION), DRILL_POSTGRES_IMAGE (default
# postgres:16-alpine), DRILL_WALG_ARGS (extra `docker run` arguments of the wal-g containers, e.g. a
# CA bundle mount), DRILL_RPO_LIMIT (seconds, default 900), DRILL_RTO_LIMIT (seconds, default 14400),
# DRILL_RECOVERY_TIMEOUT (seconds, default 14400).
#
# Safety: the scratch postgres and API run on an internal docker network of their own (no egress,
# so nothing is mailed or uploaded), the postgres alias tally-postgres resolves only there, WAL
# archiving is off on the scratch copy, and storage is only read. The images are never built here.
# Exit status: 0 drill passed (or PARTIAL with --db-only), 1 drill failed, 2 usage error.
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
CONF="$ROOT/infra/postgres/postgresql.prod.conf"
ENV_FILE="$ROOT/infra/.env"
REPORT_DIR="$ROOT/test-results/restore-drill"
TARGET_TIME=""
INCIDENT_TIME=""
BACKUP=""
PRIMARY=""
TENANTS=()
DB_ONLY=0
KEEP=0
RPO_LIMIT=${DRILL_RPO_LIMIT:-900}
RTO_LIMIT=${DRILL_RTO_LIMIT:-14400}
RECOVERY_TIMEOUT=${DRILL_RECOVERY_TIMEOUT:-14400}
WALIN=/var/lib/postgresql/wal-restore

usage() {
  awk 'NR > 1 && /^set -euo/ { exit } NR > 1 { sub(/^# ?/, ""); print }' "${BASH_SOURCE[0]}"
}

usage_error() {
  echo "restore-drill: $*" >&2
  echo "Run with --help for the options." >&2
  exit 2
}

while (($# > 0)); do
  case "$1" in
    --target-time) TARGET_TIME=${2:?--target-time needs a value}; shift 2 ;;
    --incident-time) INCIDENT_TIME=${2:?--incident-time needs a value}; shift 2 ;;
    --backup) BACKUP=${2:?--backup needs a value}; shift 2 ;;
    --primary) PRIMARY=${2:?--primary needs a value}; shift 2 ;;
    --tenant) TENANTS+=("${2:?--tenant needs a value}"); shift 2 ;;
    --env-file) ENV_FILE=${2:?--env-file needs a value}; shift 2 ;;
    --report-dir) REPORT_DIR=${2:?--report-dir needs a value}; shift 2 ;;
    --db-only) DB_ONLY=1; shift ;;
    --keep) KEEP=1; shift ;;
    -h | --help) usage; exit 0 ;;
    *) usage_error "unknown option '$1'" ;;
  esac
done

for n in "$RPO_LIMIT" "$RTO_LIMIT" "$RECOVERY_TIMEOUT"; do
  [[ "$n" =~ ^[1-9][0-9]*$ ]] || usage_error "DRILL_RPO_LIMIT, DRILL_RTO_LIMIT and DRILL_RECOVERY_TIMEOUT must be positive integers"
done
[[ -f "$ENV_FILE" ]] || usage_error "env file $ENV_FILE not found (copy infra/.env.example, or pass --env-file)"
[[ -f "$CONF" ]] || usage_error "$CONF not found"
command -v docker >/dev/null || usage_error "docker is required"

to_epoch() {
  date -d "$1" +%s 2>/dev/null || usage_error "cannot read the time '$1'"
}

iso() {
  date -u -d "@$1" +%Y-%m-%dT%H:%M:%SZ
}

# iso_or prints the epoch $1 as a UTC time, or $2 when $1 is empty.
iso_or() {
  if [[ -n "$1" ]]; then iso "$1"; else printf '%s' "$2"; fi
}

T0=$(date +%s)
INCIDENT=$T0
if [[ -n "$INCIDENT_TIME" ]]; then
  INCIDENT=$(to_epoch "$INCIDENT_TIME")
fi
TARGET=""
if [[ -n "$TARGET_TIME" ]]; then
  TARGET=$(to_epoch "$TARGET_TIME")
  ((TARGET <= T0)) || usage_error "--target-time is in the future"
fi
for t in "${TENANTS[@]}"; do
  [[ "$t" =~ ^[0-9a-fA-F-]{36}$ ]] || usage_error "--tenant takes a tenant id, got '$t'"
done

# env_value prints the last KEY=value of the env file, without surrounding quotes. The file is
# read, never sourced.
env_value() {
  local v
  v=$(sed -n "s/^$1=//p" "$ENV_FILE" | tail -n 1)
  if [[ "$v" =~ ^\"(.*)\"$ || "$v" =~ ^\'(.*)\'$ ]]; then
    v=${BASH_REMATCH[1]}
  fi
  printf '%s' "$v"
}

VERSION=${VERSION:-$(env_value VERSION)}
VERSION=${VERSION:-dev}
WALG_IMAGE=${DRILL_WALG_IMAGE:-tally-walg:$VERSION}
API_IMAGE=${DRILL_API_IMAGE:-tally-api:$VERSION}
PG_IMAGE=${DRILL_POSTGRES_IMAGE:-postgres:16-alpine}
read -r -a WALG_ARGS <<<"${DRILL_WALG_ARGS:-}"
DB_NAME=$(env_value POSTGRES_DB)
DB_NAME=${DB_NAME:-tally}

ID="tally-drill-$(date -u -d "@$T0" +%Y%m%d%H%M%S)"
NET="$ID-net"
PGVOL="$ID-pgdata"
WALVOL="$ID-walin"
mkdir -p "$REPORT_DIR"
REPORT="$REPORT_DIR/$ID.md"
LOG="$REPORT_DIR/$ID.log"
: >"$LOG"
TMP=$(mktemp -d)
chmod 0700 "$TMP"

log() {
  printf '%s %s\n' "$(date -u +%H:%M:%S)" "$*" | tee -a "$LOG" >&2
}

# --- state for the report ---
STEP="preparing"
RESULT=""
BACKUP_FINISH=""
SEGMENTS=0
MISSING=""
RECOVERED=""
REPLAY_LSN=""
PRIMARY_LSN=""
PRIMARY_ARCHIVED=""
LOST_BYTES=""
LEDGER_LINE=""
CHECKS_OK=0
CHECKS_FAILED=0
declare -A MARK=()

mark() {
  MARK[$1]=$(date +%s)
}

since() {
  if [[ -n "${MARK[$2]:-}" && -n "${MARK[$1]:-}" ]]; then
    echo "$((MARK[$2] - MARK[$1])) s"
  else
    echo "-"
  fi
}

write_env_files() {
  local k v
  # wal-g reads its storage and key from the same variables as the sidecar
  # (infra/docker-compose.backup.yml); the values never appear on a command line.
  v=$(env_value APP_TZ)
  {
    echo "TZ=${v:-Asia/Ho_Chi_Minh}"
    for k in WALG_S3_PREFIX WALG_FILE_PREFIX WALG_LIBSODIUM_KEY; do
      v=$(env_value "$k")
      [[ -z "$v" ]] || echo "$k=$v"
    done
    v=$(env_value WALG_S3_ENDPOINT); [[ -z "$v" ]] || echo "AWS_ENDPOINT=$v"
    v=$(env_value WALG_S3_REGION); echo "AWS_REGION=${v:-auto}"
    echo "AWS_S3_FORCE_PATH_STYLE=true"
    v=$(env_value WALG_S3_ACCESS_KEY); [[ -z "$v" ]] || echo "AWS_ACCESS_KEY_ID=$v"
    v=$(env_value WALG_S3_SECRET_KEY); [[ -z "$v" ]] || echo "AWS_SECRET_ACCESS_KEY=$v"
    [[ -z "$(env_value WALG_LIBSODIUM_KEY)" ]] || echo "WALG_LIBSODIUM_KEY_TRANSFORM=base64"
  } >"$TMP/walg.env"
  [[ -n "$(env_value WALG_S3_PREFIX)$(env_value WALG_FILE_PREFIX)" ]] || {
    log "set WALG_S3_PREFIX (or WALG_FILE_PREFIX) in $ENV_FILE"
    return 1
  }
  # The API gets the variables of x-app-env in infra/docker-compose.prod.yml, nothing else.
  {
    for k in APP_ENV APP_TZ LOG_LEVEL DOMAIN PUBLIC_BASE_URL DATABASE_URL SESSION_HASH_KEY CSRF_KEY \
      APP_ENCRYPTION_KEY SMTP_HOST SMTP_PORT SMTP_USER SMTP_PASSWORD MAIL_FROM OBJECT_STORE_ENDPOINT \
      OBJECT_STORE_REGION OBJECT_STORE_BUCKET OBJECT_STORE_ACCESS_KEY OBJECT_STORE_SECRET_KEY \
      TRUSTED_PROXY_CIDRS RESERVED_SLUGS PLATFORM_ADMIN_BOOTSTRAP_EMAIL; do
      v=$(env_value "$k")
      [[ -z "$v" ]] || echo "$k=$v"
    done
    [[ -n "$(env_value APP_ENV)" ]] || echo "APP_ENV=production"
    echo "MIGRATE_ON_START=false"
    # restore-check gets a URL without the password; pgx reads it from PGPASSWORD.
    echo "PGPASSWORD=$(scratch_password)"
  } >"$TMP/app.env"
}

# scratch_url is DATABASE_URL without its password (postgres://user:pass@host/db -> user@host/db).
scratch_url() {
  env_value DATABASE_URL | sed -E 's#^(postgres(ql)?://[^:/@]+):[^@]*@#\1@#'
}

scratch_password() {
  env_value DATABASE_URL | sed -nE 's#^postgres(ql)?://[^:/@]+:([^@]*)@.*#\2#p'
}

# walg [docker run options] -- SCRIPT [ARGS]: runs a bash script in a one-off wal-g container.
walg() {
  local -a opts=()
  while [[ "$1" != "--" ]]; do
    opts+=("$1")
    shift
  done
  shift
  docker run --rm --user 0 --env-file "$TMP/walg.env" "${WALG_ARGS[@]}" "${opts[@]}" --entrypoint bash "$WALG_IMAGE" -c "$@"
}

psql_scratch() {
  docker exec "$ID-pg" psql -X -A -t -q -v ON_ERROR_STOP=1 -U postgres -d "$DB_NAME" -c "$1"
}

check_images() {
  local img
  for img in "$WALG_IMAGE" "$@"; do
    if ! docker image inspect "$img" >/dev/null 2>&1; then
      log "image $img is not on this host; create it with the production images (make prod-up) first"
      return 1
    fi
  done
}

probe_primary() {
  local out
  if [[ -z "$PRIMARY" ]]; then
    return 0
  fi
  if ! out=$(docker exec "$PRIMARY" psql -X -A -t -q -F ' ' -U postgres -d "$DB_NAME" \
    -c "SELECT pg_current_wal_lsn(), coalesce(extract(epoch FROM last_archived_time)::bigint::text, '') FROM pg_stat_archiver" 2>>"$LOG"); then
    log "cannot read the WAL position of $PRIMARY; the drill continues without it"
    return 0
  fi
  read -r PRIMARY_LSN PRIMARY_ARCHIVED <<<"$out"
  if [[ ! "$PRIMARY_LSN" =~ ^[0-9A-F]+/[0-9A-F]+$ ]]; then
    log "unexpected WAL position '$PRIMARY_LSN' from $PRIMARY; the drill continues without it"
    PRIMARY_LSN=""
    return 0
  fi
  log "primary $PRIMARY at $PRIMARY_LSN, last segment archived at $(iso_or "${PRIMARY_ARCHIVED:-}" never)"
}

# choose_backup picks the newest base backup that finished before the target (or at all).
choose_backup() {
  local list name finish best="" best_finish=0 f header=1
  list=$(walg -- 'wal-g backup-list --detail' 2>>"$LOG") || {
    log "wal-g backup-list failed (storage settings or key?), see $LOG"
    return 1
  }
  while read -r name _ _ _ _ finish _; do
    if ((header)); then
      header=0
      continue
    fi
    [[ -n "$name" ]] || continue
    f=$(date -d "$finish" +%s) || continue
    if [[ -n "$BACKUP" ]]; then
      [[ "$name" == "$BACKUP" ]] || continue
    elif [[ -n "$TARGET" ]] && ((f > TARGET)); then
      continue
    fi
    if ((f >= best_finish)); then
      best=$name
      best_finish=$f
    fi
  done <<<"$list"
  if [[ -z "$best" ]]; then
    log "no base backup ${BACKUP:+named $BACKUP }${TARGET:+finished before $(iso "$TARGET") }in storage"
    return 1
  fi
  BACKUP=$best
  BACKUP_FINISH=$best_finish
  log "base backup $BACKUP (finished $(iso "$BACKUP_FINISH"))"
}

fetch_base() {
  docker volume create "$PGVOL" >/dev/null
  docker volume create "$WALVOL" >/dev/null
  # Root inside the container writes the data directory, then hands it to postgres (uid 70).
  # shellcheck disable=SC2016 # expanded by the container's bash
  walg -v "$PGVOL:/var/lib/postgresql/data" -- 'set -euo pipefail
    d=/var/lib/postgresql/data
    wal-g backup-fetch "$d" "$1"
    touch "$d/recovery.signal"
    chown -R 70:70 "$d"
    chmod 0700 "$d"' _ "$BACKUP" >>"$LOG" 2>&1
}

# start_fetcher runs the WAL server of the recovery: postgres' restore_command drops a request
# file for each segment it wants, this container fetches it with wal-g (which prefetches the next
# segments) and answers with the file, or with a .none marker once the archive has no such file.
start_fetcher() {
  local i
  # shellcheck disable=SC2016 # expanded by the container's bash
  docker run -d --name "$ID-walg" --user 0 --env-file "$TMP/walg.env" "${WALG_ARGS[@]}" -v "$WALVOL:$WALIN" \
    --entrypoint bash "$WALG_IMAGE" -c 'set -u
      d=$1
      install -d -o 70 -g 70 -m 0700 "$d" "$d/req"
      touch "$d/req/.ready"
      while :; do
        for r in "$d"/req/*; do
          [ -e "$r" ] || continue
          f=${r##*/}
          n=0
          while :; do
            rc=0
            wal-g wal-fetch "$f" "$d/$f.tmp" 2>"$d/last-error" || rc=$?
            if [ "$rc" -eq 0 ]; then
              chown 70:70 "$d/$f.tmp"; mv "$d/$f.tmp" "$d/$f"; echo "fetched $f"; break
            fi
            rm -f "$d/$f.tmp"
            n=$((n + 1))
            if [ "$rc" -eq 74 ] || [ "$n" -ge 5 ]; then
              [ "$rc" -eq 74 ] && echo "missing $f" || { echo "error $f: $(cat "$d/last-error")"; }
              touch "$d/$f.none"; chown 70:70 "$d/$f.none"; break
            fi
            sleep $((n * 2))
          done
          rm -f "$r"
        done
        sleep 0.2
      done' _ "$WALIN" >/dev/null
  for ((i = 0; i < 60; i++)); do
    if docker exec "$ID-walg" test -e "$WALIN/req/.ready" 2>/dev/null; then
      docker exec "$ID-walg" rm -f "$WALIN/req/.ready"
      return 0
    fi
    sleep 1
  done
  log "the WAL fetcher did not start"
  return 1
}

start_postgres() {
  local restore args
  restore="f=$WALIN/%f; rm -f \"\$f.none\"; : > \"$WALIN/req/%f\"; n=0;"
  restore+=" while [ ! -f \"\$f\" ] && [ ! -f \"\$f.none\" ]; do n=\$((n+1)); [ \$n -gt 6000 ] && exit 1; sleep 0.1; done;"
  restore+=" [ -f \"\$f\" ] && mv \"\$f\" \"%p\""
  args=(postgres -c config_file=/etc/postgresql/postgresql.conf -c archive_mode=off
    -c "restore_command=$restore" -c recovery_target_action=promote)
  if [[ -n "$TARGET" ]]; then
    args+=(-c "recovery_target_time=$(date -u -d "@$TARGET" '+%Y-%m-%d %H:%M:%S+00')")
  fi
  docker network create --internal "$NET" >/dev/null
  docker run -d --name "$ID-pg" --network "$NET" --network-alias tally-postgres \
    -v "$PGVOL:/var/lib/postgresql/data" -v "$WALVOL:$WALIN" -v "$CONF:/etc/postgresql/postgresql.conf:ro" \
    "$PG_IMAGE" "${args[@]}" >/dev/null
}

wait_promoted() {
  local deadline state
  deadline=$(($(date +%s) + RECOVERY_TIMEOUT))
  while (($(date +%s) < deadline)); do
    if [[ "$(docker inspect -f '{{.State.Running}}' "$ID-pg" 2>/dev/null)" != "true" ]]; then
      log "the scratch postgres stopped during recovery:"
      docker logs --tail 20 "$ID-pg" 2>&1 | tee -a "$LOG" >&2
      return 1
    fi
    if state=$(psql_scratch 'SELECT pg_is_in_recovery()' 2>/dev/null) && [[ "$state" == "f" ]]; then
      return 0
    fi
    sleep 2
  done
  log "recovery did not finish within ${RECOVERY_TIMEOUT}s"
  return 1
}

measure_recovery() {
  local out
  out=$(psql_scratch "SELECT coalesce(extract(epoch FROM pg_last_xact_replay_timestamp())::bigint::text, 'none'), pg_last_wal_replay_lsn()" | tr '|' ' ')
  read -r RECOVERED REPLAY_LSN <<<"$out"
  # No transaction replayed after the backup: the copy ends where the backup does.
  if [[ "$RECOVERED" == "none" ]]; then
    RECOVERED=$BACKUP_FINISH
  fi
  if [[ -n "$PRIMARY_LSN" && -n "$REPLAY_LSN" ]]; then
    LOST_BYTES=$(psql_scratch "SELECT greatest(pg_wal_lsn_diff('$PRIMARY_LSN', '$REPLAY_LSN'), 0)::bigint")
  fi
  SEGMENTS=$(docker logs "$ID-walg" 2>/dev/null | grep -c '^fetched ' || true)
  MISSING=$(docker logs "$ID-walg" 2>/dev/null | grep '^error ' | head -n 3 || true)
  log "recovered to $(iso "$RECOVERED") at $REPLAY_LSN after $SEGMENTS WAL segments"
}

start_api() {
  local i
  docker run -d --name "$ID-api" --network "$NET" --env-file "$TMP/app.env" "$API_IMAGE" serve >/dev/null
  for ((i = 0; i < 150; i++)); do
    if [[ "$(docker inspect -f '{{.State.Running}}' "$ID-api" 2>/dev/null)" != "true" ]]; then
      log "the scratch API stopped:"
      docker logs --tail 20 "$ID-api" 2>&1 | tee -a "$LOG" >&2
      return 1
    fi
    if docker exec "$ID-api" /tally healthcheck --url http://127.0.0.1:8080/readyz >/dev/null 2>&1; then
      return 0
    fi
    sleep 2
  done
  log "the scratch API did not become ready"
  return 1
}

ledger_verify() {
  local out rc=0
  out=$(docker exec "$ID-api" /tally ledger verify 2>&1) || rc=$?
  printf '%s\n' "$out" >>"$LOG"
  LEDGER_LINE=$(printf '%s\n' "$out" | grep '^verified ' | tail -n 1 || true)
  LEDGER_LINE=${LEDGER_LINE:-$(printf '%s\n' "$out" | tail -n 1)}
  log "ledger verify: $LEDGER_LINE"
  return "$rc"
}

restore_checks() {
  local id url list
  url=$(scratch_url)
  if ((${#TENANTS[@]} == 0)); then
    list=$(psql_scratch 'SELECT id FROM tally.tenants ORDER BY slug')
    mapfile -t TENANTS <<<"$list"
  fi
  for id in "${TENANTS[@]}"; do
    [[ -n "$id" ]] || continue
    if docker exec "$ID-api" /tally tenant restore-check --tenant "$id" --scratch-url "$url" >>"$LOG" 2>&1; then
      CHECKS_OK=$((CHECKS_OK + 1))
    else
      CHECKS_FAILED=$((CHECKS_FAILED + 1))
      log "restore-check failed for tenant $id (details in $LOG)"
    fi
  done
  log "restore-check: $CHECKS_OK passed, $CHECKS_FAILED failed"
  ((CHECKS_FAILED == 0))
}

write_report() {
  local status=$1 reference rpo="-" rto="-" verdict=()
  reference=${TARGET:-$INCIDENT}
  if [[ -n "$RECOVERED" ]]; then
    rpo=$((reference - RECOVERED))
    ((rpo >= 0)) || rpo=0
  fi
  if [[ -n "${MARK[ledger]:-}" ]]; then
    rto=$((MARK[ledger] - T0))
  elif [[ "$DB_ONLY" == 1 && -n "${MARK[promoted]:-}" ]]; then
    rto=$((MARK[promoted] - T0))
  fi
  if [[ "$status" == "ok" ]]; then
    RESULT=PASS
    if [[ "$rpo" == "-" || "$rpo" -gt "$RPO_LIMIT" ]]; then
      if [[ "$LOST_BYTES" == "0" ]]; then
        verdict+=("RPO by time is ${rpo}s but no WAL of the primary is missing: the database was quiet")
      else
        RESULT=FAIL
        verdict+=("RPO ${rpo}s exceeds ${RPO_LIMIT}s")
      fi
    fi
    if [[ "$rto" == "-" || "$rto" -gt "$RTO_LIMIT" ]]; then
      RESULT=FAIL
      verdict+=("RTO ${rto}s exceeds ${RTO_LIMIT}s")
    fi
    if [[ "$RESULT" == "PASS" && "$DB_ONLY" == 1 ]]; then
      RESULT=PARTIAL
      verdict+=("database only: the API, ledger verify and restore-check did not run")
    fi
  else
    RESULT=FAIL
    verdict+=("stopped while $STEP")
  fi
  {
    echo "# Restore drill $ID"
    echo
    echo "Result: **$RESULT**${verdict[*]:+ — $(IFS=';'; echo "${verdict[*]}")}"
    echo
    echo "| Item | Value |"
    echo "|---|---|"
    echo "| Drill started | $(iso "$T0") |"
    echo "| Incident time | $(iso "$INCIDENT") |"
    echo "| Target time | $(iso_or "$TARGET" "end of the archive") |"
    echo "| Base backup | ${BACKUP:--} ${BACKUP_FINISH:+(finished $(iso "$BACKUP_FINISH"))} |"
    echo "| WAL segments fetched | $SEGMENTS |"
    echo "| Recovered to | $(iso_or "$RECOVERED" -) (LSN ${REPLAY_LSN:--}) |"
    echo "| Primary at incident | ${PRIMARY:--} ${PRIMARY_LSN:+LSN $PRIMARY_LSN, WAL not recovered: ${LOST_BYTES:--} bytes} |"
    echo "| RPO | ${rpo} s (limit ${RPO_LIMIT} s) |"
    echo "| RTO | ${rto} s (limit ${RTO_LIMIT} s) |"
    echo "| ledger verify | ${LEDGER_LINE:--} |"
    echo "| restore-check | $CHECKS_OK passed, $CHECKS_FAILED failed |"
    echo "| Images | $WALG_IMAGE, ${PG_IMAGE}$([[ "$DB_ONLY" == 1 ]] || echo ", $API_IMAGE") |"
    echo
    echo "| Phase | Duration |"
    echo "|---|---|"
    echo "| Choose and fetch the base backup | $(since start fetched) |"
    echo "| WAL replay until promotion | $(since fetched promoted) |"
    echo "| API ready on the scratch copy | $(since promoted api) |"
    echo "| ledger verify | $(since api ledger) |"
    echo "| restore-check | $(since ledger checked) |"
    if [[ -n "$MISSING" ]]; then
      echo
      echo "WAL fetch errors (first three):"
      echo
      printf '%s\n' "$MISSING" | sed 's/^/    /'
    fi
    echo
    echo "RPO is the reference time (target time, else incident time) minus the last transaction the"
    echo "copy replayed. RTO runs from the drill start to a clean ledger verify on the scratch API."
    echo "Full log: $(basename "$LOG")."
  } >"$REPORT"
  log "report: $REPORT ($RESULT)"
}

cleanup() {
  local rc=$?
  trap - EXIT
  if [[ -z "$RESULT" ]]; then
    if ((rc == 0)); then write_report ok; else write_report failed; fi
  fi
  if ((KEEP)); then
    log "kept the scratch stack; remove it with:"
    log "  docker rm -f $ID-pg $ID-walg $ID-api; docker network rm $NET; docker volume rm $PGVOL $WALVOL"
  else
    docker rm -f "$ID-api" "$ID-pg" "$ID-walg" >/dev/null 2>&1 || true
    docker network rm "$NET" >/dev/null 2>&1 || true
    docker volume rm "$PGVOL" "$WALVOL" >/dev/null 2>&1 || true
  fi
  rm -rf "$TMP"
  if [[ "$RESULT" == "FAIL" ]]; then
    exit 1
  fi
  exit "$rc"
}
trap cleanup EXIT
trap 'exit 1' INT TERM

log "restore drill $ID (env $ENV_FILE)"
mark start
STEP="checking the images"
if ((DB_ONLY)); then check_images; else check_images "$API_IMAGE"; fi
STEP="reading the settings"
write_env_files
STEP="probing the primary"
probe_primary
STEP="choosing the base backup"
choose_backup
STEP="fetching the base backup"
fetch_base
mark fetched
STEP="replaying WAL"
start_fetcher
start_postgres
wait_promoted
mark promoted
measure_recovery
if ((!DB_ONLY)); then
  STEP="starting the scratch API"
  start_api
  mark api
  STEP="verifying the ledgers"
  ledger_verify
  mark ledger
  STEP="checking the tenants"
  restore_checks
  mark checked
fi
STEP="done"
