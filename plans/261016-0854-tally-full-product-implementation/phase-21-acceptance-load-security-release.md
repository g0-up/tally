---
title: "Phase 21: Acceptance, Load, Security & Release"
status: in-progress
priority: P1
effort: 8d
dependsOn: [phase-13-saas-control-settings, phase-19-pos-offline-pwa, phase-20-ui-ux-review-accessibility]
---

# Phase 21 — Acceptance, Load, Security & Release

## Context links

- PRD §12 (NFR-01..16), §13 (AT-01..18), §16 stages A–F, §17 release gates and Definition of Done
- [database-schema-catalog-pricing.md](design/database-schema-catalog-pricing.md) §3.1 search gate and optional `sku_search_tokens`
- [database-conventions.md](design/database-conventions.md) §1 forbidden objects, §8 backup rule
- [architecture.md](design/architecture.md) §2 processes, observability, §7 tenant hosts (host guard, cookies, Cloudflare edge)
- [phase-01-monorepo-docker-infra.md](phase-01-monorepo-docker-infra.md) backup/restore targets, prod compose
- [phase-13-saas-control-settings.md](phase-13-saas-control-settings.md) tenant restore tooling
- Sidecup reference: `/home/cesc/Documents/personal-workspace/sidecup/infra` (prod compose, homelab overlay, backups) — read `.env.example` only

## Overview

Prove the whole product against the PRD before commercial release: a reproducible reference dataset (NFR-04), k6 load and soak tests with the p95 targets, the NFR-03 search gate (adds `sku_search_tokens` only if it fails), all end-to-end acceptance tests AT-01..18 as Playwright + API scenarios with stored evidence, crash/retry and offline chaos tests, backup hardening and a measured restore drill (RPO ≤ 15 min, RTO ≤ 4 h), tenant-isolation and security review, observability alerts, user/support documentation, and the release checklist mapped to PRD §17.

## Key insights

- Evidence is the deliverable: each AT and NFR gets a stored artifact (Playwright HTML report + trace, k6 summary JSON, `tally ledger verify` output, restore timing log) under `apps/web/e2e/acceptance/evidence/<date>/` (git-ignored, archived as CI artifacts) and a row in `docs/release/acceptance-matrix.md`.
- The reference dataset is generated through domain services (`tally seed load`), not raw inserts, so it obeys every invariant; `tally ledger verify` must pass on it before any measurement.
- NFR-04 measures "one owner with 2–3 devices" per tenant across the target tenant count; the count is a parameter (`LOAD_TENANTS`, default 50) until OPEN-07 gives a real number.
- Search gate (NFR-03): if `GET /search` p95 > 1 s on 20 000 SKUs with LIKE on normalised keys, add migration `sku_search_tokens` (catalog-pricing §3.1) with Go-maintained tokens in the same tx as SKU writes and switch `catalog.SearchQuery` to the token strategy (Strategy pattern, no API change). Still no extension, function or trigger.
- RPO ≤ 15 min cannot be met by daily `pg_dump` alone: prod adds continuous WAL archiving (`archive_mode = on`, `archive_command` running `wal-g wal-push` to the S3/R2 bucket) plus nightly base backups; these are PostgreSQL server settings and a sidecar container, not database functions.
- AT-12 follows the OPEN-14 default (manual onboarding wizard); AT-14 follows the OPEN-15 default (full data export job). If the owner changes either decision, only these two scenarios change.
- Crash mid-transaction (AT-11) is simulated by killing the API container during a posting with a test-only fault hook (`TALLY_FAULT_INJECT` honoured only when `APP_ENV=test`), then retrying with the same idempotency key.

## Requirements

| ID | Behaviour |
|---|---|
| NFR-01 | Automated cross-tenant suite over API, export, file URL, cache, worker, support paths and tenant hosts (AT-09) |
| NFR-02 | Ledger verify clean after every AT and after load/soak; no duplicated or lost outbox events |
| NFR-03 | Lookup p95 ≤ 1 s, posting p95 ≤ 2 s on the reference load |
| NFR-04 | Reference tenant 20 000 SKU, 1 000 000 moves; 2–3 concurrent devices per tenant over `LOAD_TENANTS` tenants |
| NFR-05 | Interactive reports p95 ≤ 5 s; large exports async with progress/cancel |
| NFR-06 | Uptime probe and SLO dashboard (99.9 % target, not an SLA) |
| NFR-07 | Measured restore drill: RPO ≤ 15 min, RTO ≤ 4 h, reconciliation after restore (AT-15) |
| NFR-08 | TLS, secrets from env/secret store, MFA, rate limits, dependency and image scans, penetration-style review |
| NFR-09 | Request/correlation IDs, metrics, PII-free logs, alerts for stuck jobs, ledger drift, webhook errors |
| NFR-10 | Browser/device matrix run (two latest Chrome/Edge/Firefox/Safari; Android and iOS real devices); certified printer/scanner list |
| NFR-11 | Accessibility results from phase 20 re-run on the release build |
| NFR-12/16 | Privacy review: data inventory, consent flags default off, AI and analytics off by default, retention policy draft |
| NFR-13 | Pagination and quota checks on every list endpoint; job queue limits per tenant |
| NFR-14 | Upgrade rehearsal: backup → migrate → verify → rollback on a copy of prod-like data |
| NFR-15 | Property tests for money, quantity, allocation and costing invariants present and green |
| AT-01..18 | All PRD acceptance scenarios automated and passing with evidence |
| §17 | Release checklist signed: tests, reconciliation, CSV/audit/pagination, no high-severity isolation/data/finance bugs, user guide, support checklist, alerts, pilot confirmation |

## Architecture

```
apps/api/internal/cli/seed_load.go     `tally seed load --tenants N --skus 20000 --moves 1000000 --seed 42`
apps/api/internal/platform/fault/      test-only fault hook (compiled in, active only when APP_ENV=test)
apps/api/test/isolation/                cross-tenant matrix tests (every route × foreign id)
apps/web/e2e/acceptance/               at-01-*.spec.ts … at-18-*.spec.ts, fixtures/, evidence/ (ignored)
load/k6/                               lookup.js, posting.js, reports.js, pos-devices.js, soak.js, thresholds.json
infra/docker-compose.backup.yml        wal-g sidecar + base backup schedule (overlay on prod/homelab compose)
infra/scripts/restore-drill.sh         timed restore into scratch stack + ledger verify + report
docs/release/acceptance-matrix.md      AT/NFR → evidence link, status, date
docs/release/release-checklist.md      PRD §17 gates
docs/user-guide/                       owner guide (Vietnamese): POS, invoices, nhập hàng, công nợ, báo cáo, onboarding
docs/support/support-checklist.md      triage, support session rules, ledger drift procedure, incident comms
```

AT mapping to stages (PRD §16): A → AT-09, AT-10, AT-15; B → AT-01, AT-02, AT-06, AT-07, AT-16, AT-18; C → AT-03, AT-04, AT-05, AT-08, AT-17; D → report-vs-ledger checks (phase 12 criteria); E → AT-11, AT-12, AT-14 plus load/restore/security; F → RPT-10/11 checks with AI disabled by default.

## Related files

| Path | Action |
|---|---|
| `apps/api/internal/cli/seed_load.go` + test | create |
| `apps/api/internal/platform/fault/*.go` | create |
| `apps/api/test/isolation/*_test.go` | create |
| `apps/web/e2e/acceptance/*.spec.ts`, `fixtures/*` | create |
| `load/k6/*.js`, `load/k6/thresholds.json`, `load/README.md` | create |
| `infra/docker-compose.backup.yml`, `infra/postgres/postgresql.prod.conf` | create — WAL archiving sidecar and settings |
| `infra/scripts/restore-drill.sh` | create |
| `Makefile` | modify — `load-seed`, `load-test`, `soak-test`, `acceptance`, `restore-drill`, `security-scan` |
| `.github/workflows/ci.yml`, `.github/workflows/release.yml` | modify/create — acceptance job (nightly), scans, image signing |
| `apps/api/migrations/0000NN_sku_search_tokens.{up,down}.sql` | create **only if** the search gate fails |
| `apps/api/internal/catalog/search.go` | modify **only if** the search gate fails — token strategy |
| `docs/release/*.md`, `docs/user-guide/*.md`, `docs/support/*.md`, `docs/runbook.md` | create/modify |

## Implementation steps

1. Reference dataset: `tally seed load` provisions tenants and posts realistic documents (receipts with lots/colors, POS sales, invoices with PX, returns, payments, counts, kits) through domain services until 20 000 SKUs and 1 000 000 moves; deterministic by seed; ends with `tally ledger verify`.
2. k6 scenarios with thresholds: lookup (search, SKU detail, POS bootstrap, party lookup) p95 < 1000 ms; posting (POS checkout, receipt, payment, stock out) p95 < 2000 ms; reports p95 < 5000 ms; `pos-devices.js` simulates 3 devices per tenant selling the same last units of a color (AT-03 under load); error rate < 0.5 % excluding expected `409`.
3. Run on the prod-like compose (same images, homelab overlay behind a local Traefik, same Postgres settings) on a host sized like the target; record CPU/memory/DB metrics; tune indexes/queries; repeat until thresholds pass. Soak: 4 h peak-season mix with the worker running; no memory growth > 10 %, no stuck outbox rows, ledger verify clean.
4. Search gate: evaluate step 2 results for `/search`; if failing, implement `sku_search_tokens` migration + token maintenance + strategy switch, re-run, record both results.
5. Acceptance specs AT-01..18 in Playwright using the UI for user-visible steps and the API for setup; every spec ends with API assertions on stock by color, obligations, payments, fund balances and `tally ledger verify --tenant`.
6. Chaos/offline: AT-11 kills the API during a posting (fault hook), restarts, retries with the same key (one document, all-or-nothing); offline POS sync repeated three times posts once; an offline sale with insufficient stock lands in the exception queue.
7. Isolation suite: generated from the OpenAPI route list, each route called with a foreign tenant's ids, export ids, attachment URLs and support sessions; expects `404`/`403` and audit where defined; worker jobs asserted to set `app.tenant_id` per job. Host matrix (AT-09, run as two stores with two owners because one account belongs to exactly one store by owner decision; a second store for the same email is not supported): A's session cookie replayed on `B.DOMAIN` → same `401` as no cookie; A's owner logging in on `B.DOMAIN` → generic `invalid_credentials` with timing within ±10 % of a wrong password; unknown slug answers identically to a real one on login, `/me` and `host-context`; `/api/admin/v1` on a tenant or root host and tenant routes on `admin.DOMAIN` → `404`; a forged `Host` with a deeper label or foreign domain → `421`; Playwright confirms the browser never sends A's cookie to B (host-only `__Host-` cookie).
8. Backup hardening + restore drill: WAL archiving sidecar, nightly base backup, `restore-drill.sh` restores to a scratch stack at a chosen timestamp, measures RPO (last archived WAL vs incident time) and RTO (start → API healthy + ledger verify done), runs `tally tenant restore-check`, writes the report.
9. Security: `govulncheck`, `gosec`, `npm audit --omit=dev`, Trivy image scan, secret scan (gitleaks) in CI; manual review checklist (authn/session, CSRF, rate limits, MFA, re-auth paths, RLS default deny, `tally_readonly` support role, signed URLs, webhook signatures, upload type/size, headers/CSP, PII in logs, host-only cookies and HSTS `includeSubDomains`, real client IP behind Cloudflare Tunnel + Traefik in rate limits/audit (`CF-Connecting-IP` honoured only from `TRUSTED_PROXY_CIDRS`), no published Tally port, Traefik router priorities leave other stacks' hosts alone); origin hardening decided at deploy time and recorded in the runbook: whether Traefik keeps its LAN :80 port and whether a host firewall is added (the tunnel needs no inbound internet port); fix all high/critical findings.
10. Observability: alerts for edge reachability (external probe of `https://sidecup.app/api/v1/host-context` and one tenant host failing for 3 min, which covers tunnel, Traefik and api), outbox lag > 5 min, failed jobs, ledger verify drift (nightly job), 5xx rate, p95 latency breach, backup/WAL archive age > 15 min, webhook failures; dashboards for SLO.
11. Upgrade rehearsal (NFR-14): restore prod-like backup, `make prod-migrate`, verify, then rollback path (down migration or restore) timed and documented.
12. Browser/device matrix run and printer/scanner certification list (thermal 80 mm receipt, A4/A5 documents, USB/Bluetooth barcode scanner) recorded in `docs/release/device-matrix.md`.
13. Docs: user guide in Vietnamese, support checklist, runbook updates (deploy, backup/WAL, restore, incident, ledger drift), acceptance matrix, release checklist; README links.
14. Release: tag build, signed images, run the production checklist from `docs/runbook.md` (tunnel public hostnames `sidecup.app` + `*.sidecup.app` after the specific Sidecup ones, proxied CNAMEs to the tunnel, `www` redirect, `/api/*` cache bypass, `RESERVED_SLUGS` current with Sidecup hostnames), deploy the shared production stack on the homelab, confirm `https://tiendouong.sidecup.app` still reaches Sidecup, create each pilot tenant with `tally tenant create --slug <agreed-slug>` and confirm `https://<slug>.sidecup.app` loads, pilot owner walkthrough of AT-01/02/06/16 on their data, sign-off recorded; marketing copy states the limits (one account, one warehouse, no delivery).

## Todo

- [x] Reference dataset generator + verify
  - Evidence: `tally seed load` with integration tests `TestSeedLoadCommand`, `TestSeedLoadIsDeterministic`, `TestSeedLoadThroughput`; reviewed in `plans/reports/code-reviewer-261019-0915-phase21-go-review.md`; full Go gate green on the latest code (2026-10-19, lead: gofmt, vet incl. `-tags integration`, golangci-lint 0 issues, `go test -race ./...`, integration for every package). Full 20k SKU / 1M move dataset not generated yet (about 95 moves/s, roughly 3 h).
- [ ] k6 lookup/posting/reports/devices scenarios + thresholds (5 scripts + `thresholds.json` written, `k6 inspect` ok; never run against a stack)
- [ ] Tuning loop + soak test (not run)
- [ ] Search gate decision (+ tokens only if needed) (earlier benchmark p95 202 ms at service level on dev, no token table needed; not re-run after the benchmark's fail-assert change)
- [ ] AT-01..18 Playwright/API specs with evidence (18 specs written, `--list` ok; none run; AT-15 depends on the restore drill)
- [ ] Chaos and offline replay tests (fault hook + `TestPostingThatFailsBeforeCommitIsAllOrNothing` green in the full Go gate on 2026-10-19; AT-11 e2e and offline replay not run)
- [x] Cross-tenant isolation suite
  - Evidence: `TestTenantIsolationAcrossEveryRoute` (370 operations, 394 calls; 220/220 calls with tenant B ids return 404), `TestAdminAPIRefusesTenantSessionsAndHosts`, `TestHostBoundaries`, login timing, worker and service-role tests; run by the reviewer (`plans/reports/code-reviewer-261019-0915-phase21-go-review.md`) and green again in the full Go gate on the latest code (2026-10-19, lead). Browser host-only cookie check in AT-09 not run.
- [ ] WAL archiving + restore drill with measured RPO/RTO (wal-g sidecar + `restore-drill.sh` written, shellcheck and compose config ok; drill not re-run after the non-root change; images not built)
- [ ] Security scans + manual review + fixes (govulncheck 0 affecting, gosec, pnpm audit, gitleaks clean and manual API checklist done earlier; full scan incl. Trivy on images not re-run after latest changes; open weak points: CSP `unsafe-inline` styles, no HSTS preload, per-instance rate limiter, webhook signature without timestamp)
- [ ] Alerts and SLO dashboard (13 alert rules, promtool tests pass; no Prometheus/Alertmanager deployed, no uptime probe or SLO dashboard)
- [ ] Upgrade rehearsal (runbook section written, not run)
- [ ] Browser/device/printer matrix (certified peripherals table empty; not run)
- [x] User guide, support checklist, runbook, acceptance matrix
  - Evidence: `plans/reports/docs-manager-261019-0640-phase21-release-user-docs.md`: 11 user-guide pages, support checklist, acceptance matrix, release checklist; 217 links checked, 0 errors; CLI commands and UI labels checked against source. Owner inputs still pending: support hours, plan pricing, certified device list.
- [ ] Pilot release + sign-off (not started)

## Success criteria & verification

- `make acceptance` runs AT-01..18 green on a clean stack; the acceptance matrix links evidence for every row.
- `make load-test` passes `thresholds.json` (lookup p95 ≤ 1 s, posting p95 ≤ 2 s, reports p95 ≤ 5 s) on the reference dataset; `make soak-test` passes with ledger verify clean.
- Search gate outcome recorded with numbers; `sku_search_tokens` exists only if the gate failed.
- Restore drill report shows RPO ≤ 15 min and RTO ≤ 4 h with ledger verify clean and no cross-tenant rows.
- Isolation suite covers 100 % of OpenAPI routes; zero leaks.
- Security scans show no high/critical findings open; manual review checklist signed.
- `migrations_lint_test.go` still passes (no trigger, function, procedure, view, extension, rule, or `DO` block in any migration).
- Release checklist (PRD §17) complete, including pilot owner confirmation.

## Risks

| Risk | Mitigation |
|---|---|
| Load host differs from production | Same images/compose, documented host size, re-run after first pilot |
| Flaky e2e on timing | API-level setup, deterministic seeds, waits on network idle and explicit states, retries only in CI reporting |
| WAL archiving misconfigured | Alert on archive age, drill before go-live, monthly re-drill |
| Pilot data reveals unmodelled flows | Feed back into OPEN list; no silent schema changes; new migration + plan update |
| Scope creep at release | Release gate only checks PRD 1.1 scope; removed modules stay removed |
| Tenant host cache is purged only in-process (tenant create, sandbox reset): with several API replicas, another replica keeps routing a moved slug to the old tenant | Stay single-replica in 1.1, or add a short cache TTL / cross-replica purge broadcast before scaling out; load test covers a reset under two replicas |

## Security

- Load and acceptance use generated data only, never pilot PII; evidence artifacts scrubbed of cookies/tokens; scans run in CI with read-only tokens; the fault hook is inert outside `APP_ENV=test` and covered by a test that asserts it.

## Rollback

Release rollback = redeploy previous image tags + restore from the pre-migrate backup if a migration ran (`make prod-migrate` always takes one). `sku_search_tokens`, if added, has a down migration and the search strategy falls back to LIKE.

## Next

Post-release: monitor SLO and ledger drift alerts, collect pilot feedback, resolve OPEN-13/17/19/20/21 with real usage data, plan 1.2.
