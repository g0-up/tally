---
title: Plan multi-unit conversion and per-unit prices
date: 2026-10-20
summary: "Planned a KiotViet-style unit table (base + N units, retail/wholesale per unit), prototype v6.15 first as an approval gate"
---

# Plan multi-unit conversion and per-unit prices

## What happened
The user asked whether Tally can match KiotViet's "Đơn vị tính" section: a base unit plus N units, each with a conversion factor and its own sale price. Scouting showed the backend already holds most of it:
- `sku_units` stores N units per SKU.
- `price_list_items.unit_id` stores per-unit price rows.
- The resolver prices a line in its own unit before falling back to base price × factor (`apps/api/internal/pricing/resolver.go:154`).
- Document lines snapshot the unit and factor.

The gaps:
- The create API accepts only one extra unit and only base prices.
- SKU reads do not return per-unit prices.
- A per-unit price cannot be removed.
- There is no atomic way to save the whole unit table.
- The web forms show a single "đơn vị lớn".

## Decision
- Plan: `plans/261020-1801-multi-unit-conversion-and-unit-prices/`, 5 phases.
- Phase 1 builds `docs/design/prototype-v6.15.html` (v6.14 is untouched) using only the prototype's design-system tokens and classes. It is a hard approval gate before any app code.
- User choices (2026-10-21):
  - add an editor for existing products ("Sửa đơn vị & giá");
  - each unit gets a retail and a wholesale price;
  - the unit table appears on every product create screen;
  - the prototype is a new file.
- The "Bán trực tiếp" flag is dropped.
- A blank unit price means "auto" (base × factor) and is not stored.
- One new endpoint, `PUT /skus/{id}/units`, replaces the whole unit table atomically, so a client-side sequence of calls cannot leave it half-saved.
- A unit used on a document cannot be deleted; the UI offers "Ngừng dùng" instead.
- No migration is needed.

## Next steps
Implement phase 1 (the prototype), get the user's approval, then run `/ak:cook` on the plan for phases 2–5.

> Historical work record — not durable authority. Prefer docs/specs/ADRs for current decisions.
