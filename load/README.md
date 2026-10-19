# Load and soak tests

k6 scenarios for the NFR-03/04/05 budgets and AT-03 under load. They run against a seeded
reference dataset on the prod-like stack (same images, homelab overlay behind a local Traefik, same
Postgres settings), never against production or a pilot's data.

| Script | What it does | Budget (`k6/thresholds.json`) |
|--------|--------------|-------------------------------|
| `k6/lookup.js` | Picker search (SKU, customer, supplier), SKU and stock detail, POS bootstrap, party lists | p95 < 1000 ms |
| `k6/posting.js` | POS checkout, invoice create → confirm → stock-out, goods receipt, customer payment | p95 < 2000 ms |
| `k6/reports.js` | Dashboard and every catalogue report without a required filter | p95 < 5000 ms |
| `k6/pos-devices.js` | 3 devices per tenant sell the last units of one color at once | exactly `POS_LAST_UNITS` sold, final stock 0, never negative |
| `k6/soak.js` | Lookup + posting + reports together for `SOAK_DURATION` (default 4h) | all three budgets |

Every scenario also caps unexpected responses below 0.5 % (`tally_unexpected_responses` and
`http_req_failed` per kind). A `409` counts as expected only for the races the API resolves on
purpose: `insufficient_stock` once a color runs out, and `sale_changed` / `shift_changed`, which the
scripts retry under the same `Idempotency-Key` as the API asks. Any other `409` (for example
`exception_required` from a credit limit or a minimum price in the seed) is an error.

## Requests

The scripts follow `apps/api/api/openapi.yaml`:

- Sign-in per device: `GET /api/v1/host-context` (anonymous CSRF cookie and token), then
  `POST /api/v1/auth/login` with `X-CSRF-Token`; the session and session-bound CSRF cookies travel in
  the `Cookie` header of every later request, and every `POST` sends `X-CSRF-Token`.
- Every posting command sends a fresh `Idempotency-Key`; the invoice confirm also sends `If-Match`
  with the `ETag` of the draft.
- The POS checkout prices the cart with `POST /pricing/calculate` first and pays exactly the total in
  cash, as a walk-in sale must.
- Money and quantities are decimal strings.

## Reference dataset

`make load-seed` runs `tally seed load` in the api image of the production-like stack:

```bash
SEED_LOAD_OWNER_PASSWORD=... tally seed load --tenants 50 --skus 20000 --moves 1000000 --seed 42
```

- `tally seed load` runs only with `APP_ENV` set to `development` or `test`, so the env file of the
  load stack sets `APP_ENV=test`; with the `production` of `infra/.env.example` it is refused, which
  keeps a load dataset out of a real store.
- Tenant slugs `load-42-1` … `load-42-N` (`<slug-prefix>-<seed>-<n>`; override with
  `LOAD_SLUG_PREFIX` or a `LOAD_SLUGS` list). An existing slug is an error, so a fresh dataset
  needs a new seed: `make load-seed LOAD_SEED=43` and the same `LOAD_SEED` for the runs.
- One owner per tenant who signs in as `owner@<slug>.load.tally.test` (override with
  `LOAD_OWNER_LOGIN`) with the shared password of `SEED_LOAD_OWNER_PASSWORD` (`make load-seed`
  passes `LOAD_OWNER_PASSWORD`), without two-step verification. Without the password the owners
  stay pending verification and cannot sign in.
- Each tenant has stocked colored SKUs in their base unit that are not lot-tracked, an active cash
  payment method, active suppliers, active customers without a credit limit, and an active
  `count_variance` or `loss` stock adjustment reason (used by `pos-devices.js`).
- `tally ledger verify` passes on the dataset before any measurement.

## Environment

Nothing here is a secret except the load owners' password; pass it from the shell, never commit it.

| Variable | Default | Meaning |
|----------|---------|---------|
| `BASE_URL` | `http://localhost` | Where requests go (the local Traefik entrypoint). |
| `TENANT_HOST` | `{slug}.localhost` | `Host` header per tenant, `{slug}` replaced. Use `{slug}.<DOMAIN>` when Traefik routes on the real domain. |
| `TENANT_URL` | empty | Instead of `BASE_URL` + `TENANT_HOST`: a per-tenant URL such as `https://{slug}.load.example`; DNS must resolve it. |
| `LOAD_TENANTS` | `50` | Tenant count (`OPEN-07` gives the real number). |
| `LOAD_SLUG_PREFIX` / `LOAD_SLUGS` | `load-42-` / empty | Tenant slugs, see above (`make` derives the prefix from `LOAD_SEED`). |
| `LOAD_OWNER_LOGIN` | `owner@{slug}.load.tally.test` | Owner login per tenant. |
| `LOAD_OWNER_PASSWORD` | required | Owner password of the seeded tenants. |
| `LOAD_DEVICES` | `3` | Devices (signed-in sessions and VUs) per tenant for lookup, posting and soak. |
| `LOAD_DURATION` / `LOAD_RAMP_UP` | `10m` / `1m` | Hold and ramp-up time of lookup, posting and reports. |
| `LOAD_THINK_MIN_MS` / `LOAD_THINK_MAX_MS` | `1000` / `3000` | Pause between two actions of a device. |
| `LOAD_FIXTURE_SKUS` | `30` | SKUs per tenant read in setup as sellable lines. |
| `LOAD_CLIENT_IPS` | `on` | Send a synthetic `CF-Connecting-IP` (198.18.0.0/15) per device. |
| `SOAK_DURATION` | `4h` | Soak length. |
| `POS_DEVICES` / `POS_LAST_UNITS` | `3` / `5` | Devices per tenant and units left in the contested color. |
| `LOAD_VERBOSE` | empty | `1` logs every unexpected response (status and error code). |

### Rate limits and client addresses

The API limits every client address to 20 requests/s (burst 100) and sign-in to 10 per minute per
address and login. It reads the client address from `CF-Connecting-IP` only when the TCP peer is in
`TRUSTED_PROXY_CIDRS`, so:

- Run against the local Traefik with the Traefik network in `TRUSTED_PROXY_CIDRS`; each device then
  counts as its own client, as on the real edge.
- Hitting the API container directly makes every device share the load host's address, and the
  run fails on `429 rate_limited`. That is a wrong setup, not a capacity finding.

## Running

The make targets run k6 from the pinned `grafana/k6` image and write summaries to
`load/test-results/` (git-ignored):

```bash
LOAD_OWNER_PASSWORD=... make load-seed LOAD_TENANTS=50      # once per LOAD_SEED, on the prod-like stack
LOAD_OWNER_PASSWORD=... make load-test BASE_URL=http://localhost
LOAD_OWNER_PASSWORD=... make soak-test BASE_URL=http://localhost
```

`make load-test` runs `lookup.js`, `posting.js`, `reports.js` and then `pos-devices.js`
(it changes the seeded stock, so it runs last; seed a new `LOAD_SEED` before the next run), then
`tally ledger verify`. One script by hand:

```bash
docker run --rm --network host -u "$(id -u):$(id -g)" -v "$PWD/load:/load" -w /load \
  -e BASE_URL -e LOAD_OWNER_PASSWORD -e LOAD_TENANTS \
  grafana/k6:2.3.0 run --summary-export test-results/lookup-summary.json k6/lookup.js
```

## Soak checks outside k6

`make soak-test` stores `docker stats` of the stack before and after the run in
`load/test-results/`. Pass criteria besides the k6 budgets:

- Memory of `api`, `worker` and `postgres` grows by at most 10 % between the two samples.
- No stuck outbox rows: `tally_outbox_lag_seconds` stays under 300 and no event went `dead`
  (`infra/monitoring/alerts.yml`); in SQL as the `postgres` superuser (row-level security hides
  the rows from the app role), `SELECT count(*) FROM outbox_events WHERE processed_at IS NULL AND
  available_at < now() - interval '5 minutes'` is 0 once the run has drained.
- `tally ledger verify` is clean (the target runs it at the end).

Record CPU, memory and database figures with the host size next to the summaries; tune and repeat
until the thresholds pass.
