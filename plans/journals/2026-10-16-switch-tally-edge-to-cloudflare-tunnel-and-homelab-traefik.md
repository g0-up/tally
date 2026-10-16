---
title: Switch Tally edge to Cloudflare Tunnel and homelab Traefik
date: 2026-10-16
summary: "Owner answers applied: sidecup.app domain, tunnel + Traefik edge, immutable slugs, one account per store, firewall deferred"
---

# Switch Tally edge to Cloudflare Tunnel and homelab Traefik

## What happened
The owner answered the open questions from the tenant-subdomain plan change: domain `sidecup.app`; production runs on this machine through Cloudflare Tunnel and the homelab Docker network; slugs can never change; one account belongs to one store only; the origin firewall is decided at deploy time.

A read-only check of `/home/cesc/Documents/homelab-infras` showed Traefik v3 on the external network `homelab` (entrypoint `web` :80) and the `cloudflared-sidecup` tunnel pointing at `http://traefik:80`. Sidecup already serves `tiendouong.sidecup.app` and `tiendouong-api.sidecup.app` on the same zone.

## Decision
- Removed every Caddy, ACME/DNS-01, origin certificate and `CLOUDFLARE_API_TOKEN` item from the plan. TLS ends at the Cloudflare edge (Universal SSL covers apex + one wildcard level).
- Production = `docker-compose.prod.yml` + `docker-compose.homelab.yml`. No proxy and no published ports. Postgres alias `tally-postgres`. Traefik routers `tally-api` (priority 3) and `tally-web` (priority 2) with header middleware `tally-headers`.
- Explicit low priorities are required: Traefik's default priority is rule length, so Tally's wildcard rule would otherwise outrank Sidecup's exact Host routers.
- Added `RESERVED_SLUGS` (seeded with Sidecup hostnames) and a runbook check for collisions in both directions.
- Client IP: gin `SetTrustedProxies(homelab subnet)` + `RemoteIPHeaders=[CF-Connecting-IP]`; no `TrustedPlatform`.
- One account = one store: sign-up with an existing email answers like success and mails the existing store link; ownership transfer to a used email returns `422 email_in_use`. AT-09 is tested as two stores, two owners.
- Firewall and Traefik's LAN :80 exposure deferred to deploy (phase 21).

Files: `plan.md`, `design/architecture.md` §1/§2/§5/§6/§7, phases 01, 02, 04, 21.

## Next steps
- Confirm the apex `sidecup.app` is free and how Sidecup will name future customer hosts on the zone.
- Decide at deploy time: shared `cloudflared-sidecup` tunnel vs a Tally tunnel, LAN port, firewall.

> Historical work record — not durable authority. Prefer docs/specs/ADRs for current decisions.
