---
title: Add Cloudflare tenant subdomains to Tally plan
date: 2026-10-16
summary: "Each tenant gets <slug>.DOMAIN on one shared stack behind Cloudflare; session stays tenant authority, host is cross-checked"
---

# Add Cloudflare tenant subdomains to Tally plan

## What happened
Plan 261016-0854-tally-full-product-implementation was updated so every tenant has its own subdomain `<slug>.DOMAIN`, with DNS on Cloudflare. Edits were made in place; no new plan directory.

## Decision
- One authority section: `design/architecture.md` §7 (host map, slug rules, request pipeline, cookies/URLs/mail, PWA, Cloudflare edge/TLS). Phases reference it instead of repeating it.
- Hosts: apex = sign-up + "Tìm cửa hàng của bạn"; `<slug>.DOMAIN` = tenant app + same-origin API; `admin.DOMAIN` = platform admin only.
- `tenants.slug` is immutable and never reused (`retired_tenant_slugs`, filled by purge). Go `org.SlugPolicy` enforces 3–30 chars, no `--`, a reserved list and no `-demo` suffix; sandbox uses `<parent>-demo`.
- The session stays the only source of `app.tenant_id`. `HostContext` + `TenantHostGuard` cross-check the host; a mismatch or unknown slug looks exactly like an unauthenticated request. Login is scoped to the host tenant with argon2 timing parity.
- Cookies are host-only `__Host-` cookies; every mail link goes through `platform/urls.Builder`.
- Edge: proxied Cloudflare `@` + `*` records, Full (strict). Caddy is a custom xcaddy image with `caddy-dns/cloudflare` for a wildcard cert via DNS-01 and `caddy-cloudflare-ip` for the real client IP. New env vars `CLOUDFLARE_API_TOKEN` and `TRUSTED_PROXY_CIDRS`. Homelab Traefik uses a DNS-01 resolver.
- Dev uses `<slug>.localhost:5173`.

Files: plan.md (decision rows, AC 8, open concerns), design/architecture.md, database-schema-org-partners.md, database-conventions.md, ubiquitous-language.md, phases 01, 02, 03, 04, 13, 14, 18, 19, 21.

## Next steps
- Owner to confirm: real domain, proxied vs DNS-only, slug immutability, AT-09 membership gap (tested as two owners).

> Historical work record — not durable authority. Prefer docs/specs/ADRs for current decisions.
