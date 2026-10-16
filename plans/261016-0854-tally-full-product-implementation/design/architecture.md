# Architecture — Tally monorepo (apps/api, apps/web, infra)

Inherits brainstorm §6 (Go 1.25 + gin + sqlx + pgx v5 + shopspring/decimal + golang-migrate/iofs + cobra; React + Vite + Tailwind + shadcn/ui + TanStack Query + React Router) and the Sidecup infra layout (`/home/cesc/Documents/personal-workspace/sidecup/infra`). Vocabulary: [ubiquitous-language.md](ubiquitous-language.md). Database rules: [database-conventions.md](database-conventions.md). UI source of truth: `docs/design/prototype-v6.14.html` + [UI spec report](../../reports/design-261016-1549-prototype-ui-spec.md).

## 1. Repository layout

```
tally/
├── Makefile                      dev, db-up, db-backup, migrate-*, seed, test, lint, build, e2e, prod-*, homelab-*
├── README.md
├── apps/
│   ├── api/                      Go module github.com/<owner>/tally/apps/api
│   │   ├── Dockerfile            golang:1.25-alpine build → gcr.io/distroless/static:nonroot
│   │   ├── Makefile              run, worker, migrate-up/down, seed, test, test-integration, lint
│   │   ├── cmd/tally/main.go     cobra root only
│   │   ├── internal/
│   │   │   ├── app/              composition root: wires config → db → repos → services → http
│   │   │   ├── cli/              serve, worker, migrate, seed, tenant, ledger, maintenance, healthcheck
│   │   │   ├── platform/         technical kernel, no business words (§3)
│   │   │   ├── <domain>/         org identity partner catalog pricing inventory procurement sales
│   │   │   │                     kits debt cash campaign crm notification reporting saas
│   │   │   ├── posting/          cross-domain posting coordinator (§4.3)
│   │   │   └── testkit/          pg container helpers, fixtures builders, fixed clock
│   │   └── migrations/           NNNNNN_<domain>_<subject>.{up,down}.sql + migrations_lint_test.go + embed.go
│   └── web/
│       ├── Dockerfile            node:22-alpine pnpm build → nginx:1.27-alpine
│       ├── nginx.conf            SPA fallback, cache headers, /api proxy only in homelab
│       ├── package.json          pnpm, React 19, react-router 7, TanStack Query 5
│       ├── vite.config.ts  vitest.config.ts  playwright.config.ts  components.json
│       ├── e2e/                  Playwright journeys (AT-01..18)
│       ├── public/               manifest.webmanifest, icons, sw (phase 19)
│       └── src/                  (§5)
├── infra/
│   ├── docker-compose.yml        dev: postgres:16-alpine, mailpit, minio (+ createbuckets job)
│   ├── docker-compose.prod.yml   postgres, api, worker, web (no proxy, no published ports)
│   ├── docker-compose.homelab.yml  production overlay: external `homelab` network, Traefik labels + header middleware (§7.6)
│   ├── postgres/init/01-roles.sh   runs psql -v with passwords → postgres/roles.sql (roles, grants; no functions)
│   ├── backups/                  pg_dump output (git-ignored)
│   └── .env.example              every variable, no secrets
├── docs/                         design/prototype-v6.14.html, runbook.md, architecture docs
└── plans/
```

## 2. Runtime topology

| Process | Binary / image | Role |
|---|---|---|
| `api` | `tally serve` | HTTP API under `/api/v1`, platform admin under `/api/admin/v1`, health `/healthz`, `/readyz` |
| `worker` | `tally worker` | outbox dispatcher, notification sweeps, expiry roll-forward, export jobs, SaaS lifecycle, idempotency purge |
| `web` | nginx static | one SPA + PWA bundle on every host; route tree chosen by host kind (§7.1): tenant app on `<slug>.DOMAIN`, sign-up/store lookup on apex, platform admin on `admin.DOMAIN` |
| edge (outside this repo) | Cloudflare Tunnel `cloudflared-sidecup` + homelab Traefik v3 | TLS at Cloudflare; Traefik routes `/api/*` → api:8080, rest → web:80 by `Host`, adds security headers (§7.6) |
| `postgres` | postgres:16-alpine | only data store; not published on prod |
| `minio` (dev) / S3-compatible (prod, e.g. R2) | — | attachments, images, exports |
| `mailpit` (dev) / SMTP (prod) | — | password reset, MFA, export-ready mail |

Production sets `MIGRATE_ON_START=false` (differs from Sidecup on purpose): `make prod-migrate` runs `db-backup` then `tally migrate up --backup-file <dump>` (conventions §8).

## 3. Backend — `apps/api`

### 3.1 Layers per domain package

```
internal/catalog/
  model.go        entities + value objects, invariants as methods (no SQL, no gin)
  ports.go        interfaces this domain needs (Repository, UsageReader, Clock …) — consumer-owned
  repo.go         sqlx handwritten SQL implementing ports against db.Querier
  service.go      use cases; takes ports in constructor; opens no transactions itself
  http.go         gin handlers: bind DTO → call service → map to response; RegisterRoutes(rg *gin.RouterGroup)
  dto.go          request/response structs + validation tags; mapping funcs model↔dto
  errors.go       domain error codes wrapping apperr kinds
  *_test.go       model/service unit tests with fakes; repo_integration_test.go (build tag integration)
```

Dependency rule: `http → service → model`; `service → ports`; `repo implements ports`; `app` wires concrete types. A domain imports another domain only through a port interface declared in its own `ports.go` (Dependency Inversion); `posting` is the only package that calls several domain services inside one transaction. No package imports `internal/app`.

### 3.2 Platform kernel (`internal/platform`)

| Package | Responsibility |
|---|---|
| `config` | env parsing (`envconfig`-style struct, fail fast on missing secrets), `APP_ENV` |
| `db` | `Open` (pgx v5 stdlib + sqlx), `Querier` interface (`sqlx.ExtContext`), `WithTenantTx`, `WithPlatformTx`, `WithReadOnlyTenantTx` (`SET LOCAL ROLE tally_readonly` for support), `IsUniqueViolation(constraint)` |
| `httpx` | gin engine factory, JSON error renderer, cursor pagination helpers, `ETag`/`If-Match` ↔ version, `CommandRouter` for `{id}:{verb}` routes |
| `middleware` | request id, structured logging, recovery, CORS (dev only), host context (§7.3), session auth, tenant context + tenant-host guard, CSRF (double submit), rate limit, entitlement guard, support-session read-only guard |
| `apperr` | kinds: `Validation`, `NotFound`, `Conflict`, `Forbidden`, `Unauthenticated`, `PreconditionFailed`, `ExceptionRequired`, `PeriodLocked`, `ReadOnlyTenant`; stable `code` strings |
| `clock` | `Clock` interface, `System`, `Fixed`; `BusinessDate(tz)` |
| `ids` | UUIDv7 generator |
| `textnorm` | `Key(name)` (diacritics, `đ→d`, lowercase, collapse spaces), `SearchNorm` |
| `money` | `Money`, `Qty`, `Factor`, `Percent` over `decimal.Decimal`; `RoundVND`; scale guards (≤4 decimals) |
| `docseq` | `Issuer.Next(ctx, q, prefix, businessDate)` — gap-free per tenant/prefix |
| `idempotency` | `Guard.Run(ctx, q, scope, key, hash, fn)` |
| `audit` | `Recorder.Record(ctx, q, Event)` writing `audit_events` (before/after jsonb, reason) |
| `outbox` | `Publisher.Publish(ctx, q, Event)` in-tx; `Dispatcher` used by worker with `FOR UPDATE SKIP LOCKED` |
| `objectstore` | `Store` interface (`Put`, `PresignGet`, `Delete`); `s3` adapter (minio-go) for MinIO/R2 |
| `mailer` | `Mailer` interface; SMTP adapter; template rendering |
| `authn` | argon2id hashing, opaque session tokens (SHA-256 stored), TOTP, recovery codes |
| `logging` | `slog` JSON handler, redaction of secrets/PII |
| `periodlock` | `Guard.Check(ctx, q, businessDate)` against `period_locks` |

### 3.3 Domain packages and their aggregates

| Package | Aggregates / services (ubiquitous names) |
|---|---|
| `org` | Tenant, Store, Warehouse, TenantSettings, `TenantProvisioner` (seed via Go), `PeriodLockService` |
| `identity` | User (exactly one active owner per tenant, SEC-01), Session, Device, MFA, PasswordReset, OwnershipTransfer (ORG-07), ReAuth for sensitive actions (SEC-06), `Authorizer` (session ↔ tenant ↔ entitlement) |
| `partner` | Party (customer/supplier/both), CustomerGroup, Contact, Address, `PartyDeduplicator` |
| `catalog` | Color, Unit, TaxRate, Attribute, AttributeSet, Category, Product, VariantAxis, Sku, SkuUnit, Barcode, MixGroup, `VariantMatrixFactory`, `AttributeValidator`, `SearchIndexer`, `EditRules` |
| `pricing` | PriceList, PriceListItem, PriceChange, Promotion, `PriceResolver`, `PromotionEngine` |
| `inventory` | Lot, StockMove, LotBalance, SkuStock, Reservation, StockCount, StockAdjustment, `MovingAverageCosting`, `LotPicker`, `StockLedger` |
| `procurement` | PurchaseOrder, Receipt, LandedCostAllocation, SupplierDocument, PurchaseReturn, PurchasePriceAdjustment |
| `sales` | Quote, Invoice(+versions), StockOut, SalesDocument, Shift, PosCart, PosSyncException, SalesReturn, Exchange, `SalesPricingCalculator` |
| `kits` | KitRecipe, AssemblyOrder, AssemblyPosting, Disassembly, LotGenealogy, `KitAvailability` |
| `debt` | Obligation, Schedule, Payment, CreditNote, DebtAdjustment, DebtOffset, Settlement, Statement, CollectionNote, `AllocationStrategy` |
| `cash` | FundAccount, PaymentMethod, CashEntry, `CashBook` |
| `campaign` | Campaign, Attribution, ChecklistItem |
| `crm` | CustomerActivity, CareTask, Consent, LoyaltyProgram, LoyaltyEntry |
| `notification` | Notification, `Producer` per kind, `Sweeper` |
| `reporting` | read models: Dashboard, NXT, Revenue, GrossMargin, DebtAging, CashFlow, KitReport, SeasonReport, SavedReport, ExportJob |
| `saas` | Plan, Subscription, Entitlement, SaasInvoice, PaymentEvent, SupportTicket, SupportSession, FeatureFlag, Usage, AccountClosure, Incident, Partner |

### 3.4 Design patterns (where and why)

| Pattern | Where | Why (SOLID) |
|---|---|---|
| Repository + `db.Querier` | every `repo.go` | services depend on interfaces (DIP); tests use fakes; same repo works inside any tx |
| Unit of Work | `db.WithTenantTx` around a service call; `posting.Coordinator` | one transaction per command, RLS context set once |
| Template Method / Command | `posting.Coordinator.Post(ctx, cmd PostingCommand)`: period lock → idempotency → doc number → lock rows (global order) → `cmd.Apply(tx)` → projections → audit → outbox → commit | each document type implements `PostingCommand` (`Validate`, `Apply`, `Reverse`); new documents add a type, not branches (OCP) |
| Chain of Responsibility + Strategy | `pricing.PriceResolver` = ordered `PriceListSource` chain (customer list → customer-group list → active season/campaign list → base retail/wholesale list), then `ListRowMatcher` inside the first matching list (exact color beats any-color, exact unit beats base × factor, highest tier `min_qty` ≤ base qty); `PromotionEngine` applies discounts afterwards; manual price is an explicit user override | order fixed by catalog-pricing §5.1 and PRD §7.3; add a source without touching others (OCP) |
| Strategy | `inventory.CostingMethod` (`MovingAverageCosting`), `inventory.LotPicker` (`ExactColorLot`, `FefoLot`), `debt.AllocationStrategy` (`OldestDueFirst`, `Manual`, `Earmarked`) | swappable policies isolated from services |
| Specification | `catalog.EditRules` (`CanChangeUnitFactor`, `CanRemoveAxis`, `CanChangeAttributeType`) using `catalog.UsageReader` | brainstorm §4.4 rules testable alone |
| Factory | `catalog.VariantMatrixFactory`, `docseq.Issuer`, `debt.ObligationFactory`, `kits.KitLotFactory`, `sales.SalesDocumentFactory` (from StockOut / POS / Exchange) | creation rules in one place |
| State | `sales.InvoiceStatus`, `procurement.PurchaseOrderStatus`, `saas.SubscriptionLifecycle`, `debt.StatementStatus` | explicit transition tables; illegal transitions return `apperr.Conflict` |
| Registry | `catalog.AttributeTypeRegistry` (`text`, `number`, `boolean`, `date`, `select`, `multiselect` → validator); `notification.ProducerRegistry` | add a type by registering (OCP) |
| Observer via transactional outbox | `outbox.Publisher` in tx → `tally worker` dispatch → notification, CRM timeline, usage | side effects never block posting; at-least-once with dedupe keys |
| Adapter | `objectstore/s3`, `mailer/smtp`, `saas/payprovider` | infrastructure behind ports |
| Decorator | middleware chain, `EntitlementGuard` wrapping posting | cross-cutting concerns outside services |
| Value Object | `money.Money`, `Qty`, `catalog.SkuCode`, `catalog.VariantLabel`, `sales.MixKey` | immutable, validated at construction |

Interface segregation: ports are small and consumer-owned, e.g. `sales` declares `StockReserver { Reserve(ctx, q, ReserveRequest) error }` rather than depending on the whole inventory service.

### 3.5 Posting flow (one transaction)

```
HTTP POST /api/v1/invoices/{id}/stock-outs  (Idempotency-Key, If-Match on the invoice)
  → middleware: host context → session → tenant (+ host guard, §7.3) → entitlement (not read_only) → csrf
  → sales.Handler → posting.Coordinator.Post(ctx, sales.PostStockOut{…})
      WithTenantTx:
        periodlock.Check(business_date)
        idempotency.Guard (scope "stock_out.post")
        docseq.Issuer.Next("BH")
        lock sku_stock rows ascending → lots ascending → reservations
        cmd.Apply: inventory.StockLedger.Issue(...)   (stock_moves out, lot_balances, sku_stock, COGS by moving average)
                   sales.SalesDocumentFactory.FromStockOut(...)
                   debt.ObligationFactory.Receivable(...) when unpaid part > 0
                   campaign attribution copy
        audit.Record, outbox.Publish("stock.changed", "obligation.created")
      commit → response stored in idempotency_keys
```

Reversal (`Đảo`) uses the same coordinator with `cmd.Reverse`, writing mirror rows with `reversal_of`; it never updates posted rows.

### 3.6 HTTP API conventions

- Base `/api/v1`, JSON, `snake_case` fields, money/qty as decimal **strings** (no float), dates `YYYY-MM-DD`, timestamps RFC 3339 UTC.
- Resources plural kebab-case: `/skus`, `/price-lists/{id}/items`, `/stock-counts/{id}/lines`, `/sales-returns`.
- Commands use the custom-method suffix: `POST /invoices/{id}:confirm`, `/stock-outs/{id}:reverse`, `/pos/carts/{id}:checkout`; collection-level commands `POST /exchanges:preview`, `/allocations:suggest`. Creating a posted document is a plain `POST` on its collection (`POST /receipts`, `POST /invoices/{id}/stock-outs`). Gin treats `:` as a parameter marker, so each resource registers `POST /invoices/*target` once and `httpx.CommandRouter` splits `{id}:{verb}` and dispatches to the registered handler for that verb (unknown verb → `404`); OpenAPI still documents each `{id}:verb` path.
- List: cursor pagination `?cursor=&limit=` (max 200), filters as query params, response `{data, next_cursor}`.
- Concurrency: response `ETag: "<version>"`; mutations require `If-Match`; mismatch → `412 precondition_failed`.
- Idempotency: every posting/payment endpoint requires `Idempotency-Key`; replay returns the stored body with `Idempotent-Replay: true`.
- Errors: `{"error": {"code": "credit_limit_exceeded", "message": "…", "fields": {…}, "exception_kind": "credit_limit_exceeded"}}`; `ExceptionRequired` (409) lets the UI open the owner-confirmation dialog and retry with `exception_confirmation_id`.
- Auth: opaque host-only session cookie `__Host-tally_session` (HttpOnly, Secure, SameSite=Lax; `tally_session` in dev), CSRF token header `X-CSRF-Token`; the session tenant must match the request host (§7.3); platform admin uses a separate cookie and route group, served only on `admin.DOMAIN`, with mandatory TOTP. Hosts, cookies and URL building: §7.
- OpenAPI 3.1 file `apps/api/api/openapi.yaml` is the contract; `pnpm gen:api` generates TS types into `apps/web/src/shared/api/schema.d.ts` (openapi-typescript). A contract test in Go checks every registered gin route exists in the spec.

### 3.7 CLI (`internal/cli`)

`tally serve | worker | migrate up|down|version|force [--backup-file] | seed dev | tenant create --name --owner-email | ledger verify [--tenant] | ledger rebuild --tenant [--dry-run] | maintenance purge-idempotency | expire-exports | healthcheck`.

### 3.8 Testing strategy

| Level | Tooling | Scope |
|---|---|---|
| Unit | `go test`, table tests, fakes for ports | model invariants, resolver rules, costing math, allocation, state machines |
| Integration | `-tags integration`, testcontainers-go `postgres:16-alpine`, migrations applied once per package | repositories, RLS isolation, posting end-to-end, concurrency (parallel goroutines oversell tests), `ledger verify` |
| Migration | `migrations_lint_test.go`; up → down → up in CI | no procedural SQL; reversible |
| Contract | route ↔ OpenAPI test | API drift |
| Web unit | vitest + RTL + msw | components, hooks, forms, adapters |
| E2E | Playwright against docker stack + seeded tenant | acceptance journeys AT-01..18 |
| Load | k6 script `apps/api/loadtest/` | NFR-03/04 targets |

## 4. Frontend — `apps/web`

### 4.1 Source tree

```
src/
├── main.tsx                      providers: QueryClient, Router, Toaster, ThemeTokens
├── app/
│   ├── router.tsx                route table = MENU of prototype (40 routes) + MOVED redirects
│   ├── layouts/                  AppShell (sidebar accordion, topbar, shortcuts), AdminShell, AuthLayout
│   ├── guards/                   RequireAuth, RequireEntitlement, RequireRecentAuth
│   └── providers/
├── features/
│   ├── auth/  dashboard/  catalog/  pricing/  partners/  procurement/  inventory/
│   ├── sales/  pos/  kits/  debt/  cash/  campaign/  crm/  notifications/
│   ├── reports/  settings/  onboarding/  admin/      (platform SaaS console)
│   └── <feature>/
│       ├── api/                  queries.ts (query-key factory + hooks), mutations.ts, adapters.ts (DTO ↔ view model)
│       ├── components/           presentational, props-only, no fetching
│       ├── containers/           wire hooks → components
│       ├── pages/                route components (one per route)
│       ├── schemas.ts            zod schemas shared by forms and adapters
│       └── index.ts              public surface (routes, types)
├── shared/
│   ├── api/                      http client (fetch wrapper: CSRF, Idempotency-Key, If-Match, error → ApiError), schema.d.ts
│   ├── ui/                       shadcn/radix primitives restyled to tokens + compound components (DataTable, Modal, Drawer, Field, MoneyInput, QtyInput, ColorSwatch, StatusPill, KpiCard, Stepper …)
│   ├── domain/                   ubiquitous types & formatters: Money, Qty, DocNo, Color, status maps, labels vi
│   ├── hooks/                    useIdempotentMutation, useHotkeys, useDebounce, useCursorList, useConfirmException
│   ├── lib/                      format (vi-VN), decimal (decimal.js-light), dates (Asia/Ho_Chi_Minh), textnorm
│   └── styles/                   tokens.css (from prototype :root), tailwind @theme mapping
└── test/                         msw handlers, render helpers
```

### 4.2 Frontend patterns

| Pattern | Use |
|---|---|
| Feature modules | each feature owns api/components/pages; cross-feature imports only through `index.ts` |
| Container / Presentational | containers own queries and mutations; components are pure and story-testable |
| Query-key factory | `skuKeys.list(filters)`, `skuKeys.detail(id)`; invalidation via the factory only |
| Adapter | `adapters.ts` maps API DTOs (decimal strings) into view models (`Money`) and back; UI never touches raw DTOs |
| Registry | `AttributeFieldRegistry`: `text→Input`, `number→NumberInput + unit_label`, `boolean→Switch`, `date→DatePicker`, `select→Combobox`, `multiselect→MultiCombobox` (brainstorm §6) |
| Compound components | `<DataTable>` (`.Toolbar`, `.Filters`, `.Body`, `.Pagination`), `<Modal>` (`.Header`, `.Body`, `.Footer`) matching prototype markup |
| Strategy | POS `PaymentMethodPanel` per method; `PriceSourceBadge` per price source |
| Command | `useIdempotentMutation` creates one key per user intent, reused on retry |
| Repository (offline) | `pos/offline/OutboxRepository` over IndexedDB (phase 19) |

### 4.3 Design fidelity rules

- Tokens: every color, radius, shadow, spacing and font size in prototype `:root` becomes a CSS variable in `tokens.css` and a Tailwind 4 `@theme` entry; components use tokens only (lint rule bans raw hex in `src/features`).
- Markup parity: each screen in the UI spec lists its prototype view id; the page renders the same sections, labels (Vietnamese verbatim), column order, and empty states. Visual regression: Playwright screenshots of prototype view vs. app route at 1440 and 390 widths, diff threshold in phase 20.
- Accessibility: WCAG 2.2 AA; the 18 a11y/UX gaps in the UI spec are fixed in phase 20, not copied.

## 5. Infrastructure

| Compose file | Services | Notes |
|---|---|---|
| `infra/docker-compose.yml` (dev) | `postgres` (5432, init roles), `mailpit` (8025 UI, 1025 SMTP), `minio` (9000/9001) + `minio-init` | API and web run on host (`make dev`), ports fixed: api 8080, web 5173; `make check-ports` reports owners instead of switching ports |
| `infra/docker-compose.prod.yml` | `postgres`, `api`, `worker`, `web` | project name `tally-prod`; one shared stack for all tenants (isolated by `tenant_id` + RLS, no per-customer stacks); no proxy and no published ports; healthchecks via `tally healthcheck` |
| `infra/docker-compose.homelab.yml` | overrides prod | the production deployment: joins api/web to the external `homelab` network, Traefik labels (low-priority apex + wildcard routers, header middleware), postgres alias `tally-postgres`; served through Cloudflare Tunnel (§7.6) |

Secrets come from `infra/.env` (never committed); the tunnel token stays with the tunnel compose outside this repo (§7.6). Database URLs: `DATABASE_URL` (tally_app) for api/worker, `MIGRATE_DATABASE_URL` (tally_owner) only for migrate. Backups: `make db-backup` → `infra/backups/tally-<ts>.dump`; `make db-restore FILE=…`.

## 6. Security baseline

- Passwords argon2id; login rate limit per IP+email (`login_attempts`); account lockout window; TOTP MFA required for owner (optional others) and platform admins.
- Sessions opaque, hashed at rest, idle 12 h / absolute 30 d; revoke on password change.
- Authorization: one owner account per tenant with full rights (SEC-01); the server still checks session, tenant and entitlement on every request, and sensitive actions (ownership transfer, period reopen, write-off, data export, MFA reset) require re-authentication within 5 minutes (SEC-06). Platform admins never act inside a tenant except through a consented read-only support session (SEC-07).
- RLS as defence in depth behind explicit `tenant_id` filters; isolation integration tests per table.
- Uploads: content-type sniffing, size limit, private bucket, presigned GET with short TTL.
- Logs redact tokens, passwords, phone/email beyond last 3 chars.
- CSP and headers as a Traefik middleware (as Sidecup homelab), no third-party scripts; HSTS forced with `includeSubDomains`.
- Tenant hosts: host-only cookies, session tenant must equal host tenant, login scoped to the host tenant with timing parity, unknown slugs indistinguishable from real ones outside sign-up (§7).

## 7. Tenant hosts (subdomain per tenant)

Every tenant has its own subdomain on one shared stack: `https://<slug>.<DOMAIN>`. Production `DOMAIN` is **`sidecup.app`** (Cloudflare zone, served through Cloudflare Tunnel to the homelab, §7.6). The host is a routing and branding aid; **the session stays the only authority for `tenant_id`** (the host is cross-checked, never used to set `app.tenant_id`).

### 7.1 Host map

| Host | Kind | Serves |
|---|---|---|
| `<DOMAIN>`, `www.<DOMAIN>` (→ 301 to apex by a Cloudflare redirect rule) | `root` | Sign-up (`/signup`), "Tìm cửa hàng của bạn" (`/find-store`), public `/status`, SaaS payment webhooks. No tenant session routes |
| `<slug>.<DOMAIN>` | `tenant` | Tenant app and `/api/v1` session routes, login/verify/reset/MFA, PWA/POS offline |
| `admin.<DOMAIN>` | `admin` | Platform admin console (`/admin/*`) and `/api/admin/v1` only |

The same nginx SPA bundle is served on every host. `GET /api/v1/host-context` (public) returns `{kind, root_url, admin_url?}` computed from `Host`; an unknown slug still answers `kind: "tenant"` so the response never reveals whether a store exists. The router mounts the route tree for that kind; `/admin/*` on a non-admin host and tenant routes on root/admin hosts render 404.

### 7.2 Slug rules (`Tenant.Slug`, `tenants.slug`)

- Chosen at sign-up; suggested by `textnorm.Slug(display_name)` (strip Vietnamese diacritics, `đ→d`, non-alphanumerics → `-`, collapse, trim, cut to 30; numeric suffix `-2`, `-3` … when taken).
- Sign-up validation in Go (`org.SlugPolicy`): `^[a-z0-9][a-z0-9-]{1,28}[a-z0-9]$` (3–30 chars), no `--` anywhere (also blocks punycode `xn--`), not in the reserved list (`www, app, api, admin, auth, login, signup, account, billing, pay, mail, smtp, imap, mx, ns1, ns2, ftp, static, assets, cdn, img, files, status, help, support, docs, blog, news, dev, staging, test, e2e, tally`), not in `RESERVED_SLUGS` (env, comma list of hostnames other services already use on the same zone; today Sidecup's `tiendouong`, `tiendouong-api`), not ending in `-demo` (sandbox suffix), not in `tenants.slug` or `retired_tenant_slugs`. The database keeps a looser CHECK (3–40 chars, same alphabet, no `--`) so sandbox slugs fit.
- Sandbox tenants (ORG-06): `<parent-slug>-demo` when `sandbox_of` is set; the standalone dev seed uses `demo`.
- **Immutable** (owner decision): no rename for owners or platform admins, no API or CLI for it: changing an origin strands installed PWAs and unsynced offline sales (§7.5).
- Never reused: `tally tenant purge` inserts the slug into `retired_tenant_slugs` before deleting rows.
- Single DNS label only, so Cloudflare Universal SSL (apex + one wildcard level) covers every tenant host.

### 7.3 Request pipeline

`RequestID → Logger → Recover → HostContext → SecurityHeaders → RateLimit → Session → Tenant (+ TenantHostGuard) → CSRF → Entitlement → SupportReadOnly` (phase 02 owns the order)

- `HostContext` parses `Host` against the host of `PUBLIC_BASE_URL`: apex/`www` → `root`; `admin` → `admin`; one extra label → `tenant` + slug looked up through an in-process cache (`slug → tenant_id`, safe to cache because slugs are immutable; unknown slugs cached negatively for 60 s); anything else (deeper labels, foreign hosts, IP literals) → `421 misdirected_request`.
- Route groups are bound to host kinds: `/api/admin/v1` only on `admin`; tenant session routes only on `tenant`; sign-up, slug check and store lookup only on `root`. A mismatch returns `404`.
- `TenantHostGuard`: `session.tenant_id` must equal the host tenant. A mismatch (or an unknown slug) is answered exactly like a missing session (`401 unauthenticated`), so a stolen or misplaced cookie never works on another store.
- Login on a tenant host matches `email_key` **and** the host tenant. A valid email of another tenant gets the same generic `invalid_credentials` as a wrong password, after the same argon2id work (a dummy hash is verified when no user matches) so timing does not leak membership.
- Client IP: cloudflared adds `CF-Connecting-IP` and Traefik passes it through. `httpx.ClientIP` uses that header only when the direct peer is inside `TRUSTED_PROXY_CIDRS` (the `homelab` network subnet, i.e. Traefik), otherwise the peer address; gin's own trusted-platform shortcut is not used because it trusts the header from anyone. Rate limits, `login_attempts.ip`, `sessions.ip` and audit use that IP.

### 7.4 Cookies, URLs and mail

- Cookies are **host-only** (no `Domain` attribute): prod names `__Host-tally_session`, `__Host-tally_csrf` (tenant hosts) and `__Host-tally_admin_session` (admin host), all `Secure; Path=/`; `SameSite=Lax`. A tenant cookie is never sent to another tenant or to the admin host. Dev (`APP_ENV=dev`, plain http) drops the `__Host-` prefix and `Secure`.
- Sign-up flow: root host creates tenant + owner → verification mail links to `https://<slug>.<DOMAIN>/verify?token=…` → the owner logs in on their own host. No cross-host session hand-off exists.
- "Tìm cửa hàng của bạn": `POST /api/v1/store-lookup {email}` on root always answers `202` and mails the store link when the email exists (rate limited per IP and per email).
- `platform/urls.Builder` derives every absolute URL from `PUBLIC_BASE_URL`: `Root(path)`, `Tenant(slug, path)` (inserts `<slug>.` before the host, keeps scheme and port), `Admin(path)`. Every mail template (verify, password reset, MFA, export ready, closure token, store lookup, ownership transfer) uses it; nothing else builds URLs.
- Dev: `PUBLIC_BASE_URL=http://localhost:5173`; tenant hosts are `http://<slug>.localhost:5173` (`*.localhost` resolves to loopback in Chromium/Firefox, no DNS needed). Vite sets `server.allowedHosts: ['.localhost']` and its `/api` proxy keeps the original `Host`.

### 7.5 PWA and offline

Service worker, IndexedDB outbox and offline cache are per origin, so each tenant host is a separate install with separate storage — isolation by the browser itself. Because slugs are immutable an origin never changes, so no outbox is stranded. The client still keys caches by `tenant_id` from `/me` and wipes on mismatch as defence in depth.

### 7.6 Edge: Cloudflare Tunnel → homelab Traefik

Production runs on the homelab machine; nothing listens on the internet directly.

```
browser ──HTTPS──▶ Cloudflare edge (sidecup.app, *.sidecup.app; Universal SSL)
        ──tunnel──▶ cloudflared-sidecup (network homelab, outbound only)
        ──http────▶ Traefik :80 (entrypoint web) ──▶ tally api:8080 (/api/*) | tally web:80 (rest)
```

- Already running, outside this repo (`/home/cesc/Documents/homelab-infras`): external Docker network `homelab`; Traefik v3 (`proxy/compose.yml`, entrypoint `web` on :80, docker provider with `exposedByDefault: false` and `network: homelab`); the `cloudflared-sidecup` tunnel container, whose ingress lives in the Cloudflare Zero Trust dashboard and points to `http://traefik:80`. Tally only adds labelled containers; it runs no proxy and no TLS of its own.
- TLS terminates at Cloudflare. Universal SSL covers `sidecup.app` and `*.sidecup.app` (one level), which is why slugs are single labels. No origin certificate, no ACME, no Cloudflare API token in this repo; the tunnel token stays with the tunnel compose.
- Tunnel public hostnames (tunnel `cloudflared-sidecup`): `sidecup.app` and `*.sidecup.app` → `http://traefik:80`, ordered after the existing specific hostnames (`tiendouong.sidecup.app`, `tiendouong-api.sidecup.app`) because ingress rules match top-down. Each public hostname gets a proxied CNAME to `<tunnel-id>.cfargotunnel.com`; explicit DNS records keep winning over the wildcard.
- Traefik routers (labels in `docker-compose.homelab.yml`): `tally-api` = ``(Host(`${DOMAIN}`) || HostRegexp(`^[a-z0-9-]+[.]${DOMAIN}$`)) && PathPrefix(`/api/`)`` with `priority=3`; `tally-web` = the same host part `&& !PathPrefix(`/api/`)` with `priority=2`. The explicit low priorities are required: Traefik ranks by rule length by default, so Tally's long wildcard rule would otherwise outrank Sidecup's `Host(`tiendouong.sidecup.app`)` routers and take their traffic. Unescaped dots of `${DOMAIN}` in the regex are harmless: only `sidecup.app` hosts arrive through the tunnel and `HostContext` re-validates the host exactly (§7.3).
- Shared network hygiene (as Sidecup): postgres stays on the project network with alias `tally-postgres` and `DATABASE_URL` uses that alias, because other stacks on `homelab` also have services named `api`, `web`, `postgres`; Traefik routes `/api/` straight to the api container, so nginx in `web` never proxies `/api`.
- Security headers: Traefik middleware `tally-headers` on both routers: HSTS forced (Traefik only sees http) `max-age=31536000; includeSubDomains`, nosniff, `frameDeny`, Referrer-Policy `strict-origin-when-cross-origin`, Permissions-Policy, CSP `default-src 'self'; img-src 'self' data: blob: https://<object-store-host>; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'`, empty `Server`.
- Cloudflare zone: Always Use HTTPS on (zone-wide, Sidecup hosts are already HTTPS); Redirect Rule `www.sidecup.app/*` → `https://sidecup.app/${1}` (301); Cache Rule "bypass cache" for `/api/*` on `sidecup.app` and `*.sidecup.app`; static assets follow origin `Cache-Control` (nginx marks `index.html` and `sw.js` `no-cache`). The zone SSL mode does not affect tunnel origins.
- Shared zone with Sidecup: Sidecup customer stacks already use `sidecup.app` hostnames. Those names are listed in `RESERVED_SLUGS` (§7.2), and before adding any new Sidecup hostname the operator checks that no Tally tenant owns that slug (runbook).
- Known trade-off: Traefik also publishes :80 on the LAN (a homelab-infras choice), so a LAN client could forge `CF-Connecting-IP`. Accepted for a home network; origin hardening (keep or drop the LAN port, firewall) is decided at deploy time.
