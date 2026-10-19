# Tally

Multi-tenant sales, inventory and debt management for small owner-run shops (product requirements:
[prd.md](prd.md)). One stack and one Postgres
database serve every tenant; each tenant works on its own host (`<slug>.sidecup.app`), isolated by
`tenant_id` and row-level security.

## Layout

| Path | Contents |
|---|---|
| `apps/api` | Go API and worker (`tally` binary: `serve`, `healthcheck`, ...), gin, pgx, golang-migrate |
| `apps/web` | React 19 + Vite + TypeScript SPA (Tailwind 4, TanStack Query, react-router) |
| `infra` | Docker compose stacks (dev, prod, homelab overlay), Postgres role init, backups |
| `docs` | Runbook and product documentation |
| `plans` | Implementation plan and design documents |

## Quick start

```sh
cd apps/web && pnpm install && cd ../..
make dev        # postgres :5442, mailpit :1025/:8025, minio :9000/:9001, API :8080, web :5173
```

Open http://localhost:5173 (tenant hosts: http://demo.localhost:5173). `make help` lists all targets.

See [docs/runbook.md](docs/runbook.md) for development details, backups, and production deployment on
the homelab behind Cloudflare Tunnel and Traefik.

Store owners' guide (Vietnamese): [docs/user-guide/](docs/user-guide/README.md). Support:
[docs/support/support-checklist.md](docs/support/support-checklist.md). Release gates:
[docs/release/release-checklist.md](docs/release/release-checklist.md) and
[docs/release/acceptance-matrix.md](docs/release/acceptance-matrix.md).
