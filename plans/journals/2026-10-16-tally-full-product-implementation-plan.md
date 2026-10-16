---
title: Tally full product implementation plan
date: 2026-10-16
---

# Tally full product implementation plan

## Context
Planned PRD 1.1 for Tally from the accepted catalog/database brainstorm, the PRD and the v6.14 prototype. Plan: plans/261016-0854-tally-full-product-implementation/ (21 phases, 139d, 8 design files).

## Decisions
- Database has no triggers, functions, procedures, views, rules, extensions, DO blocks or function defaults; invariants live in Go, guarded by a migration lint test plus a pg_catalog check.
- Monorepo apps/api (Go, gin, sqlx, pgx) and apps/web (React, Vite, TanStack Query); Docker infra modelled on Sidecup.
- Command endpoints use the `{id}:{verb}` suffix; gin cannot route a `:` suffix, so httpx.CommandRouter dispatches verbs.
- Offline POS is cash only (PRD SAL-13); quote revisions use a `revision_no` column with history in audit_log.

## Issues found during the sweep
- Web phases 15–17 and 19 had invented endpoint paths (/parties, /pos-carts, slash-style commands); aligned to backend routes and added the missing backend routes (invoice :check/:reserve-more, stock count :recount, lot state filter, kit progress/preview, assembly preview, disassembly list).
- The prototype UI spec mislabels two invoice actions; web phase follows the prototype.

## Open
Owner to confirm implicit POS shifts (CASH-06) vs explicit shifts in the prototype gap list; editable price lists; screens without a visual baseline.

> Historical work record — not durable authority. Prefer docs/specs/ADRs for current decisions.
