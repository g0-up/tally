# Tally runbook

Operational guide for developing, deploying and running Tally. Commands run from the repository root
unless stated otherwise; `make help` lists every target. Architecture background lives in
`plans/261016-0854-tally-full-product-implementation/design/architecture.md` (§2 topology, §5 infra,
§7 tenant hosts and edge).

## 1. Development

### Prerequisites

- Docker with the compose plugin.
- Go (version from `apps/api/go.mod`; the `go` command downloads the toolchain when needed).
- Node 22.12 or newer with corepack (`corepack enable`), which provides the pinned pnpm.
- `golangci-lint` v2 for `make lint`.

### First run

```sh
cd apps/web && pnpm install && cd ../..   # once, and after dependency changes
make dev
```

`make dev` runs `check-ports`, starts the dev dependencies in the docker compose project `tally`
(`infra/docker-compose.yml`), then runs the API with `go run` and the web app with Vite. Ctrl-C stops the
API and Vite; the dependencies keep running until `make down`. `make dev` does not migrate: the schema
migrations are embedded in the binary (`apps/api/migrations`), and you apply them by hand with
`make migrate-up`, after `make db-backup` when the dev database holds data. `make db-docs` regenerates
the table index [docs/database.md](database.md) from the migrated dev database.

| Service | URL / port (bound to 127.0.0.1) | Credentials |
|---|---|---|
| API | http://localhost:8080 (`/healthz`) | |
| Web (Vite) | http://localhost:5173, `/api` proxied to :8080 with the original `Host` | |
| Postgres 16 | localhost:**5442** (`POSTGRES_HOST_PORT`), database `tally` | `postgres`/`postgres`, `tally_owner`/`tally_owner_dev`, `tally_app`/`tally_app_dev`, `tally_readonly`/`tally_readonly_dev` |
| Mailpit | SMTP localhost:1025, UI http://localhost:8025 | |
| MinIO | S3 http://localhost:9000, console http://localhost:9001, bucket `tally-dev` | `tally-dev`/`tally-dev-secret` |

The dev defaults (`DATABASE_URL` as `tally_app`, `MIGRATE_DATABASE_URL` as `tally_owner`, SMTP,
object store, `PUBLIC_BASE_URL=http://localhost:5173`) live in `apps/api/Makefile`; override them in
the shell or in `apps/api/.env` (git-ignored).

### Tenant hosts in development

Every tenant has its own host. Browsers resolve `*.localhost` to the loopback address without any DNS
or `/etc/hosts` change, and Vite accepts those hosts (`server.allowedHosts: ['.localhost']`):

- apex (marketing, signup): http://localhost:5173
- platform admin: http://admin.localhost:5173
- tenant `demo`: http://demo.localhost:5173

Command-line check without a browser: `curl -fsS -H 'Host: demo.localhost:5173' localhost:5173`.

### Ports

Ports are fixed. `make check-ports` (also the first step of `make dev`) checks 8080, 5173, 5442, 1025,
8025, 9000 and 9001; when one is taken by anything other than the Tally dev project it prints the owner
(container and compose project, or process) and exits 1. It never switches to another port. Stop the
owner, or, for another compose project, run that project under its own name (`docker compose -p <name>`)
and stop it with the same name.

Port 5432 is often held by another local Postgres, so Tally publishes Postgres on 5442. To use a
different host port, export `POSTGRES_HOST_PORT` for every `make` and `docker compose` call; the API
Makefile derives the dev database URLs from it.

### Everyday commands

```sh
make migrate-up         # apply the embedded schema migrations (make db-backup first when data exists)
make db-docs            # regenerate docs/database.md from the migrated dev database
make test               # Go unit tests (race) + Vitest
make test-integration   # Go integration tests (build tag "integration", needs Docker; runs db-up first)
make lint               # go vet + golangci-lint + eslint + tsc
make build              # tally-api and tally-web images tagged with VERSION (git describe)
make seed               # tally seed dev: the store cua-hang-a the browser suites sign in to
make e2e                # Playwright against a running `make dev`
make down               # stop the dev dependencies (data stays in the tally_pgdata / tally_miniodata volumes)
```

Resetting the dev database: `docker compose -f infra/docker-compose.yml down -v` deletes the `tally`
project volumes only; the role init script runs again on the next `make db-up`.

Integration tests start a throwaway `postgres:16-alpine` per test package through testcontainers (roles
from `infra/postgres/roles.sql`, then every embedded migration applied as `tally_owner`), so they never
touch the dev database. `go test -tags integration ./migrations/...` additionally checks the catalog
(no functions, triggers, views, sequences or extensions; row security on every tenant table),
tenant isolation on every tenant table, and that up, down all, up rebuilds an identical schema. The object store and mailer tests use the dev MinIO and Mailpit (`make db-up`;
override with `OBJECT_STORE_*` and `MAILPIT_URL`). Leftover test containers carry the label
`org.testcontainers=true` and are removed by the Ryuk reaper when the test process exits.

### API binary (`tally`)

Run from `apps/api` with `go run ./cmd/tally <command>` (the Makefile targets add the dev defaults), or
`/tally <command>` inside the image. `tally --help` lists every command.

| Command | Purpose |
|---|---|
| `serve [--addr :8080]` | HTTP API; Prometheus metrics on `METRICS_ADDR` (default `127.0.0.1:9090`); `/healthz` (process up) and `/readyz` (database reachable, 503 otherwise) answer on any host |
| `worker [--dispatch-interval 2s] [--purge-interval 24h] [--stock-interval 1h] [--shift-interval 1h] [--sweep-interval 1h] [--export-interval 15s] [--subscription-interval 1h] [--usage-interval 24h]` | outbox dispatcher (one transaction per event; polls every interval while events are due, backing off to 10s when idle) and the scheduled jobs: idempotency-key purge, expired lots and stock signals, POS shifts left open from an earlier business date, notification sweep, queued report and data exports (and expiry of finished ones), due subscription transitions and account closures, daily usage counters. Every scheduled job runs once at start; a failing tenant is logged and the rest still run. Metrics on `METRICS_ADDR` (default `127.0.0.1:9091`) |
| `migrate up` / `down [N]` (default 1) / `down --all` / `version` / `force VERSION` | schema migrations as `tally_owner` (`MIGRATE_DATABASE_URL`); `force` clears a dirty state (`force -- -1` for "no version") |
| `maintenance purge-idempotency` | one-off purge of expired idempotency keys, one tenant per transaction |
| `maintenance expire-exports` | one-off run of the export retention the worker applies on every `--export-interval` tick, for every tenant not replaced or held in maintenance. It deletes the files of finished exports past their download link (24 h for a report export, 7 days for a full data export) and marks them expired, and marks failed any export left running without a heartbeat for over an hour. It reads the worker configuration (database and `OBJECT_STORE_*`) and prints `expired N exports`. A file that cannot be deleted leaves its export as it is for the next run, and the command fails |
| `maintenance revoke-sessions --tenant <id or slug> [--reason <text>]` | signs out every session of a tenant (incident handling); the reason is recorded on the sessions and in the audit log |
| `ledger verify [--tenant <id>] [--sku <id>]` | proves every projection of the inventory, procurement, sales, debt, cash and kit ledgers equals the sum of its ledger, for every tenant or one, and that the status of each purchase order, invoice and assembly order agrees with the quantities of its lines; `--sku` checks only that SKU's stock ledger. Non-zero exit on a mismatch. Runs only as the application role (the `DATABASE_URL` of the API): a superuser or a role with BYPASSRLS, such as `MIGRATE_DATABASE_URL`, is refused |
| `ledger rebuild --tenant <id> [--dry-run \| --confirm] [--statement-timeout 10m]` | rewrites every projection of one tenant that a `ledger verify` check finds wrong to the value that check expects, then the purchase order, invoice and assembly order statuses those quantities give (set directly, not through the usual status transitions). A status that is wrong together with its quantities shows in `verify` as the quantity only; run `verify` again after the rebuild. It runs in one tenant transaction holding the tenant's posting locks and verifies again inside it. The default (or `--dry-run`) prints the differences, each marked `rebuild` or `manual`, and the rows it would rewrite, then rolls back. Each statement may run for `--statement-timeout` (0 for no limit) instead of the API's `DB_STATEMENT_TIMEOUT`. Like `verify`, it runs only as the application role. `--confirm` writes: it commits and records `ledger.rebuild` in the audit log only when the verify after the rewrite is clean, and otherwise rolls everything back and fails. **Back up first** (`make db-backup`); only a restore undoes a rebuild |
| `tenant create --name <store> --owner-email <email> [--slug <slug>] [--owner-name <name>]` | provisions a tenant (store, warehouse, settings, default data, `tenant.create` audit entry) with its owner pending verification, and mails the owner the verification link, where they choose a password; `--slug` defaults to the address suggested from the name (`Cửa hàng A` gives `cua-hang-a`, then `cua-hang-a-2` …). Outside production the one-time link is also printed |
| `tenant maintenance on\|off --tenant <id>` | holds a tenant in maintenance or releases it. While held, every change is refused (`423 tenant_maintenance`) and its background jobs are skipped; reads keep working |
| `tenant restore-check --tenant <id> --scratch-url <url>` | counts one tenant's rows in a restored backup and verifies its ledgers there; non-zero exit means the backup is not a sound source |
| `tenant copy --tenant <id> --from <url>` | copies the tenant's rows missing from the live database out of a restored backup (tenant in maintenance; all or nothing) |
| `tenant purge --tenant <id> (--confirm-slug <slug> --backup-file <dump> \| --dry-run)` | deletes every row of a store closed at its owner's request and retires its address, as `tally_owner` |
| `seed demo --owner-email <email> [--parent <slug>] [--owner-name <name>]` | creates a demo (sandbox) tenant: `<parent>-demo` for the store given with `--parent`, or the standalone `demo` without one. Its owner needs an e-mail address no other account uses |
| `seed dev` | creates the development store `cua-hang-a` (Cửa hàng A) with 40 SKUs and at least 400 stock moves (`make seed` runs it). The owner is `E2E_OWNER_EMAIL` (default `chu-cua-hang-a@example.test`). With `E2E_OWNER_PASSWORD` set, the owner is activated with that password; otherwise they stay pending verification. When the store exists with at least that data, the command changes nothing and prints its SKU and stock move counts, so it can run again. When it exists with fewer SKUs or stock moves (made by `tenant create` or a seed dev that failed half way), the command fails: remove that store or reset the database, then run it again. Runs only with `APP_ENV` set to `development` or `test`; an unset `APP_ENV` is refused (`make seed` defaults it to `development`) |
| `seed load [--tenants 1] [--skus 20000] [--moves 1000000] [--seed 42] [--slug-prefix load]` | creates load-test tenants `<prefix>-<seed>-<n>` filled through the domain services, then verifies the ledgers; owners are activated with `SEED_LOAD_OWNER_PASSWORD` when set. about 95 stock moves a second per tenant, so the default takes about 3 hours. Used by `make load-seed` ([`load/README.md`](../load/README.md)), which runs it in the api image of the production-like stack, where `APP_ENV` defaults to `production`: that stack's env file must set it to `development` or `test`. Runs only with `APP_ENV` set to `development` or `test`; an unset `APP_ENV` is refused |
| `admin create --email <email> --name <name>` | creates an active platform admin; the password is read from the first line of stdin and the TOTP enrolment is printed once |
| `healthcheck [--url <liveness url>] [--timeout <d>]` | container health probe (calls `/healthz`) |

The tenant `maintenance`, `restore-check`, `copy` and `purge` procedures are in
[`docs/operations/tenant-restore.md`](operations/tenant-restore.md).

`migrate up`, `down` and `force` refuse to run without `--backup-file <dump>` unless `APP_ENV` is
set explicitly to `development` or `test`. An unset `APP_ENV` does not waive the guard: the migrator
cannot tell which database `MIGRATE_DATABASE_URL` points at, so a shell that forgot `APP_ENV` is
treated like production. The dump must be a pg_dump custom-format archive (it starts with the
`PGDMP` signature, which `make db-backup` produces) written within the last hour; a plain-SQL,
truncated or stale file is refused (`make prod-migrate` passes the dump it just took). `make
migrate-up` / `migrate-down` set `APP_ENV=development` for the dev database. `version` is
read-only and needs no backup.

`MIGRATE_ON_START=true` makes `serve` run `migrate up` before listening; it needs
`MIGRATE_DATABASE_URL` and is rejected in production.

Ledger drift follows verify → rebuild dry run → backup (`make db-backup STACK=prod` on production) →
`ledger rebuild --confirm` → verify. The steps and the reading of each output are in
[the support checklist's ledger drift section](support/support-checklist.md#3-ledger-drift).

`serve` and the account commands (`tenant create`, `seed demo`, `seed dev`, `seed load`, `admin create`,
`maintenance revoke-sessions`) need the SMTP settings and the secrets `SESSION_HASH_KEY`, `CSRF_KEY` and `APP_ENCRYPTION_KEY`. Outside
production an unset secret falls back to a public development value and a warning names it; in
production each must be set to at least 32 characters or the command refuses to start. Never rotate
them casually: a new `SESSION_HASH_KEY` signs every owner out and voids pending e-mail links and MFA
recovery codes, and a new `APP_ENCRYPTION_KEY` makes the stored authenticator-app secrets unreadable.

### Browser, device and peripheral checks

Parity, accessibility and the browser/device matrix (Chrome, Edge, Firefox, Safari, real Android and iOS
phones, HID scanner, 80 mm and A4 printers) are run as described in
[`docs/operations/device-browser-matrix.md`](operations/device-browser-matrix.md).

## 2. Database roles

`infra/postgres/init/01-roles.sh` runs once, when the Postgres data volume is empty, and applies
`infra/postgres/roles.sql` with the passwords passed as psql variables:

| Role | Use | Rights |
|---|---|---|
| `tally_owner` | migrations (`MIGRATE_DATABASE_URL`) | owns schema `tally` |
| `tally_app` | API and worker (`DATABASE_URL`) | `SELECT/INSERT/UPDATE/DELETE` on tables created by `tally_owner`; no DDL; member of `tally_readonly` for support sessions |
| `tally_readonly` | consented support sessions | `SELECT` only, and none on the platform tables (tenants, users, sessions, subscriptions …) |

The last migration re-asserts these grants on every table, revokes `UPDATE`/`DELETE` from `tally_app` on
the append-only tables (ledgers, audit and version history) and keeps `schema_migrations` to
`tally_owner`.

Check: `psql "$DATABASE_URL" -c 'create table tally.x(i int)'` fails with `permission denied for schema tally`.
Changing a password later needs `ALTER ROLE ... PASSWORD` as well as the env file, because the init
script never runs again on an existing volume.

## 3. Backup and restore

Production has two layers: continuous WAL archiving with a nightly base backup (point-in-time
recovery, RPO ≤ 15 min, RTO ≤ 4 h) and logical dumps taken before every migration and on demand.
Development uses the dumps only.

### Continuous archiving (production)

`infra/docker-compose.backup.yml` is part of every production command (`BACKUP_COMPOSE` in the
Makefile; `BACKUP_COMPOSE=` leaves it out). It mounts `infra/postgres/postgresql.prod.conf` into
postgres and adds the `walg` sidecar (wal-g, image `tally-walg:$VERSION` from `infra/backup/`):

- postgres archives every finished WAL segment into the `walspool` volume (`archive_mode=on`,
  `archive_timeout=300s`, so a quiet database still closes a segment every 5 minutes);
- the sidecar uploads the spool every 30 s (`wal-g wal-push`), takes a base backup daily at
  `BACKUP_BASE_TIME` (`APP_TZ`, default 02:30) and keeps `BACKUP_RETAIN_FULL` (default 7) full
  backups with the WAL they need;
- everything is encrypted client-side with `WALG_LIBSODIUM_KEY` before it leaves the host.

Before the first `make prod-up`, create a bucket and an access token scoped to it (object
read/write), separate from the app's object store, and fill the `WALG_*` values of `infra/.env`
(`openssl rand -base64 32` for the key). Store a copy of the bucket credentials and the key outside
the homelab: without the key no backup restores, and rotating it needs a fresh base backup.

```sh
docker compose -p tally-prod ps walg                          # healthy, or unhealthy with the reason below
docker compose -p tally-prod exec walg walg-sidecar health    # why: WAL waiting > 15 min, archiver failing, base backup > 26 h old
docker compose -p tally-prod exec walg walg-sidecar list      # base backups in the bucket
docker compose -p tally-prod exec walg walg-sidecar base      # base backup now (after a restore or key rotation)
docker compose -p tally-prod logs walg                        # uploads, retention, failures
```

The sidecar reads the data volume read-only and connects to postgres as the superuser
(`POSTGRES_SUPERUSER_PASSWORD`) for `pg_backup_start`/`pg_backup_stop`. When uploads fail, postgres
keeps the WAL in `pg_wal` and the spool grows; fix the bucket or token before the disk fills.

### Logical dumps

```sh
make db-backup                                    # dev stack
make db-backup STACK=prod                         # production (reads infra/.env)
make db-restore FILE=infra/backups/tally-<timestamp>.dump [STACK=prod]
```

- `db-backup` runs `pg_dump -Fc` inside the postgres container as the superuser and writes
  `infra/backups/tally-<YYYYmmddHHMMSS>.dump` with mode 600 (written to `.partial` first, so a failed dump
  never looks complete). `infra/backups/` is git-ignored; copy dumps off the machine as well.
- `db-restore` asks for `yes` (or takes `CONFIRM=yes`), then runs
  `pg_restore --clean --if-exists --single-transaction --exit-on-error`: either the whole dump is applied
  or nothing changes. Stop the api and worker first in production (`docker compose ... stop api worker`).
  The roles must already exist, which they do on any volume initialised by the stack.
- Restore drill of a dump on a scratch volume (never touches the real dev or prod data):

  ```sh
  export COMPOSE_PROJECT_NAME=tally-scratch POSTGRES_HOST_PORT=5443
  docker compose -f infra/docker-compose.yml up -d --wait postgres
  make db-restore FILE=infra/backups/tally-<timestamp>.dump CONFIRM=yes
  docker compose -f infra/docker-compose.yml down -v
  unset COMPOSE_PROJECT_NAME POSTGRES_HOST_PORT
  ```

### Measured restore drill

`make restore-drill` (`infra/scripts/restore-drill.sh`, `--help` lists every option) proves the
archive restores within budget. Run it monthly and before a release:

```sh
make restore-drill                                                   # newest backup + every archived WAL
make restore-drill DRILL_ARGS="--target-time '2026-10-19 09:30:00+07'"  # point in time
make restore-drill DRILL_ARGS="--tenant <tenant-id> --keep"          # one tenant, leave the copy running
```

It needs the `tally-walg` and `tally-api` images of the running release on the host (`make prod-up`
creates them; `--db-only` skips the API part and reports PARTIAL). The drill:

1. picks the newest base backup finished before the target and restores it into new volumes;
2. starts postgres 16 with the production settings, archiving off, on an internal network of its
   own (no egress, alias `tally-postgres` only there), and replays WAL fetched by a wal-g container
   until the target or the end of the archive, then promotes;
3. reads the last replayed commit time and, with `--primary` (the Makefile passes
   `DRILL_PRIMARY=tally-prod-postgres-1`, read-only), the WAL the primary had not archived yet;
4. starts the API image on the copy, runs `tally ledger verify` and `tally tenant restore-check` for
   every tenant (or each `--tenant`);
5. writes `test-results/restore-drill/tally-drill-<UTC timestamp>.md` (RPO, RTO, phase durations,
   ledger and restore-check results) and its log, then removes the scratch containers, network and
   volumes unless `--keep`.

PASS needs RPO ≤ 15 min (or no WAL lost on a quiet database) and RTO ≤ 4 h (`DRILL_RPO_LIMIT`,
`DRILL_RTO_LIMIT` in seconds); exit status 0 pass or partial, 1 fail, 2 usage error. Attach the report
to the release checklist.

### Restoring production to a point in time

Only after an incident, and only from a drill that passed:

1. `make restore-drill DRILL_ARGS="--target-time '<last good time>' --keep"`; read the report. The
   promoted copy is in volume `tally-drill-<timestamp>-pgdata`.
2. `docker stop tally-drill-<timestamp>-pg tally-drill-<timestamp>-api`, then `make prod-down` (never `-v`).
3. Keep the current data, then copy the restored one in (`cp -a` keeps the postgres owner):

   ```sh
   docker run --rm -v tally-prod_pgdata:/from:ro -v tally-prod_pgdata-before-restore:/to postgres:16-alpine cp -a /from/. /to/
   docker run --rm -v tally-drill-<timestamp>-pgdata:/from:ro -v tally-prod_pgdata:/to postgres:16-alpine \
     sh -c 'find /to -mindepth 1 -delete && cp -a /from/. /to/'
   ```

4. `make prod-up`, then `docker compose -p tally-prod exec walg walg-sidecar base` (the restore starts a
   new WAL timeline) and `docker compose -p tally-prod exec api /tally ledger verify`.
5. Remove the drill with the command it printed; drop `tally-prod_pgdata-before-restore` once the
   restored data is accepted.

Production never runs `migrate down`; rolling back a release means restoring the backup taken by
`make prod-migrate`. Some down migrations refuse to run once data depends on them (for example recost
COGS adjustments or replaced sandbox tenants) and fail with the blocking constraint's name; restore the
backup taken before the migration instead.

Recovering one store's rows from a backup, or permanently removing a closed store, uses the single-tenant
CLI described in [`docs/operations/tenant-restore.md`](operations/tenant-restore.md).

## 4. Production on the homelab

Tally runs as one stack (`tally-prod`) for every tenant: `infra/docker-compose.prod.yml` plus the
overlay `infra/docker-compose.homelab.yml`, with settings from `infra/.env`. Every `prod-*` target merges
both files.

```
browser ─HTTPS─▶ Cloudflare edge (sidecup.app, *.sidecup.app; Universal SSL)
        ─tunnel─▶ cloudflared-sidecup (network homelab, outbound only)
        ─http───▶ Traefik :80 (entrypoint web) ─▶ api:8080 for /api/* (Host preserved) | web:8080 for the rest
```

The edge (network `homelab`, Traefik, the `cloudflared-sidecup` tunnel) belongs to the homelab-infras
repository and is not changed from here. Tally adds only labelled containers: no proxy, no certificate,
no DNS token, and no published host port. Postgres stays on the project network under the alias
`tally-postgres`.

### First deploy checklist

1. **Env file.** `cp infra/.env.example infra/.env && chmod 600 infra/.env`, then replace every
   `change-me` value (`openssl rand -base64 32` for keys, `openssl rand -hex 24` for passwords that also
   appear inside `DATABASE_URL` / `MIGRATE_DATABASE_URL`). Set `TRUSTED_PROXY_CIDRS` to the subnet printed
   by `docker network inspect homelab -f '{{(index .IPAM.Config 0).Subnet}}'`. `make check-env` refuses
   placeholders and validates the merged compose file.
2. **Reserved slugs.** `RESERVED_SLUGS` lists every hostname label Sidecup already uses on `sidecup.app`
   (`tiendouong,tiendouong-api` at the time of writing), so signup can never hand them to a tenant.
3. **Start the stack.** `make prod-up` (build and start), then `make prod-migrate` (backup, then
   `tally migrate up --backup-file`). The api container must report healthy: `docker compose -p tally-prod ps`.
4. **Tunnel public hostnames** (Cloudflare Zero Trust → Networks → Tunnels → `cloudflared-sidecup` →
   Public hostnames): add `sidecup.app` and `*.sidecup.app`, both to service `http://traefik:80`. Place
   them **after** the existing specific Sidecup hostnames (`tiendouong.sidecup.app`,
   `tiendouong-api.sidecup.app`), because ingress rules match top-down.
5. **DNS.** Proxied CNAME records `@` and `*` → `<tunnel-id>.cfargotunnel.com`. Existing explicit records
   stay as they are; explicit records keep winning over the wildcard.
6. **Zone rules.**
   - SSL/TLS → Edge Certificates: Always Use HTTPS on.
   - Redirect Rule: `www.sidecup.app/*` → `https://sidecup.app/${1}` (301).
   - Cache Rule: bypass cache for URI path starting with `/api/` on `sidecup.app` and `*.sidecup.app`.
7. **Verify through the edge.**
   - `curl -sI https://sidecup.app`, `https://admin.sidecup.app` and `https://anything.sidecup.app` answer
     from Tally with `strict-transport-security: max-age=31536000; includeSubDomains`,
     `content-security-policy`, `x-content-type-options: nosniff` and `x-frame-options: DENY`.
   - `curl -sI https://sidecup.app/` and `https://sidecup.app/sw.js` (once the service worker ships) show
     `cache-control: no-cache` and no `cf-cache-status: HIT`.
   - `curl -sI https://tiendouong.sidecup.app` still answers from Sidecup (router priority check: Tally's
     routers use priority 3 and 2, below every exact `Host` router).
   - `docker ps --filter label=com.docker.compose.project=tally-prod --format '{{.Names}} {{.Ports}}'` shows
     no published host port.

### Adding a Sidecup hostname on the shared zone

Before adding any new Sidecup hostname `<label>.sidecup.app`:

1. Check that no Tally tenant owns the slug:
   `docker compose -p tally-prod exec postgres psql -U postgres -d tally -c "select id from tally.tenants where slug = '<label>'"`
   must return zero rows. If a tenant owns it, pick another hostname.
2. Add `<label>` to `RESERVED_SLUGS` in `infra/.env` and run `make prod-up` so signup refuses it.
3. Add the tunnel public hostname above the Tally wildcard entries.

### Routine operations

```sh
make prod-config                 # validate prod + homelab + backup compose with infra/.env
make prod-up                     # build and (re)start api, worker, web, postgres, walg
make prod-logs SERVICE=api       # follow logs (JSON on stdout)
make prod-migrate                # backup, then migrate up with --backup-file
make db-backup STACK=prod        # ad-hoc backup
make prod-down                   # stop the stack (volume tally-prod_pgdata stays)
```

**Web-only redeploy.** A front-end change ships without restarting api, worker or postgres. Tag the running
image first so it can be restored, then rebuild and recreate only `web`. Add
`-f infra/docker-compose.backup.yml` only when the stack runs the walg sidecar; it fails to interpolate
while the `WALG_S3_*` values are unset.

```sh
docker tag "$(docker inspect -f '{{.Image}}' tally-prod-web-1)" tally-web:rollback-$(date +%Y%m%d%H%M)
VERSION=$(git describe --tags --always --dirty) docker compose -f infra/docker-compose.prod.yml \
  -f infra/docker-compose.homelab.yml --env-file infra/.env up -d --build --no-deps web
docker compose -p tally-prod ps web       # healthy; then reload a tenant host and check the new asset hash
```

Rollback: `docker tag tally-web:rollback-<stamp> tally-web:<VERSION>` and run the same `up` command
without `--build`.

Volumes and projects are named per compose project (`tally` for dev, `tally-prod` for production), so
removing a Tally project never touches another stack's volumes. Always address a project explicitly
(`docker compose -p tally-prod ...` or the Make targets) instead of relying on the current directory.

Release checks (the [release checklist](release/release-checklist.md) lists which ones a release needs):

```sh
make restore-drill               # measured point-in-time restore into a scratch stack (section 3)
make security-scan               # govulncheck, gosec (via golangci-lint), pnpm audit --prod, trivy, gitleaks
make acceptance                  # Playwright acceptance journeys against a running stack (like make e2e)
LOAD_OWNER_PASSWORD=... make load-seed   # tally seed load: 50 tenants x 20 000 SKUs x 1 000 000 moves
LOAD_OWNER_PASSWORD=... make load-test   # k6 lookup, posting, reports, pos-devices, then ledger verify
LOAD_OWNER_PASSWORD=... make soak-test   # 4 h k6 soak with docker stats before/after, then ledger verify
```

The scanners are pinned in the Makefile and run from `go run` or their docker images, so nothing has
to be installed; `SCAN_IMAGES=` skips the image scans when the images are not on the host. Load runs
belong on a production-like stack, never on the live one: they create tenants and sell stock. Their
settings and pass criteria are in [`load/README.md`](../load/README.md).

### Upgrade and upgrade rehearsal

Upgrade: pull the new commit, `make prod-up`, then `make prod-migrate` when the release adds migrations.
The API never migrates on start in production (`MIGRATE_ON_START` is pinned to `false`).

A release with migrations is rehearsed first on a copy of production (NFR-14):

1. Create the new images without touching the running stack: check out the release, then
   `VERSION=<new> make build`.
2. `make restore-drill DRILL_ARGS=--keep`: a verified, promoted copy of production on the internal
   network `tally-drill-<timestamp>-net`, where `tally-postgres` is the copy.
3. Migrate the copy with the new image and time it; the URLs come from `infra/.env`:

   ```sh
   export MIGRATE_DATABASE_URL="$(sed -n 's/^MIGRATE_DATABASE_URL=//p' infra/.env)"
   export DATABASE_URL="$(sed -n 's/^DATABASE_URL=//p' infra/.env)"
   NET=tally-drill-<timestamp>-net
   time docker run --rm --network $NET -e MIGRATE_DATABASE_URL tally-api:<new> migrate up
   docker run --rm --network $NET -e MIGRATE_DATABASE_URL tally-api:<new> migrate version
   docker run --rm --network $NET -e DATABASE_URL tally-api:<new> ledger verify
   unset MIGRATE_DATABASE_URL DATABASE_URL
   ```

4. Record the migration time (the length of the maintenance window), the `ledger verify` result and
   any failure in the release checklist; a migration that fails here is fixed before release.
5. Remove the drill with the command it printed.

On release day upgrade as above with the new `VERSION` in `infra/.env`; `make prod-migrate` takes the
dump that a rollback restores. Then reconcile: `docker compose -p tally-prod exec api /tally ledger verify`,
the api container healthy, the edge checks of step 7 of the first deploy checklist, and no new alert
for an hour. Rolling back means the previous `VERSION`, `make prod-up`, then
`make db-restore STACK=prod FILE=<the dump prod-migrate wrote>` with api and worker stopped, never
`migrate down`.

### Alerts and observability

The API (`tally serve`) and the worker (`tally worker`) expose Prometheus metrics on `METRICS_ADDR`
(defaults `127.0.0.1:9090` and `127.0.0.1:9091`, never the public port). Logs are JSON on stdout with
the request id; metric labels carry route templates such as `/api/v1/skus/:id`, never ids or personal
data.

[`infra/monitoring/alerts.yml`](../infra/monitoring/alerts.yml) holds the alert rules for the scrape
jobs `tally-api` and `tally-worker`, with unit tests in `alerts.test.yml`. CI checks both; by hand:

```sh
docker run --rm -v "$PWD/infra/monitoring:/m:ro" -w /m --entrypoint promtool prom/prometheus:v3.15.0 check rules alerts.yml
docker run --rm -v "$PWD/infra/monitoring:/m:ro" -w /m --entrypoint promtool prom/prometheus:v3.15.0 test rules alerts.test.yml
```

| Signal (NFR-09, budgets NFR-03/05/07) | Covered by | Status |
|---|---|---|
| API or worker down | `TallyTargetDown`, `TallyMetricsMissing` | rule ready |
| 5xx | `TallyHigh5xxRatio` (> 0.5 % for 10 min), `TallyRoute5xx` | rule ready |
| p95 latency | `TallyLookupP95High` (> 1 s), `TallyPostingP95High` (> 2 s), `TallyReportP95High` (> 5 s), `TallyPostingErrors` | rule ready |
| Outbox lag, stuck and failed jobs | `TallyOutboxLagging` (> 5 min), `TallyOutboxStuck` (> 30 min), `TallyOutboxDeadEvents`, `TallyOutboxRetrying` | rule ready for outbox events only |
| Payment webhook failures | `TallyPaymentWebhookFailing` (non-2xx answers) | status codes only |
| WAL waiting > 15 min, archiver failing, stale base backup | `walg` container healthcheck (`walg-sidecar health`) | docker health only, no metric |
| Ledger drift | `tally ledger verify` by hand or from cron | no metric |
| Edge reachability (Cloudflare → tunnel → Traefik) | step 7 of the first deploy checklist | manual |

Gaps that need work before these alerts can page anyone:

- No Prometheus or Alertmanager runs in the stack, and both metric listeners bind to loopback inside
  their containers; scraping needs `METRICS_ADDR=0.0.0.0:<port>` on an unpublished network.
- No edge probe: an external uptime check of `https://sidecup.app` and a tenant host is not set up.
- No ledger drift metric: until there is one, run
  `docker compose -p tally-prod exec -T api /tally ledger verify` nightly from cron and mail a
  non-zero exit.
- No WAL-age metric and nothing alerts on an unhealthy container: watch `docker compose -p tally-prod ps`
  or add a container-health exporter.
- Background work outside the outbox (report exports, campaign sends, scheduled jobs) has no failure
  metric; webhook failures are not broken down by cause (signature, payload, processing).

### Creating a tenant

Tenants are rows in the shared database, not separate stacks. Owners normally sign up on the apex
host; an operator creates one with:

```sh
docker compose -p tally-prod exec api /tally tenant create --name "<store name>" --slug <slug> --owner-email <email>
```

The slug becomes `https://<slug>.sidecup.app`; it must not appear in `RESERVED_SLUGS` or collide with a
Sidecup hostname (see above). The command provisions the tenant's seed data and mails the owner a
verification link (valid 24 hours) where they choose a password; in production the link is not
printed. An e-mail address that already owns a store is refused.
