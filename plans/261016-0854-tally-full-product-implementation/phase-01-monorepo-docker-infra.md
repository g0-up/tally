---
title: "Phase 01: Monorepo, Docker & Infra"
status: todo
priority: P1
effort: 3d
dependsOn: []
---

# Phase 01 — Monorepo, Docker & Infra

## Context links

- [architecture.md](design/architecture.md) §1 layout, §2 topology, §5 infra, §7.6 edge (Cloudflare Tunnel → homelab Traefik, wildcard subdomains)
- [database-conventions.md](design/database-conventions.md) §5 roles, §8 backup rule
- Reference stack: `/home/cesc/Documents/personal-workspace/sidecup/infra` (`docker-compose.yml`, `docker-compose.prod.yml`, `docker-compose.homelab.yml`, `.env.example`) and its root `Makefile`, `apps/api/Dockerfile`, `apps/web/Dockerfile`, `apps/web/nginx.conf`. Read `.env.example` only — never the real `.env`.
- Homelab edge (outside this repo, read-only reference): `/home/cesc/Documents/homelab-infras/proxy/` (Traefik v3, network `homelab`, entrypoint `web`) and `/home/cesc/Documents/homelab-infras/tunnel/compose.yml` (`cloudflared-sidecup`). Never print or copy the tunnel token.
- PRD NFR-07 (backup/restore), NFR-08 (TLS/secrets), NFR-14 (safe upgrade), ORG-03 (one store, one warehouse)

## Overview

Create the empty monorepo skeleton with `apps/api`, `apps/web`, `infra/`, a root `Makefile`, and Docker images/compose stacks for dev and production. Production runs on the homelab machine: `docker-compose.prod.yml` + the `docker-compose.homelab.yml` overlay join the existing `homelab` network, and the existing Traefik + Cloudflare Tunnel serve `sidecup.app`, `admin.sidecup.app` and every tenant `<slug>.sidecup.app` from one stack. TLS ends at Cloudflare, so the repo holds no proxy, certificate or DNS token. At the end `make dev` starts Postgres + Mailpit + MinIO in Docker and runs a hello-world API and web app on fixed ports.

## Key insights

- Copy Sidecup's proven shape (`check-ports` that names the port owner instead of switching ports, distroless API image, nginx web image) and change four things on purpose: there are no per-customer stacks (no `CUSTOMER` variable, no `infra/customers/`) because every tenant lives in one shared stack and database, isolated by `tenant_id` + RLS; Postgres roles are created by `infra/postgres/init/01-roles.sh` + `infra/postgres/roles.sql`; a separate `worker` service exists; production never migrates on start (`MIGRATE_ON_START=false`) because a backup must precede migrations.
- Postgres 16 (PRD/brainstorm require ≥15 for `UNIQUE NULLS NOT DISTINCT`).
- The edge already exists: Cloudflare Universal SSL covers `sidecup.app` + `*.sidecup.app`, `cloudflared-sidecup` forwards to `http://traefik:80`, and Traefik discovers labelled containers on `homelab`. Tally adds only labels, so there is no Caddy, ACME or DNS-01 work. Traefik ranks routers by rule length by default, so Tally's wildcard routers get explicit low priorities to leave Sidecup's exact `Host` routers on the same zone untouched.
- Role SQL uses only `CREATE ROLE`, `GRANT`, `ALTER DEFAULT PRIVILEGES` — no functions, no `DO` blocks (user constraint).

## Requirements

| ID | Behaviour delivered here |
|---|---|
| NFR-07 | `make db-backup` / `make db-restore` wired; backups land in `infra/backups/` (git-ignored) |
| NFR-08 | HTTPS at the Cloudflare edge (Universal SSL for `sidecup.app` + `*.sidecup.app`), tunnel to Traefik, no public origin port; security headers + CSP + HSTS `includeSubDomains` as a Traefik middleware; secrets only in env files; `.env*` ignored except `.env.example` |
| NFR-14 | `make prod-migrate` = backup → migrate with `--backup-file`; prod compose has no migrate-on-start |
| NFR-09 | JSON logs to stdout from api/worker; Traefik access logs stay with homelab-infras |
| ORG-03 | No multi-warehouse infra concerns (one shared DB, tenant rows inside) |

## Architecture

```
infra/docker-compose.yml        postgres:16-alpine(5432, init roles) · mailpit(1025/8025) · minio(9000/9001) · minio-init(mc mb tally-dev)
infra/docker-compose.prod.yml   postgres · api(tally serve) · worker(tally worker) · web(nginx) — no proxy, no published ports
infra/docker-compose.homelab.yml  production overlay: external network "homelab" on api/web, postgres alias tally-postgres, Traefik labels (routers tally-api p=3, tally-web p=2, middleware tally-headers)

Cloudflare edge (sidecup.app, *.sidecup.app) ──tunnel──▶ cloudflared-sidecup ──http──▶ Traefik :80 ──▶ api:8080 (/api/*, Host preserved) | web:80 (rest)
```

Environment variables (all listed in `infra/.env.example` with placeholder values): `DOMAIN, APP_ENV, APP_TZ=Asia/Ho_Chi_Minh, LOG_LEVEL, POSTGRES_DB, POSTGRES_SUPERUSER_PASSWORD, TALLY_OWNER_PASSWORD, TALLY_APP_PASSWORD, TALLY_READONLY_PASSWORD, DATABASE_URL, MIGRATE_DATABASE_URL, SESSION_HASH_KEY, CSRF_KEY, APP_ENCRYPTION_KEY, SMTP_HOST, SMTP_PORT, SMTP_USER, SMTP_PASSWORD, MAIL_FROM, OBJECT_STORE_ENDPOINT, OBJECT_STORE_REGION, OBJECT_STORE_BUCKET, OBJECT_STORE_ACCESS_KEY, OBJECT_STORE_SECRET_KEY, PUBLIC_BASE_URL, TRUSTED_PROXY_CIDRS, RESERVED_SLUGS, MIGRATE_ON_START, PLATFORM_ADMIN_BOOTSTRAP_EMAIL`. `PUBLIC_BASE_URL` is the apex (production `DOMAIN=sidecup.app`, `PUBLIC_BASE_URL=https://sidecup.app`); tenant and admin hosts are derived from it. `TRUSTED_PROXY_CIDRS` is the `homelab` network subnet (`docker network inspect homelab`). `RESERVED_SLUGS` example value: `tiendouong,tiendouong-api` (hostnames Sidecup already uses on the zone).

Role init: the official image runs `infra/postgres/init/01-roles.sh` on an empty volume; the script only calls `psql -v owner_pw="$TALLY_OWNER_PASSWORD" … -f /etc/tally/roles.sql` (file mounted from `infra/postgres/roles.sql`, outside the init directory so it is not auto-run without variables):

```sql
CREATE ROLE tally_owner LOGIN PASSWORD :'owner_pw';
CREATE ROLE tally_app LOGIN PASSWORD :'app_pw' NOSUPERUSER NOBYPASSRLS;
CREATE ROLE tally_readonly LOGIN PASSWORD :'ro_pw' NOSUPERUSER NOBYPASSRLS;
GRANT tally_readonly TO tally_app;              -- lets the API SET LOCAL ROLE tally_readonly for support sessions
CREATE SCHEMA tally AUTHORIZATION tally_owner;
ALTER ROLE tally_owner SET search_path = tally;
ALTER ROLE tally_app SET search_path = tally;
ALTER ROLE tally_readonly SET search_path = tally;
ALTER DEFAULT PRIVILEGES FOR ROLE tally_owner IN SCHEMA tally GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO tally_app;
ALTER DEFAULT PRIVILEGES FOR ROLE tally_owner IN SCHEMA tally GRANT SELECT ON TABLES TO tally_readonly;
GRANT USAGE ON SCHEMA tally TO tally_app, tally_readonly;
```

Append-only revokes happen per table inside migrations (phase 03).

## Related files

| Path | Action |
|---|---|
| `Makefile` | create — targets: help, check-ports, check-env, db-up, dev, dev-api, dev-web, worker, down, db-backup, db-restore, migrate-up, migrate-down, seed, test, test-api, test-web, test-integration, lint, build, e2e, prod-config, prod-up, prod-down, prod-logs, prod-migrate (every prod target merges `docker-compose.prod.yml` + `docker-compose.homelab.yml`) |
| `.gitignore` | create — `infra/.env`, `infra/backups/`, `apps/web/dist`, `node_modules`, coverage, `test-results` |
| `.editorconfig`, `README.md` | create |
| `apps/api/go.mod`, `apps/api/cmd/tally/main.go`, `apps/api/internal/cli/root.go`, `apps/api/internal/cli/serve.go`, `apps/api/internal/cli/healthcheck.go` | create — cobra root, `serve` returning 200 on `/healthz` |
| `apps/api/Makefile` | create — run, worker, migrate-up/down, seed, test, test-integration, lint (golangci-lint) |
| `apps/api/Dockerfile` | create — `golang:1.25-alpine` build `CGO_ENABLED=0 -trimpath -ldflags "-s -w -X main.version=$VERSION"` → `gcr.io/distroless/static:nonroot`, `ENTRYPOINT ["/tally"]`, `CMD ["serve"]` |
| `apps/api/.golangci.yml` | create — govet, staticcheck, errcheck, gosec, revive, gocritic, depguard (forbid `internal/<domain>` importing another domain except via ports) |
| `apps/web/package.json`, `pnpm-lock.yaml`, `vite.config.ts`, `tsconfig*.json`, `eslint.config.js`, `vitest.config.ts`, `playwright.config.ts`, `index.html`, `src/main.tsx` | create — Vite + React 19 + TS strict, placeholder page |
| `apps/web/Dockerfile`, `apps/web/nginx.conf` | create — `node:22-alpine` + corepack pnpm build → `nginx:1.27-alpine`; SPA fallback, `immutable` cache for hashed assets, no-cache for `index.html` and service worker |
| `infra/docker-compose.yml`, `infra/docker-compose.prod.yml`, `infra/docker-compose.homelab.yml` | create |
| `infra/docker-compose.homelab.yml` labels | create — `traefik.enable=true`, `traefik.docker.network=homelab`; router `tally-api`: ``(Host(`${DOMAIN}`) || HostRegexp(`^[a-z0-9-]+[.]${DOMAIN}$`)) && PathPrefix(`/api/`)``, `priority=3`, service port 8080; router `tally-web`: same host part ``&& !PathPrefix(`/api/`)``, `priority=2`, port 80; both on entrypoint `web` with middleware `tally-headers` (HSTS forced `max-age=31536000; includeSubDomains`, nosniff, frameDeny, Referrer-Policy, Permissions-Policy, CSP `default-src 'self'; img-src 'self' data: blob: https://<object-store-host>; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'`, empty `Server`). `/internal/*` and `/metrics` are never routed (api router only matches `/api/`) |
| `infra/postgres/init/01-roles.sh`, `infra/postgres/roles.sql` | create — shell wrapper passes passwords as psql variables; SQL has roles/grants only |
| `infra/.env.example`, `infra/backups/.gitkeep` | create |
| `.github/workflows/ci.yml` | create — jobs: api (lint, unit, integration with service postgres:16-alpine), web (lint, typecheck, test, build), migrations (lint + up/down/up), docker build |
| `docs/runbook.md` | create — dev setup (tenant hosts on `<slug>.localhost:5173`), deploy, production on the homelab (Cloudflare Tunnel public hostnames, wildcard DNS, zone rules, slug/hostname collision check with Sidecup), backup/restore, creating tenants with `tally tenant create` |

## Implementation steps

1. Initialise `apps/api` Go module (Go 1.25), add cobra, gin, sqlx, pgx v5, shopspring/decimal, golang-migrate v4, slog; create `cmd/tally/main.go` that calls `cli.Execute(version)`.
2. Implement `serve` with a gin engine exposing `/healthz` (liveness) and `healthcheck` subcommand that GETs `http://127.0.0.1:8080/healthz` (used by distroless healthcheck — no curl in image).
3. Scaffold `apps/web` with pnpm + Vite React TS template, add Tailwind 4, TanStack Query 5, react-router 7, zod, react-hook-form, radix primitives, sonner, lucide-react, vitest, RTL, msw, Playwright; strict TS; `pnpm dev` on port 5173 with `/api` proxy to 8080 (`changeOrigin: false` so the API sees the original `Host`) and `server.allowedHosts: ['.localhost']` so `http://<slug>.localhost:5173` works without DNS; dev `PUBLIC_BASE_URL=http://localhost:5173`.
4. Write `infra/docker-compose.yml` (dev) with healthchecks (`pg_isready`, `mc ready local`), named volumes, init scripts mounted read-only.
5. Write role init scripts; verify with `psql -U tally_app -c 'select current_user'` and that `tally_app` cannot `CREATE TABLE` in schema `tally`.
6. Write prod compose: api + worker from the same image (`command: ["worker"]`), `MIGRATE_ON_START: "false"`, `DATABASE_URL` uses `tally_app@tally-postgres`, `MIGRATE_DATABASE_URL` uses `tally_owner` (passed only to a one-shot `migrate` service under profile `ops`); no proxy service and no `ports:` on any service.
7. Write the homelab overlay mirroring Sidecup's: api and web join the external `homelab` network (plus the project network), postgres keeps only the project network with alias `tally-postgres`, Traefik labels as in Related files. Verify with `docker compose ... config` that no label uses the plain router names `api`/`web` and that priorities are set.
8. Write root `Makefile` (Sidecup style, without `CUSTOMER`): `ENV_FILE` (fixed `infra/.env`), `COMPOSE`, `COMPOSE_PROD`, `COMPOSE_HOMELAB`; `check-ports` for 8080/5173/5432/8025/9000 that prints `lsof` owner and exits 1; `db-backup` runs `pg_dump -Fc` in the postgres container to `infra/backups/tally-$(date +%Y%m%d%H%M%S).dump`; `db-restore FILE=` runs `pg_restore --clean --if-exists` after an interactive confirm; `prod-migrate` = `db-backup` then `docker compose run --rm migrate up --backup-file <latest>`.
9. Add CI workflow; add `.gitignore`/`.editorconfig`; write `docs/runbook.md` including the production checklist: in the Zero Trust dashboard add tunnel `cloudflared-sidecup` public hostnames `sidecup.app` and `*.sidecup.app` → `http://traefik:80`, placed after the existing specific Sidecup hostnames; proxied CNAMEs `@` and `*` → `<tunnel-id>.cfargotunnel.com` (existing explicit records stay); Redirect Rule `www` → apex; Always Use HTTPS; Cache Rule bypassing `/api/*`; confirm `index.html`/`sw.js` arrive with `no-cache` through the edge; before adding any Sidecup hostname on `sidecup.app`, check `tenants.slug` for it, and add every new Sidecup hostname label to `RESERVED_SLUGS`.
10. Smoke test: `make dev` → `curl -fsS localhost:8080/healthz` and `curl -fsS localhost:5173`; `docker compose -f infra/docker-compose.prod.yml -f infra/docker-compose.homelab.yml --env-file infra/.env.example config -q` validates (example env has placeholders that satisfy `:?` checks); in dev `curl -fsS -H 'Host: demo.localhost:5173' localhost:5173` returns the SPA.

## Todo

- [ ] Go module + cobra root + `serve`/`healthcheck`
- [ ] Web scaffold with full dependency set and strict TS
- [ ] Dev compose (postgres, mailpit, minio, minio-init)
- [ ] Role init scripts (no functions/DO)
- [ ] Prod compose (postgres, api, worker, web, ops migrate; no proxy, no ports)
- [ ] Homelab overlay: `homelab` network, `tally-postgres` alias, low-priority Traefik routers, header middleware
- [ ] Root Makefile incl. check-ports, db-backup, db-restore, prod-migrate
- [ ] API/Web Dockerfiles, nginx.conf
- [ ] golangci-lint + eslint configs, depguard rule for domain imports
- [ ] CI workflow
- [ ] `.env.example`, `.gitignore`, runbook

## Success criteria & verification

- `make dev` brings up the stack; `make check-ports` reports a busy port owner instead of picking a new port.
- `docker build apps/api` and `docker build apps/web` succeed; API image < 30 MB, runs as nonroot.
- `docker compose -f infra/docker-compose.prod.yml --env-file infra/.env.example config -q` exits 0; same for homelab merge.
- `psql "$DATABASE_URL" -c 'create table tally.x(i int)'` fails with permission denied for `tally_app`.
- `make db-backup` produces a restorable dump: `make db-restore FILE=<dump>` on a scratch volume succeeds.
- CI green on an empty-feature commit.
- `git check-ignore infra/.env infra/backups/x.dump` prints both paths.
- On the first prod deploy: `curl -sI https://sidecup.app`, `https://admin.sidecup.app` and `https://anything.sidecup.app` answer from Tally through the tunnel with the `tally-headers` set; `https://tiendouong.sidecup.app` still answers from Sidecup (priority check); `docker ps` shows no published Tally port.

## Risks

| Risk | Mitigation |
|---|---|
| Init scripts run only on empty volumes, so role changes are missed later | Migration `000026_privileges_assertions` re-asserts grants idempotently with plain `GRANT`/`REVOKE` |
| Port collisions with other local projects (Sidecup also uses 8080/5173/5432) | `check-ports` fails loudly; document `docker compose -p` per project; never auto-increment ports |
| Secrets leaking via compose `config` output in CI logs | CI uses `.env.example` only; `config -q` |
| Tally's wildcard router steals another stack's host on the shared Traefik | Explicit `priority` 2/3 below Traefik's length-based defaults; deploy check that `tiendouong.sidecup.app` still reaches Sidecup |
| A Tally slug and a Sidecup hostname collide on `sidecup.app` | `RESERVED_SLUGS` blocks known Sidecup labels at signup; runbook check before any new Sidecup hostname |
| Cloudflare caches API or HTML responses | Cache Rule bypass for `/api/*`; nginx `no-cache` on `index.html`/`sw.js`; verified in runbook checklist |
| Rate limits/audit see Traefik's IP instead of clients | `httpx.ClientIP` reads `CF-Connecting-IP` only from peers in `TRUSTED_PROXY_CIDRS` (the `homelab` subnet), phase 02 |

## Security

- No secret committed; `.env.example` holds placeholders only. No Tally port is published; internet traffic arrives only through the outbound Cloudflare Tunnel. Postgres stays on the project network. Distroless nonroot API image. Headers + CSP via Traefik middleware. Traefik's LAN port :80 and any host firewall are homelab-infras choices, decided at deploy time (phase 21). Separate DB roles: the API never holds DDL rights.

## Rollback

Infrastructure only; revert the commit. Volumes are named per project (`tally-prod_pgdata`) so removal never touches Sidecup volumes.

## Next

Phase 02 builds the API platform kernel on this skeleton; phase 14 builds the web foundation.
