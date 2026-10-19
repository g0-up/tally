---
title: "Phase 02: API Platform Foundation"
status: done
priority: P1
effort: 5d
dependsOn: [phase-01-monorepo-docker-infra]
---

# Phase 02 — API Platform Foundation

## Context links

- [architecture.md](design/architecture.md) §3.1 layers, §3.2 platform kernel, §3.4 patterns, §3.5 posting flow, §3.6 HTTP conventions, §3.7 CLI, §3.8 tests, §7 tenant hosts (host map, pipeline, cookies, URL builder)
- [database-conventions.md](design/database-conventions.md) §2 (clock, ids, textnorm, version, docseq), §4 RLS + `WithTenantTx`, §6 lock order, §7 idempotency
- [ubiquitous-language.md](design/ubiquitous-language.md) — names of value objects and error codes
- PRD §7.3 (decimal, rounding), §11.1–11.2 (domain boundaries), NFR-02, NFR-09, NFR-15

## Overview

Build the technical kernel every domain uses: config, DB access with tenant transactions, money/quantity value objects, clock, ids, text normalisation, error model, HTTP engine and middleware, idempotency guard, document number issuer, audit recorder, outbox publisher/dispatcher, object store and mailer adapters, the posting coordinator skeleton, test kit, CLI commands and the OpenAPI contract skeleton. No business tables except those the kernel needs (created in phase 03).

## Key insights

- No logic may live in the database, so the kernel carries what triggers usually do: timestamps (`clock`), ids (`ids`), optimistic versions (repository helper), document numbers (`docseq`), append-only discipline (no UPDATE/DELETE helpers for ledger repos).
- `WithTenantTx` is the only way to obtain a tenant `db.Querier`; services receive the querier, never `*sqlx.DB`. This makes the Unit of Work explicit and RLS context unforgettable.
- `posting.Coordinator` is a Template Method: the fixed sequence (period lock → idempotency → doc number → ordered locks → apply → audit → outbox) is written once; documents implement a small `PostingCommand` interface.
- Money travels as decimal strings in JSON; `money.Money` refuses floats at construction.

## Requirements

| ID | Behaviour delivered here |
|---|---|
| NFR-02 | Atomic posting skeleton, idempotency guard, outbox at-least-once with dedupe |
| NFR-09 | Request id → `X-Request-Id` + log field; slog JSON; redaction; `/metrics` Prometheus (requests, latency, posting duration, outbox lag) |
| NFR-15 | `money` value objects with scale guards; `clock.BusinessDate(tz)`; version-checked updates |
| NFR-01 | `WithTenantTx` sets `app.tenant_id`; tenant id only from session context, never from request body or `Host`; the host tenant (`<slug>.DOMAIN`) is a cross-check that must equal the session tenant |
| SEC-04 | `audit.Recorder` with redaction of secrets |
| SEC-05 | `periodlock.Guard` checks business date vs locks |
| ORG-05 | `docseq.Issuer` gap-free per tenant/prefix |
| DATA-05 | `objectstore` signed URLs, size/type limits |

## Architecture

```go
// platform/db
type Querier interface { sqlx.ExtContext; sqlx.PreparerContext }
func (d *DB) WithTenantTx(ctx context.Context, tenantID uuid.UUID, fn func(q Querier) error) error   // BEGIN; SELECT set_config('app.tenant_id',$1,true); fn; COMMIT
func (d *DB) WithPlatformTx(ctx context.Context, fn func(q Querier) error) error
func (d *DB) WithReadOnlyTenantTx(ctx context.Context, tenantID uuid.UUID, fn func(q Querier) error) error // + SET LOCAL ROLE tally_readonly; SET TRANSACTION READ ONLY

// platform/money
type Money struct{ d decimal.Decimal }   // scale ≤ 4; NewMoney(string) (Money, error); Add/Sub/Mul(Qty|Factor|Percent); RoundVND()
type Qty struct{ d decimal.Decimal }     // scale ≤ 4, sign checks by caller
type Factor struct{ d decimal.Decimal }  // > 0, scale ≤ 6
func BaseQty(q Qty, f Factor) (Qty, error) // rejects results with > 4 decimals

// posting
type PostingCommand interface {
    Scope() string                       // idempotency scope, e.g. "receipt.post"
    BusinessDate() time.Time
    DocPrefix() string                   // "PN", "BH", "HD" … or "" when none
    LockKeys() LockSet                   // sku ids, lot ids, obligation ids … sorted by Coordinator
    Apply(ctx context.Context, tx Tx) (Result, error)
}
type Coordinator struct{ db *db.DB; idem idempotency.Guard; seq docseq.Issuer; locks Locker; lockGuard periodlock.Guard; audit audit.Recorder; outbox outbox.Publisher; ent EntitlementGuard; clock clock.Clock }
func (c *Coordinator) Post(ctx context.Context, actor Actor, key string, cmd PostingCommand) (Result, error)
```

`Tx` bundles the querier with request-scoped helpers (`Now()`, `Actor()`, `DocNo()`, `Publish(evt)`, `Audit(evt)`), keeping domain code free of kernel wiring (ISP).

Error model (`apperr`): `type Error struct{ Kind Kind; Code string; Message string; Fields map[string]string; ExceptionKind string }`; `httpx.RenderError` maps kind → status (Validation 422, NotFound 404, Conflict 409, ExceptionRequired 409, PreconditionFailed 412, PeriodLocked 423, ReadOnlyTenant 403, Forbidden 403, Unauthenticated 401) and never leaks internal errors (logged with request id, rendered as `internal_error`).

Middleware order: `RequestID → Logger → Recover → HostContext → SecurityHeaders(api) → RateLimit(ip) → Session → Tenant (+ TenantHostGuard) → CSRF(mutations) → Entitlement → SupportReadOnly → handler`.

Host handling (architecture §7.3): `HostContext` classifies `Host` against the host of `PUBLIC_BASE_URL` into `root | tenant(slug, tenant_id?) | admin`, else `421`. Slug → tenant id comes from a `TenantHostResolver` port (implemented by identity in phase 04) behind an in-process cache (positive entries never expire because slugs are immutable; negative entries 60 s; purge evicts). Route groups declare the host kinds they accept (`httpx.OnHosts(root|tenant|admin)`), mismatch → `404`. `TenantHostGuard` turns a session whose tenant differs from the host tenant into `Unauthenticated`, exactly like a missing cookie.

```go
// platform/urls — the only place that builds absolute URLs (mail links, redirects)
type Builder struct{ base *url.URL }          // from PUBLIC_BASE_URL
func (b Builder) Root(path string) string
func (b Builder) Tenant(slug, path string) string   // https://<slug>.<host>[:port]/path
func (b Builder) Admin(path string) string
```

## Related files

| Path | Action |
|---|---|
| `apps/api/internal/platform/config/config.go` | create — typed env, validation, `APP_ENV` |
| `apps/api/internal/platform/db/{db.go,tx.go,errors.go,version.go}` | create — open pool (pgx stdlib, max conns, statement timeout), tx helpers, `IsUniqueViolation(name)`, `UpdateWithVersion` helper |
| `apps/api/internal/platform/{clock,ids,textnorm,money}/…` | create + unit tests (textnorm table incl. `Đường Phèn → duong phen`, money scale/rounding) |
| `apps/api/internal/platform/apperr/apperr.go` | create |
| `apps/api/internal/platform/httpx/{engine.go,render.go,pagination.go,etag.go,bind.go,command_router.go,hosts.go}` | create — cursor encode/decode (base64 of `(sort_key,id)`), `If-Match` → expected version; `engine.SetTrustedProxies(TRUSTED_PROXY_CIDRS)` (the `homelab` network subnet where Traefik runs; empty in dev) with `engine.RemoteIPHeaders = []string{"CF-Connecting-IP"}`, so `ClientIP()` takes the Cloudflare header only from Traefik and the peer address otherwise (no `TrustedPlatform`, which would trust the header from anyone); `hosts.go` = `OnHosts` route-group guard |
| `apps/api/internal/platform/middleware/{request_id.go,logger.go,recover.go,host_context.go,ratelimit.go,session.go,tenant.go,csrf.go,entitlement.go,support_readonly.go}` | create (session/entitlement/tenant-host call ports implemented in phases 04/13); `host_context.go` + table tests for apex, `www`, `admin`, slug, deeper labels, foreign host, IP literal, port suffix |
| `apps/api/internal/platform/urls/builder.go` + test | create — `Root/Tenant/Admin`, keeps scheme and port (`http://demo.localhost:5173` in dev) |
| `apps/api/internal/platform/idempotency/guard.go` | create — insert-first inside the posting tx, compare `request_hash` (SHA-256 of method+path+canonical body), replay stored response; only committed 2xx responses are stored — a failed command (4xx/5xx) rolls back the key row, so the client may retry with the same key |
| `apps/api/internal/platform/docseq/issuer.go` | create — `INSERT … ON CONFLICT DO NOTHING` + `UPDATE … RETURNING next_value - 1`; format `PREFIX-NNNN` (zero-padded to 4, grows past 9999) per ubiquitous-language §5 |
| `apps/api/internal/platform/audit/recorder.go` | create — redaction list |
| `apps/api/internal/platform/outbox/{publisher.go,dispatcher.go}` | create — `FOR UPDATE SKIP LOCKED`, batch 100, exponential retry, dead state after 10 tries |
| `apps/api/internal/platform/periodlock/guard.go` | create |
| `apps/api/internal/platform/objectstore/{store.go,s3.go}` | create — minio-go; presign PUT/GET ≤ 10 min; content-type allow list; max size from config |
| `apps/api/internal/platform/mailer/{mailer.go,smtp.go,templates/}` | create |
| `apps/api/internal/platform/logging/logging.go` | create — slog JSON, redact keys (`password`, `token`, `secret`, `otp`, phone/email masking) |
| `apps/api/internal/platform/metrics/metrics.go` | create — prometheus client, `/metrics` bound to internal listener |
| `apps/api/internal/posting/{coordinator.go,command.go,locks.go,tx.go}` + tests | create |
| `apps/api/internal/app/{app.go,routes.go}` | create — composition root; route groups `/api/v1` (root/tenant hosts per route) and `/api/admin/v1` (admin host), bound with `httpx.OnHosts` |
| `apps/api/internal/cli/{serve.go,worker.go,migrate.go,seed.go,tenant.go,ledger.go,maintenance.go}` | create (ledger/tenant bodies filled by later phases; commands registered now with `not implemented` errors replaced phase by phase) |
| `apps/api/internal/testkit/{postgres.go,fixtures.go,clock.go,http.go}` | create — testcontainers-go postgres:16-alpine, apply migrations once, create roles, connect as `tally_app` |
| `apps/api/api/openapi.yaml` | create — info, servers, security schemes (cookie + csrf header), shared schemas: `Error`, `Money`, `Qty`, `Cursor`, `DocNo`, `ColorCode` |
| `apps/api/internal/app/openapi_contract_test.go` | create — every gin route present in spec and vice versa |

## Implementation steps

1. `config`: struct with `env` tags; fail fast listing every missing variable; never log values.
2. `db`: pool, tx helpers with `defer rollback`, panic-safe; `set_config` executed as the first statement; integration test proves a query without `WithTenantTx` returns zero rows on an RLS table (uses a temp test table created by the test migration fixture).
3. Value objects (`money`, `ids` UUIDv7, `clock`, `textnorm`) with exhaustive table tests and property tests (`testing/quick`) for `BaseQty` and rounding.
4. `apperr` + `httpx.RenderError`; JSON binding that rejects unknown fields and numbers for money fields. `httpx.CommandRouter` maps `POST /{resource}/{id}:{verb}` and `POST /{resource}:{verb}` to registered handlers (gin cannot route a `:` suffix); unit test for unknown verb → 404 and id parsing.
5. Middleware chain; session/tenant/entitlement/host depend on interfaces (`SessionResolver`, `EntitlementReader`, `TenantHostResolver`) implemented later — wire fakes in tests. Tests: cookie of tenant A on host B → 401 with the same body as no cookie; unknown slug → same 401; admin group on a tenant host → 404; tenant group on root/admin host → 404. `urls.Builder` unit tests for prod and dev bases.
6. `idempotency.Guard`, `docseq.Issuer`, `periodlock.Guard`, `audit.Recorder`, `outbox.Publisher` — each takes `db.Querier`, so it joins the caller's transaction.
7. `posting.Coordinator.Post`: sort lock keys by the global order, `SELECT … FOR UPDATE` via `Locker` callbacks registered by domains (`RegisterLocker("sku_stock", fn)`), run `Apply`, record audit, publish outbox events, persist idempotent response, commit. Concurrency test: two goroutines posting with the same key → one applies, the other replays.
8. `outbox.Dispatcher` + `tally worker` loop with graceful shutdown (SIGTERM), handler registry (`Subscribe(eventType, handler)`), metrics for lag.
9. `objectstore` + `mailer` adapters with integration tests against dev MinIO/Mailpit (build tag `integration`).
10. OpenAPI skeleton and contract test; `pnpm gen:api` script placeholder in web (filled in phase 14).
11. CLI: `serve` (graceful shutdown, read/write timeouts), `worker`, `migrate` (wraps golang-migrate with `iofs`; production guard on `--backup-file` age ≤ 1 h and `MIGRATE_DATABASE_URL` required), `healthcheck`.

## Todo

- [x] config, logging, metrics
- [x] db pool + WithTenantTx/WithPlatformTx/WithReadOnlyTenantTx + version helper
- [x] money/qty/factor/percent, clock, ids, textnorm with tests
- [x] apperr + httpx (render, bind, pagination, etag)
- [x] middleware chain with interface seams
- [x] idempotency guard, docseq issuer, periodlock guard, audit recorder
- [x] outbox publisher + dispatcher + worker command
- [x] posting coordinator + lock ordering + replay test
- [x] objectstore (S3/MinIO) + mailer (SMTP) adapters
- [x] testkit (postgres container, fixtures, fixed clock)
- [x] openapi.yaml skeleton + contract test
- [x] CLI serve/worker/migrate/healthcheck with production backup guard

## Success criteria & verification

- `make -C apps/api test` (unit) and `make -C apps/api test-integration` green; coverage of `platform/money`, `docseq`, `idempotency`, `posting` ≥ 90 %.
- Concurrency tests: 50 parallel `docseq.Next` calls produce 50 distinct consecutive numbers; duplicate idempotency key under parallel load applies once.
- `tally migrate up` with `APP_ENV=production` and no `--backup-file` exits non-zero with a clear message.
- `go test ./internal/app -run TestOpenAPIContract` passes.
- `go test ./internal/platform/middleware -run 'TestHostContext|TestTenantHostGuard'` and `./internal/platform/urls` pass.
- `golangci-lint run` clean, including depguard rule.

## Risks

| Risk | Mitigation |
|---|---|
| Forgetting `WithTenantTx` in a code path | Repos accept only `db.Querier`; services receive it from `WithTenantTx`; RLS returns zero rows if unset; isolation tests in every domain phase |
| Deadlocks between postings | Global lock order enforced by `Locker` sort; deadlock retry (SQLSTATE 40P01) once with jitter inside Coordinator |
| Idempotency table growth | `maintenance purge-idempotency` scheduled in worker daily |

## Security

- Session and tenant resolved only from the cookie; request bodies and `Host` cannot set `tenant_id` (host is a cross-check only, mismatch = unauthenticated, no disclosure of whether the slug exists). Logs redacted. Rate limiting on IP and on auth routes (tighter). `/metrics` on an internal port, not routed by Caddy.

## Rollback

Code-only phase; revert commits. No schema yet besides what phase 03 introduces.

## Next

Phase 03 creates all migrations; phase 04 implements session/tenant resolvers behind the middleware seams.
