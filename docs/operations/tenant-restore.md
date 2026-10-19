# Tenant restore, copy and purge

Single-tenant operations against a backup. The whole-database backup and restore procedure lives in
[`docs/runbook.md` §3](../runbook.md#3-backup-and-restore); this page covers recovering **one store's**
rows from a backup and permanently removing a closed store.

Commands are shown as `tally <command>`: run them from `apps/api` with `go run ./cmd/tally <command>`
in development, or `docker compose -p tally-prod exec api /tally <command>` in production. Every command
takes the tenant **id** (not the slug); find it in the admin console or with
`SELECT id FROM tally.tenants WHERE slug = '<slug>'`.

## 1. Restore the backup into a scratch database

Never restore into the live database. Bring up a throw-away postgres from the same compose file, so it
has the same roles (`tally_owner`, `tally_app`, `tally_readonly`) the dump's grants and row security
refer to:

```sh
export COMPOSE_PROJECT_NAME=tally-scratch POSTGRES_HOST_PORT=5443
docker compose -f infra/docker-compose.yml up -d --wait postgres
make db-restore FILE=infra/backups/tally-<timestamp>.dump CONFIRM=yes
```

`db-restore` runs `pg_restore --clean --if-exists --single-transaction --exit-on-error`, so the scratch
copy is either the whole dump or nothing. The scratch connection string uses the application role:

```sh
SCRATCH_URL=postgres://tally_app:<app password>@localhost:5443/tally?sslmode=disable
```

## 2. Check the tenant in the backup

```sh
tally tenant restore-check --tenant <tenant-id> --scratch-url "$SCRATCH_URL"
```

It prints the tenant's slug and status in the backup, its row count per tenant table, and runs the ledger
verification (debt, cash, inventory, procurement, sales, kits) on the scratch copy. It only reads,
inside a read-only tenant transaction. A non-zero exit or any `ledger mismatch` line means the backup
is not a sound source: pick an older dump and repeat from step 1.

## 3. Copy the missing rows into the live database

1. Hold the store so nothing posts while rows are copied:

   ```sh
   tally tenant maintenance on --tenant <tenant-id>
   ```

   Every change to the store is refused with `423 tenant_maintenance` while maintenance is on, and the
   background jobs (exports, subscription lifecycle, closures, notifications) skip it; reads keep working.
   The owner's account security (password, two-factor, sessions, ownership transfer) stays available,
   since those rows are not part of a copy.

2. Take a fresh backup of the live database (`make db-backup STACK=prod`), so the copy can be undone.

3. Copy:

   ```sh
   tally tenant copy --tenant <tenant-id> --from "$SCRATCH_URL"
   ```

   The copy reads and writes as `tally_app` inside tenant transactions for that tenant only, so row
   security keeps every other store out of reach on both sides. It is additive: rows already present
   live are kept as they are (`ON CONFLICT DO NOTHING`) and only missing rows are inserted. It refuses
   to run unless the tenant is in maintenance.

4. Reconcile. The command prints, per table, the scratch count, the live count before, the rows inserted
   and the live count after. It commits only when every scratch row is present live by its primary key
   and the ledgers verify on the live side; otherwise it rolls back and writes nothing. A table marked
   `MISSING n` holds scratch rows a live row refused under another unique key (for example a second
   primary contact); the `missing <table> key <id>` lines name the first five. Resolve those live rows
   in the app, then copy again. Spot-check the store in
   the app (stock on hand, open debts, fund balances) before releasing it.

5. Release the store:

   ```sh
   tally tenant maintenance off --tenant <tenant-id>
   ```

**Rollback.** A failed copy writes nothing. To undo a committed copy, restore the live backup taken in
step 2 ([runbook §3](../runbook.md#3-backup-and-restore)), then release maintenance.

Limits: only tenant tables under row security are copied (platform rows such as users, sessions or the
subscription are not), and object-store files (attachments, exports) are not part of the database dump.

Tear the scratch database down when done:

```sh
docker compose -f infra/docker-compose.yml down -v
unset COMPOSE_PROJECT_NAME POSTGRES_HOST_PORT
```

## 4. Purge a closed store

Purging deletes every row of a store and of its sandboxes and cannot be undone except from a backup.

How a store gets there: after the owner confirms, it keeps working for 30 days and may cancel. Within
about an hour after the wait ends the worker moves the closure to `exporting`: the store only reads, the
owner can no longer cancel, and a final full export is queued. Once that export succeeded the store
closes (`completed`) and the owner is e-mailed a link that downloads it without signing in until the
export's `expires_at` (7 days). A store under legal hold is held in `waiting` or `exporting` until the
hold ends.

A failed final export is queued again until three of them failed. The closure then stops at
`export_failed` and the store stays `closing` and read-only. The worker logs `account closure stopped`,
the store's audit log records `account_closure.export_failed`, and the admin tenant detail shows
`closure.request.status = export_failed`. Fix the cause from the export's `error`, then
`POST /api/admin/v1/tenants/<tenant-id>/account-closure:retry-export` starts a new round of three
attempts. The same call on a `completed` closure re-sends the download link when the owner lost the
mail; the earlier links stop working.

Completing the closure voids the store's open service invoices. A payment the provider confirms
afterwards is refused (409 `invoice_not_open`) and nothing changes in the store, but the provider may
already have taken the money. The API logs `payment refused: its invoice is not open, refund it by
hand` on every attempt, and the store's audit log records `saas_invoice.payment_refused` once per
provider event, with the provider, event id, amount and bank reference. Refund that amount through
the provider by hand and note the refund on the support ticket.

- Only a store **closed at its owner's request** can be purged: its status is `closed` and its latest
  account closure request is `completed`. A store closed because its subscription lapsed (no closure
  request, or a cancelled one) is refused; it never asked to leave and took no final export.
- Neither the store nor any of its sandboxes may be under legal hold. Clear a hold in the admin console
  (`subscription:set-status`) only when it has really ended.
- Purging deletes the export rows, so the owner's download link stops working. Wait until the final
  export's `expires_at` unless the owner confirmed they have the file.
- `--confirm-slug` must repeat the store's address, so a pasted wrong id stops here.
- It runs with the migration role, and outside development and test it needs a `pg_dump -Fc` archive
  taken within the last hour.

Look first, then purge:

```sh
export MIGRATE_DATABASE_URL=postgres://tally_owner:<owner password>@tally-postgres:5432/tally?sslmode=disable
tally tenant purge --tenant <tenant-id> --dry-run
make db-backup STACK=prod
tally tenant purge --tenant <tenant-id> --confirm-slug <slug> --backup-file infra/backups/tally-<timestamp>.dump
```

`--dry-run` runs every check and the whole delete, prints the rows it would remove per table and rolls
back; it needs neither the slug nor a backup. The real purge deletes children before parents in one
transaction, which also retires the store address: sign-up answers `taken` for that slug from then on.
It prints the rows deleted per table.

**Object-store files are not deleted** (product decision OPEN-11 is still open). The purge prints the
key prefix of the store and of each sandbox (`tenants/<tenant-id>/`): attachments and the full data
export zips under it still hold the customer's data. Until the retention decision lands, record the
prefixes with the purge ticket and remove them by hand once the decision allows it. Login-attempt
records are not deleted either.

Restart the API afterwards (`docker compose -p tally-prod restart api`) so its tenant host cache forgets
the address.

## 5. Platform admins

The admin console (`admin.<domain>`, API under `/api/admin/v1`) has its own accounts. Create one with:

```sh
printf '%s\n' '<password, 12+ characters>' | tally admin create --email <email> --name "<full name>"
```

The password is read from the first line of standard input so it stays out of the shell history. The
TOTP secret and `otpauth://` link are printed once; enrol them in an authenticator app immediately.
Sign-in always asks for a TOTP code. Five wrong codes in a row lock the admin for 15 minutes, even with
the right code (`429 rate_limited` with `Retry-After`); the lock is logged as `admin account locked`
and lifts by itself. Password attempts are throttled per e-mail.
