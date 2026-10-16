---
title: Remove per-customer infra from Tally plan
date: 2026-10-16
summary: "Plan now uses one shared prod stack for all tenants, isolated by tenant_id + RLS; Sidecup per-customer stacks dropped"
---

# Remove per-customer infra from Tally plan

## What happened
The full-product plan (`plans/261016-0854-tally-full-product-implementation/`) copied Sidecup's per-customer stack shape (`CUSTOMER=<name>`, `infra/customers/<name>/.env`). That contradicted the data model, which is shared-schema multi-tenancy (`tenant_id` on every table, composite FKs, RLS). The PRD describes self-serve multi-tenant SaaS (ORG-01, SAAS-02) with no dedicated-customer or on-prem requirement.

## Decision
One shared production stack and database for all tenants, isolated by `tenant_id` + RLS. No per-customer stacks.

## Changes
- `plan.md`: goal 6 and acceptance criterion 8 reworded; new "Deployment topology" row in default decisions.
- `phase-01-monorepo-docker-infra.md`: dropped `customers/` from the Sidecup reference, the `CUSTOMER` env and Makefile variable, `infra/customers/` in `.gitignore`/`.gitkeep`, and per-customer runbook section; backup name is now `tally-<ts>.dump`; volume `tally-prod_pgdata`; homelab override kept.
- `design/architecture.md`: removed the `customers/<name>/.env` tree entry; prod compose project name is fixed to `tally-prod`; secrets come only from `infra/.env`.
- `phase-21-acceptance-load-security-release.md`: pilot release deploys to the shared stack and creates pilot tenants with `tally tenant create`.

## Next steps
- Resolve the AT-09 gap: the PRD assumes one account with membership in two tenants, but the design has no membership table and SEC-01 allows one owner per tenant.

> Historical work record — not durable authority. Prefer docs/specs/ADRs for current decisions.
