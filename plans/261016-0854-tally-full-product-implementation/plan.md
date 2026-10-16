---
title: "Tally full product implementation (PRD 1.1)"
description: "Go + PostgreSQL API and React web for single-owner Vietnamese retail/wholesale/gift-basket stores: catalog with colors and mix packs, pricing, ledgers for stock/debt/cash, procurement, sales/POS (offline), kits, campaigns, reports, SaaS control; Docker infra after Sidecup."
status: pending
priority: P1
effort: 139d
branch: master
tags: [feature, backend, frontend, database, infra]
blockedBy: []
blocks: []
created: 2026-10-16
---

# Tally full product implementation (PRD 1.1)

## Overview

Build the whole PRD 1.1 product in a monorepo `apps/api` (Go) + `apps/web` (React/Vite), deployed with Docker following the Sidecup infra shape. The database is fully designed up front (design files below), inherits every table of the accepted brainstorm, and contains **no triggers, functions, procedures, views, rules, extensions or `DO` blocks** — all invariants live in Go domain code, enforced by a migration lint test. The web app follows the v6.14 prototype exactly (tokens, labels, layouts), reviewed with the installed UI/UX skills in phase 20. Domain names come from one ubiquitous-language glossary shared by schema, Go packages, API and web features.

Sources: [brainstorm DB design](../reports/brainstorm-261016-1528-catalog-database-design.md) · [PRD](../../prd.md) · prototype artifact `https://claude.ai/artifact/3Kg9zruBj5QNFNMbVcpqyo` (local copy `docs/design/prototype-v6.14.html`) · [UI spec extracted from the prototype](../reports/design-261016-1549-prototype-ui-spec.md) · Sidecup infra `/home/cesc/Documents/personal-workspace/sidecup/infra` (`.env.example` only).

## Goals

| # | Goal | Priority |
|---|------|----------|
| 1 | Complete, trigger/function-free PostgreSQL schema with RLS, append-only ledgers and Go-enforced invariants | P1 |
| 2 | Correct stock (by color/lot), debt and cash ledgers: atomic, idempotent, reconcilable (`tally ledger verify`) | P1 |
| 3 | All PRD 1.1 modules end to end: catalog, pricing, procurement, sales/POS/offline, returns/exchanges, inventory, kits, debt, campaigns/CRM, reports, notifications, SaaS | P1 |
| 4 | Web UI 100 % faithful to the prototype, WCAG 2.2 AA on core flows | P1 |
| 5 | SOLID + named design patterns in both apps; ubiquitous language in code, API and UI | P1 |
| 6 | Docker infra (dev, one shared prod stack on the homelab behind Cloudflare Tunnel + Traefik), backups with RPO ≤ 15 min, restore drill | P1 |
| 7 | AT-01..18 and NFR-01..16 pass with stored evidence before commercial release | P1 |

## Design (authority for every phase)

| File | Content |
|---|---|
| [design/ubiquitous-language.md](design/ubiquitous-language.md) | Glossary VI ↔ EN ↔ code, document prefixes, statuses, exception kinds |
| [design/database-conventions.md](design/database-conventions.md) | Forbidden objects + lint, naming, keys, money/qty types, RLS, roles, lock order, idempotency, seeds, backups |
| [design/database-schema-org-partners.md](design/database-schema-org-partners.md) | Platform identity, org, control tables (audit, idempotency, outbox, attachments), partners |
| [design/database-schema-catalog-pricing.md](design/database-schema-catalog-pricing.md) | Colors, units, attributes, products/SKUs/variants, mix groups, search, price lists, promotions |
| [design/database-schema-inventory-procurement.md](design/database-schema-inventory-procurement.md) | Lots, moves, balances, reservations, counts/adjustments, procurement |
| [design/database-schema-sales-kits.md](design/database-schema-sales-kits.md) | Quotes, invoices, stock outs, sales documents, POS, returns/exchanges, kits/assembly |
| [design/database-schema-debt-cash-crm-saas.md](design/database-schema-debt-cash-crm-saas.md) | Debt, cash, campaign/CRM, notifications, reporting, SaaS/support |
| [design/architecture.md](design/architecture.md) | Tech stack, processes, Go module layout, domain packages, pattern catalogue, posting flow, web architecture, security, tenant hosts (§7) |

## Phases

| # | Phase | Effort | Depends on | Status |
|---|-------|--------|------------|--------|
| 1 | [Phase 01: Monorepo, Docker & Infra](./phase-01-monorepo-docker-infra.md) | 3d | — | Pending |
| 2 | [Phase 02: API Platform Foundation](./phase-02-api-platform-foundation.md) | 5d | 01 | Pending |
| 3 | [Phase 03: Database Schema & Migrations](./phase-03-database-schema-migrations.md) | 6d | 02 | Pending |
| 4 | [Phase 04: Identity & Organization](./phase-04-identity-organization.md) | 5d | 03 | Pending |
| 5 | [Phase 05: Catalog & Partners](./phase-05-catalog-partners.md) | 7d | 04 | Pending |
| 6 | [Phase 06: Pricing & Promotions](./phase-06-pricing.md) | 5d | 05 | Pending |
| 7 | [Phase 07: Inventory Ledger](./phase-07-inventory-ledger.md) | 7d | 05 | Pending |
| 8 | [Phase 08: Procurement (Nhập hàng)](./phase-08-procurement.md) | 6d | 06, 07, 10 | Pending |
| 9 | [Phase 09: Sales, POS, Returns & Exchanges](./phase-09-sales-pos.md) | 9d | 06, 07, 10 | Pending |
| 10 | [Phase 10: Debt & Cash](./phase-10-debt-cash.md) | 6d | 05 | Pending |
| 11 | [Phase 11: Kits, Combos & Assembly](./phase-11-kits-assembly.md) | 5d | 09 | Pending |
| 12 | [Phase 12: Campaign, CRM, Notifications & Reporting](./phase-12-campaign-crm-notifications-reporting.md) | 8d | 08, 09, 10, 11 | Pending |
| 13 | [Phase 13: SaaS Control, Support & Settings](./phase-13-saas-control-settings.md) | 7d | 12 | Pending |
| 14 | [Phase 14: Web foundation & design system](./phase-14-web-foundation-design-system.md) | 8d | 02, 04 | Pending |
| 15 | [Phase 15: Web catalog & procurement](./phase-15-web-catalog-procurement.md) | 7d | 14, 05, 06, 08 | Pending |
| 16 | [Phase 16: Web sales & POS (online)](./phase-16-web-sales-pos.md) | 9d | 14, 09, 10 | Pending |
| 17 | [Phase 17: Web inventory & gift baskets](./phase-17-web-inventory-kits.md) | 7d | 14, 07, 11 | Pending |
| 18 | [Phase 18: Web overview, debt, campaigns, reports, settings, onboarding & admin](./phase-18-web-debt-campaign-reports-settings.md) | 10d | 14, 10, 12, 13 | Pending |
| 19 | [Phase 19: POS offline & PWA](./phase-19-pos-offline-pwa.md) | 6d | 16 | Pending |
| 20 | [Phase 20: UI/UX review & accessibility](./phase-20-ui-ux-review-accessibility.md) | 5d | 15, 16, 17, 18, 19 | Pending |
| 21 | [Phase 21: Acceptance, Load, Security & Release](./phase-21-acceptance-load-security-release.md) | 8d | 13, 19, 20 | Pending |

Execution order: `01 → 02 → 03 → 04 → 05 → {06, 07, 10} → {08, 09} → 11 → 12 → 13`. Web starts after 04 (`14`), then each web phase as soon as its backend phases are done (`15` after 08, `16` after 09/10, `17` after 11, `18` after 13), `19` after 16, `20` after all web phases, `21` last. Critical path ≈ 84d (01-02-03-04-05-10-09-11-12-13-18-20-21); total effort 139d. Debt/cash core (10) runs before procurement and sales so their postings create real obligations and payments.

## PRD stage mapping (§16)

| Stage | Scope | Phases | Gate |
|---|---|---|---|
| A — Foundation and ledgers | tenant, owner account, catalog, units, color on lots/moves, ledgers, audit, periods | 01–05, 07 | isolation + stock/debt/cash invariants by color tested |
| B — Goods and money end to end | procurement, quotes, invoices + one-step stock out, POS + carts, stock card, debt, returns, exchanges | 06, 08, 09, 10, 15, 16 | AT-01, 02, 06, 07, 16, 18 |
| C — Advanced operations | reservations, lots/expiry, counts by color, kits/combos, mix packs, color prices, promotions | 07, 11, 17 | AT-03, 04, 05, 08, 17 |
| D — Business management | campaigns, CRM, dashboard, reports, notifications | 12, 18 | reports reconcile with ledgers |
| E — Commercialisation | subscription, onboarding, support, offline | 13, 18, 19, 21 | pilot, restore, load/security gates |
| F — Optimisation and analytics | forecast, advanced reports, read-only AI | 12 (RPT-10/11), 21 | value measured, data egress controlled |

## Acceptance criteria

1. Every table of the brainstorm design exists in migrations (plus the documented additions), with tenant composite FKs, RLS default deny, append-only grants, and the migration lint proving no trigger/function/procedure/view/rule/extension/`DO`.
2. Stock is tracked per SKU × color × lot in base units; sellable stock excludes reserved, quarantine and expired quantities; costs are moving average and traceable.
3. Every posting goes through `posting.Coordinator` (period lock → idempotency → doc number → ordered locks → apply → audit → outbox) and is atomic and idempotent.
4. Receivables/payables/advances reconcile: balance = Σ settlements; no automatic netting; aging reproducible as-of.
5. Pricing follows the fixed chain (customer → customer group → campaign → base list), color prices beat any-color, manual override and exceptions logged.
6. Mix packs validate flavor parts and issue stock per flavor; combos expand at sale; kits consume components once.
7. Web routes, labels and tokens match the prototype (screenshot thresholds in phase 20), with the 18 known UI gaps fixed and axe serious/critical = 0.
8. Infra: `make dev`, one shared prod stack on the homelab (prod compose + homelab overlay), `sidecup.app`, `admin.sidecup.app` and `<slug>.sidecup.app` served over HTTPS through Cloudflare Tunnel → Traefik without disturbing other stacks on the zone, `make prod-migrate` (backup first), WAL archiving, measured restore drill.
9. PRD AT-01..18 automated and green; NFR-01..16 measured with evidence; PRD §17 release checklist signed.

## Decisions taken by default (changeable)

| Topic | Default in this plan | Where |
|---|---|---|
| DB logic | No triggers, functions, procedures, views, materialized views, rules, extensions, `DO` blocks; invariants in Go + CHECK/UNIQUE/FK only | conventions §1, phase 03 |
| Tenant isolation | RLS with `current_setting('app.tenant_id')` inside policies (only allowed use) + composite `(tenant_id, id)` FKs | conventions §4 |
| Deployment topology | One shared stack and database for all tenants, isolated by `tenant_id` + RLS; no per-customer stacks (Sidecup's `CUSTOMER=<name>` shape is not copied) | phase 01, architecture §5 |
| Tenant hosts | Production domain `sidecup.app` (owner-confirmed). Each tenant on `<slug>.sidecup.app` (slug chosen at sign-up, immutable by owner decision, never reused; `RESERVED_SLUGS` blocks hostnames Sidecup already uses on the zone); apex = sign-up + store lookup, `admin.DOMAIN` = platform admin; session stays the tenant authority, host is cross-checked; host-only `__Host-` cookies | phases 02, 04, 13, 14, 19; architecture §7 |
| DNS / edge | Owner-confirmed: Cloudflare Tunnel (`cloudflared-sidecup`) → existing homelab Traefik on network `homelab`; TLS at the Cloudflare edge (Universal SSL, one level of wildcard); Tally ships only Traefik labels (low-priority routers, header middleware), no Caddy, no origin cert, no DNS API token | phase 01, architecture §7.6 |
| Account scope | Owner-confirmed: one account belongs to exactly one store (global `UNIQUE (email_key)`); no multi-store membership, so PRD AT-09 is tested as two stores with two owners | phases 04, 21 |
| Origin firewall | Deferred to deploy time: the tunnel needs no inbound internet port; Traefik's LAN :80 is a homelab-infras choice | phase 21 |
| Base quantity | Computed in Go (`qty × factor`), stored, CHECK `> 0` | conventions, phase 07 |
| Search | LIKE on Go-normalised keys; `sku_search_tokens` only if the NFR-03 gate fails | phase 21 |
| Auth | Opaque session cookie, argon2id, TOTP + recovery codes, re-auth 5 min for sensitive actions; single owner, no roles (SEC-01) | phase 04 |
| Files | S3-compatible object storage: MinIO in dev/homelab, S3/R2 in prod; signed URLs | phases 01, 04 |
| Platform admin | Same Go binary, `/api/admin/v1`, separate cookie, mandatory TOTP; web `/admin` uses the same tokens | phases 13, 18 |
| POS shift | Implicit shift (CASH-06), no end-of-shift cash count (OPEN-13); "Mở ca" fallback only | phases 09, 16 |
| Offline POS | Cash only, no credit/transfer/refund offline, caps from settings | phases 09, 19 |
| OPEN-13 operating costs | Not recorded in 1.1; margin reports say "chưa trừ chi phí vận hành" | phase 12 |
| OPEN-14 opening data | Manual onboarding wizard (stock by color via DC `opening`, debts/advances via DN, fund balances); no import | phases 07, 10, 13, 18 |
| OPEN-15 data on exit | `full_data_export` job (CSV per table + dictionary, zip, expiring link), also available in read-only | phase 13 |
| OPEN-16 naming | Keep "Hóa đơn" with "Chứng từ nội bộ — không phải hóa đơn điện tử" on print | phases 09, 16 |
| OPEN-17 reservation | Reserve per SKU (not color); clear error at stock out when a color is short | phases 07, 09 |
| OPEN-18 exchange with debt | Returned value first reduces the original document's open debt, then refunds; new goods paid in full | phase 09 |
| OPEN-19/20/21 | Min price per color, NXT split by color, others: deferred to 1.2 | — |
| OPEN-22 | Keep the CK (discount) column on invoice lines | phase 16 |
| Printing (DATA-04) | API returns immutable print payloads; web renders HTML print templates (browser print/PDF) | phases 09, 12, 16 |
| Expired quantity | Worker rolls lots to expired at tenant midnight (no DB jobs) | phase 07 |
| Migrations in prod | `MIGRATE_ON_START=false`; `make prod-migrate` = backup → migrate | phase 01 |
| Debt reminders | No third-party sending; copyable text + email only with consent | phases 10, 12 |
| SaaS billing | Manual bank-transfer adapter + generic signed webhook port; plan prices admin-editable (OPEN-09) | phase 13 |
| AI Q&A and product analytics | Off by default; gated by settings + entitlement; read-only tools; logged | phases 12, 13 |
| Backups | Nightly base backup + continuous WAL archiving (wal-g sidecar) for RPO ≤ 15 min | phase 21 |

## Open concerns carried from the UI spec and web phases

- UI spec §5.2.3 mislabels two invoice actions; web phase 16 follows the prototype ("Giữ thêm hàng" reserves more stock; payment dialog title "Ghi nhận thanh toán {id}", button "Ghi phiếu thu").
- Prototype leftovers not built: notification chip "Chờ duyệt" (approval flow removed) and schedule direction "Thu hộ về" (COD removed).
- UI gap #16 asks for explicit shifts; the PRD (CASH-06) keeps implicit shifts — owner to confirm.
- Price lists are read-only in the prototype although PRC-01/02 allow editing; reason catalogs (CAT-07) have no prototype screen. These screens use new labels and have no visual baseline, like print, auth, onboarding, admin, offline policy and sync-exception surfaces.
- API paths in web phases were aligned to backend phases; the generated OpenAPI is the final authority and web phases regenerate clients from it.
- Phase 19/20 need the CI workflow and nginx `sw.js` no-cache header owned by phase 01; coordinate those edits there.
- Tenant-host surfaces (slug field "Địa chỉ cửa hàng" at sign-up, "Tìm cửa hàng của bạn" on the apex) are new labels with no prototype baseline.
- Tally and Sidecup share the `sidecup.app` zone and the homelab Traefik. Sidecup customer hostnames must stay out of the Tally slug space (`RESERVED_SLUGS` + runbook check), and the apex `sidecup.app` must not be claimed by another site.

## Working rules for executors

- Read the design files before any phase; never add a DB function/trigger/view/extension — the migration lint fails CI.
- Back up the database before any schema or data change (`make db-backup` / `make prod-migrate`).
- No plan IDs, phase numbers or finding codes in code comments, migration names, test names or commit messages; conventional commits.
- Never commit `.env` files or secrets; read Sidecup `.env.example` only.
- Track and stop every dev process you start (deterministic ports, `make check-ports`).

<!-- slug: tally-full-product-implementation -->
