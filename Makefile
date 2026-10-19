# Tally monorepo tasks. Development: `make dev`. Production (homelab): `make prod-up` with infra/.env.
# See docs/runbook.md.

# Fixed host ports; `make check-ports` names the owner of a busy one. Only Postgres is configurable
# because 5432 is commonly taken by another local Postgres.
POSTGRES_HOST_PORT ?= 5442
export POSTGRES_HOST_PORT
DEV_PORTS = 8080 5173 $(POSTGRES_HOST_PORT) 1025 8025 9000 9001

# Image tag for `make build` and the production stack.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
export VERSION

ENV_FILE        = infra/.env
COMPOSE         = docker compose -f infra/docker-compose.yml
COMPOSE_PROD    = docker compose -f infra/docker-compose.prod.yml --env-file $(ENV_FILE)
# WAL archiving and nightly base backups (infra/docker-compose.backup.yml) are part of production;
# BACKUP_COMPOSE= leaves the wal-g sidecar out, e.g. on a stack without a backup bucket yet.
BACKUP_COMPOSE ?= -f infra/docker-compose.backup.yml
COMPOSE_HOMELAB = docker compose -f infra/docker-compose.prod.yml -f infra/docker-compose.homelab.yml $(BACKUP_COMPOSE) --env-file $(ENV_FILE)

PNPM ?= $(shell command -v pnpm >/dev/null 2>&1 && echo pnpm || echo corepack pnpm)

# db-backup / db-restore target the dev stack by default; STACK=prod targets production.
STACK ?= dev
ifeq ($(STACK),dev)
STACK_COMPOSE = $(COMPOSE)
STACK_DEPS    =
else ifeq ($(STACK),prod)
STACK_COMPOSE = $(COMPOSE_HOMELAB)
STACK_DEPS    = check-env
else
$(error STACK must be dev or prod, got '$(STACK)')
endif

BACKUP_DIR  = infra/backups
BACKUP_TS  := $(shell date +%Y%m%d%H%M%S)
BACKUP_FILE ?= $(BACKUP_DIR)/tally-$(BACKUP_TS).dump

.PHONY: help check-ports check-env db-up dev dev-api dev-web worker down db-backup db-restore \
	migrate-up migrate-down seed db-docs test test-api test-web test-integration lint build e2e \
	prod-config prod-up prod-down prod-logs prod-migrate \
	load-seed load-test soak-test acceptance restore-drill security-scan

help:
	@echo "Development"
	@echo "  make dev               check ports, start postgres/mailpit/minio, run API (:8080) + web (:5173); Ctrl-C stops API/web"
	@echo "  make db-up | down      start / stop the dev dependencies (docker project 'tally')"
	@echo "  make dev-api | dev-web | worker"
	@echo "  make check-ports       fail and name the owner when a dev port is taken (never switches ports)"
	@echo "  make migrate-up | migrate-down | seed"
	@echo "  make db-docs           regenerate docs/database.md from the migrated dev database"
	@echo "  make test | test-api | test-web | test-integration | lint | build | e2e"
	@echo "Backups (STACK=dev default, STACK=prod for production)"
	@echo "  make db-backup         pg_dump -Fc into $(BACKUP_DIR)/"
	@echo "  make db-restore FILE=$(BACKUP_DIR)/<file>.dump   (asks for confirmation; CONFIRM=yes skips it)"
	@echo "Production on the homelab (infra/docker-compose.prod.yml + infra/docker-compose.homelab.yml + $(ENV_FILE))"
	@echo "  make check-env | prod-config | prod-up | prod-down | prod-logs [SERVICE=api]"
	@echo "  make prod-migrate      backup first, then migrate up with --backup-file"
	@echo "Release checks (docs/runbook.md, load/README.md)"
	@echo "  make load-seed         tally seed load: LOAD_TENANTS tenants x LOAD_SKUS SKUs x LOAD_MOVES moves, LOAD_SEED=42"
	@echo "  make load-test         k6 lookup, posting, reports, then pos-devices against BASE_URL; then ledger verify"
	@echo "  make soak-test         k6 soak (SOAK_DURATION=4h) with docker stats before/after; then ledger verify"
	@echo "  make acceptance        Playwright acceptance journeys (apps/web/e2e/acceptance) against a running stack"
	@echo "  make restore-drill     point-in-time restore into a scratch stack with an RPO/RTO report [DRILL_ARGS=...]"
	@echo "  make security-scan     govulncheck, gosec, pnpm audit --prod, trivy, gitleaks (pinned versions)"

check-ports:
	@sh infra/scripts/check-ports.sh $(DEV_PORTS)

db-up:
	$(COMPOSE) up -d --wait postgres mailpit minio
	$(COMPOSE) up --no-log-prefix minio-init

# Schema migrations are applied by hand: `make db-backup` when the database holds data, then
# `make migrate-up`.
dev: check-ports db-up
	@trap 'kill 0' INT TERM EXIT; \
	 ( cd apps/api && $(MAKE) --no-print-directory run ) & \
	 ( cd apps/web && $(PNPM) dev ) & \
	 wait

dev-api:
	cd apps/api && $(MAKE) --no-print-directory run

dev-web:
	cd apps/web && $(PNPM) dev

worker:
	cd apps/api && $(MAKE) --no-print-directory worker

down:
	$(COMPOSE) down

db-backup: $(STACK_DEPS)
	@mkdir -p $(BACKUP_DIR)
	@umask 077; \
	 if $(STACK_COMPOSE) exec -T postgres sh -c 'pg_dump -U "$$POSTGRES_USER" -d "$$POSTGRES_DB" -Fc' > "$(BACKUP_FILE).partial"; then \
		mv "$(BACKUP_FILE).partial" "$(BACKUP_FILE)"; \
		echo "Backup written to $(BACKUP_FILE)"; \
	 else \
		rm -f "$(BACKUP_FILE).partial"; echo "Backup failed" >&2; exit 1; \
	 fi

db-restore: $(STACK_DEPS)
	@test -n "$(FILE)" || { echo "usage: make db-restore FILE=$(BACKUP_DIR)/<file>.dump [STACK=dev|prod]" >&2; exit 1; }
	@test -s "$(FILE)" || { echo "$(FILE) does not exist or is empty" >&2; exit 1; }
	@if [ "$(CONFIRM)" != "yes" ]; then \
		printf 'Replace the %s database with %s? Type "yes": ' "$(STACK)" "$(FILE)"; read -r answer; \
		[ "$$answer" = "yes" ] || { echo "Aborted"; exit 1; }; \
	 fi
	$(STACK_COMPOSE) exec -T postgres sh -c 'pg_restore -U "$$POSTGRES_USER" -d "$$POSTGRES_DB" --clean --if-exists --single-transaction --exit-on-error' < "$(FILE)"
	@echo "Restored $(FILE) into the $(STACK) database"

migrate-up:
	cd apps/api && $(MAKE) --no-print-directory migrate-up

migrate-down:
	cd apps/api && $(MAKE) --no-print-directory migrate-down

# Runs with the APP_ENV of apps/api/Makefile (development unless overridden); seed dev refuses any
# APP_ENV other than development or test.
seed:
	cd apps/api && $(MAKE) --no-print-directory seed

# docs/database.md lists the tables of the migrated dev database, grouped by the migration that
# creates them. It fails when a table of apps/api/migrations is missing (run `make migrate-up`).
DB_DOCS     = docs/database.md
DB_DOCS_SQL = SELECT t.table_name, (SELECT count(*) FROM information_schema.columns c \
	WHERE c.table_schema = t.table_schema AND c.table_name = t.table_name), \
	CASE WHEN p.relforcerowsecurity THEN 'tenant (RLS)' ELSE 'platform' END \
	FROM information_schema.tables t JOIN pg_class p ON p.relname = t.table_name AND p.relnamespace = 'tally'::regnamespace \
	WHERE t.table_schema = 'tally' AND t.table_type = 'BASE TABLE' AND t.table_name <> 'schema_migrations' ORDER BY 1

define DB_DOCS_AWK
BEGIN { FS = "|" }
FNR == NR {
	split($$0, a, ":"); file = a[1]; sub(/.*\//, "", file); sub(/\.up\.sql$$/, "", file)
	name = a[2]; sub(/^CREATE TABLE /, "", name)
	owner[name] = file; list[file] = list[file] " " name
	if (!(file in seen)) { seen[file] = 1; order[++n] = file }
	next
}
{ cols[$$1] = $$2; kind[$$1] = $$3; total++; if ($$3 ~ /tenant/) tenants++ }
END {
	for (name in owner) if (!(name in cols)) { print "db-docs: table " name " is not in the database; run make migrate-up" > "/dev/stderr"; bad = 1 }
	if (bad) exit 1
	d = "../plans/261016-0854-tally-full-product-implementation/design/"
	print "# Database"
	print ""
	print "Generated by `make db-docs` from the migrated dev database; do not edit by hand."
	print ""
	print "Table design and rationale live in the design files; the migrations in `apps/api/migrations` are authoritative for exact DDL and constraint names:"
	print "[conventions](" d "database-conventions.md) ·"
	print "[org & partners](" d "database-schema-org-partners.md) ·"
	print "[catalog & pricing](" d "database-schema-catalog-pricing.md) ·"
	print "[inventory & procurement](" d "database-schema-inventory-procurement.md) ·"
	print "[sales & kits](" d "database-schema-sales-kits.md) ·"
	print "[debt, cash, CRM, SaaS](" d "database-schema-debt-cash-crm-saas.md)."
	print ""
	print "Schema `tally`: " total " tables, " tenants " tenant tables (row-level security forced, policy"
	print "`tenant_isolation` on `app.tenant_id`) and " total - tenants " platform tables."
	for (i = 1; i <= n; i++) {
		f = order[i]; v = substr(f, 1, 6); title = substr(f, 8); gsub(/_/, " ", title)
		print ""; print "## " v " " title; print ""
		print "| Table | Columns | Isolation |"; print "|---|---:|---|"
		k = split(substr(list[f], 2), names, " ")
		for (j = 1; j <= k; j++) { t = names[j]; print "| `" t "` | " cols[t] " | " kind[t] " |"; done[t] = 1 }
	}
	for (t in cols) if (!(t in done)) extra = extra "| `" t "` | " cols[t] " | " kind[t] " |\n"
	if (extra != "") { print ""; print "## Not created by a migration"; print ""; print "| Table | Columns | Isolation |"; print "|---|---:|---|"; printf "%s", extra }
}
endef
export DB_DOCS_AWK

db-docs:
	@map=$$(mktemp) && tables=$$(mktemp) && trap 'rm -f "$$map" "$$tables"' EXIT && \
	 grep -Ho 'CREATE TABLE [a-z_]*' apps/api/migrations/*.up.sql > "$$map" && \
	 echo "$(DB_DOCS_SQL)" | $(COMPOSE) exec -T postgres sh -c 'psql -X -q -v ON_ERROR_STOP=1 -U "$$POSTGRES_USER" -d "$$POSTGRES_DB" -AtF "|"' > "$$tables" && \
	 awk "$$DB_DOCS_AWK" "$$map" "$$tables" > "$(DB_DOCS).partial" && mv "$(DB_DOCS).partial" "$(DB_DOCS)" && \
	 echo "Wrote $(DB_DOCS)" || { rm -f "$(DB_DOCS).partial"; exit 1; }

test: test-api test-web

test-api:
	cd apps/api && $(MAKE) --no-print-directory test

test-web:
	cd apps/web && $(PNPM) test

# Postgres comes from testcontainers; the object store and mailer tests use the dev minio and mailpit.
test-integration: db-up
	cd apps/api && $(MAKE) --no-print-directory test-integration

lint:
	cd apps/api && $(MAKE) --no-print-directory lint
	cd apps/web && $(PNPM) lint && $(PNPM) typecheck

build:
	docker build --build-arg VERSION=$(VERSION) -t tally-api:$(VERSION) apps/api
	docker build -t tally-web:$(VERSION) apps/web

# Runs against a running `make dev` (E2E_BASE_URL defaults to http://localhost:5173).
e2e:
	cd apps/web && $(PNPM) e2e

check-env:
	@test -f $(ENV_FILE) || { echo "Missing $(ENV_FILE): copy infra/.env.example and fill it in" >&2; exit 1; }
	@if grep -n 'change-me' $(ENV_FILE); then echo "Replace the change-me placeholders above in $(ENV_FILE)" >&2; exit 1; fi
	@$(COMPOSE_HOMELAB) config -q

prod-config: check-env
	$(COMPOSE_HOMELAB) config -q
	@echo "Production config is valid"

prod-up: check-env
	$(COMPOSE_HOMELAB) up -d --build

prod-down: check-env
	$(COMPOSE_HOMELAB) down

prod-logs: check-env
	$(COMPOSE_HOMELAB) logs -f --tail=200 $(SERVICE)

# A backup always precedes a production migration; the migrator records which dump it follows.
# The dump is 0600 and owned by the invoking user, so the migrator runs as that user to read it.
prod-migrate: check-env
	$(MAKE) --no-print-directory db-backup STACK=prod BACKUP_FILE=$(BACKUP_FILE)
	$(COMPOSE_HOMELAB) --profile ops run --rm --user "$$(id -u):$$(id -g)" migrate up --backup-file /backups/$(notdir $(BACKUP_FILE))

# --- Release checks ---------------------------------------------------------------------------
# The load dataset and the ledger checks run in the api image of the production-like stack
# (LOAD_COMPOSE); k6 runs from its pinned image on the host network against BASE_URL, the local
# Traefik. Never point these at the real production stack. load/README.md has every LOAD_* setting.
LOAD_COMPOSE     ?= $(COMPOSE_HOMELAB)
K6_IMAGE          = grafana/k6:2.3.0
LOAD_RESULTS      = load/test-results
BASE_URL         ?= http://localhost
LOAD_TENANTS     ?= 50
LOAD_SKUS        ?= 20000
LOAD_MOVES       ?= 1000000
LOAD_SEED        ?= 42
# `tally seed load` names its tenants <prefix>-<seed>-<n>; a re-seed needs a new LOAD_SEED.
LOAD_SLUG_PREFIX ?= load-$(LOAD_SEED)-
export BASE_URL LOAD_TENANTS LOAD_SLUG_PREFIX
K6_ENV = BASE_URL TENANT_HOST TENANT_URL LOAD_TENANTS LOAD_SLUG_PREFIX LOAD_SLUGS LOAD_OWNER_LOGIN \
	LOAD_OWNER_PASSWORD LOAD_DEVICES LOAD_DURATION LOAD_RAMP_UP LOAD_THINK_MIN_MS LOAD_THINK_MAX_MS \
	LOAD_FIXTURE_SKUS LOAD_CLIENT_IPS SOAK_DURATION POS_DEVICES POS_LAST_UNITS LOAD_VERBOSE
# Runs as the calling user so the summaries in $(LOAD_RESULTS) stay writable; `-e NAME` passes a
# variable only when it is set, so the password never appears on the command line.
K6 = docker run --rm --network host -u "$$(id -u):$$(id -g)" -v "$(CURDIR)/load:/load" -w /load \
	$(addprefix -e ,$(K6_ENV)) $(K6_IMAGE) run --summary-export
TALLY_CLI = $(LOAD_COMPOSE) run --rm --no-deps --entrypoint /tally
LOAD_PASSWORD_CHECK = @test -n "$$LOAD_OWNER_PASSWORD" || { echo "Set LOAD_OWNER_PASSWORD (the load owners' password) in the environment" >&2; exit 1; }

# Creates new tenants; their owners are activated with LOAD_OWNER_PASSWORD from the environment.
load-seed: check-env
	$(LOAD_PASSWORD_CHECK)
	SEED_LOAD_OWNER_PASSWORD="$$LOAD_OWNER_PASSWORD" $(TALLY_CLI) -e SEED_LOAD_OWNER_PASSWORD api \
		seed load --tenants $(LOAD_TENANTS) --skus $(LOAD_SKUS) --moves $(LOAD_MOVES) --seed $(LOAD_SEED)

# pos-devices sells the contested stock, so it runs last; seed a new LOAD_SEED before the next run.
load-test: check-env
	$(LOAD_PASSWORD_CHECK)
	@mkdir -p $(LOAD_RESULTS)
	$(K6) test-results/lookup-summary.json k6/lookup.js
	$(K6) test-results/posting-summary.json k6/posting.js
	$(K6) test-results/reports-summary.json k6/reports.js
	$(K6) test-results/pos-devices-summary.json k6/pos-devices.js
	$(TALLY_CLI) api ledger verify

SOAK_STATS = docker stats --no-stream --format '{{.Name}}\t{{.CPUPerc}}\t{{.MemUsage}}\t{{.PIDs}}' $$($(LOAD_COMPOSE) ps -q)

soak-test: check-env
	$(LOAD_PASSWORD_CHECK)
	@mkdir -p $(LOAD_RESULTS)
	$(SOAK_STATS) > $(LOAD_RESULTS)/soak-stats-before.tsv
	$(K6) test-results/soak-summary.json k6/soak.js
	$(SOAK_STATS) > $(LOAD_RESULTS)/soak-stats-after.tsv
	$(TALLY_CLI) api ledger verify

# Like `make e2e`: needs the stack running (`make dev`) and E2E_OWNER_PASSWORD.
acceptance:
	cd apps/web && $(PNPM) e2e:acceptance

# Restores a base backup plus WAL into a throwaway stack (never the running one) and writes the
# measured RPO/RTO to test-results/restore-drill/. DRILL_PRIMARY is only read, to measure the WAL
# not archived yet; DRILL_ARGS passes --target-time, --tenant, --db-only, --keep ...
DRILL_PRIMARY ?= tally-prod-postgres-1
restore-drill: check-env
	infra/scripts/restore-drill.sh --env-file $(ENV_FILE) --primary $(DRILL_PRIMARY) $(DRILL_ARGS)

# Every scanner runs even when an earlier one fails; the target fails when any of them reports a
# finding. Images of SCAN_IMAGES that do not exist locally are skipped with a notice.
GOVULNCHECK    = golang.org/x/vuln/cmd/govulncheck@v1.9.0
# gosec through golangci-lint (the CI version), so the reviewed `//nolint:gosec` lines and the
# exclusions of apps/api/.golangci.yml apply; plain gosec ignores both.
GOLANGCI_LINT  = github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.7.2
TRIVY_IMAGE    = aquasec/trivy:0.76.0
GITLEAKS_IMAGE = zricethezav/gitleaks:v8.30.1
SCAN_IMAGES   ?= tally-api:$(VERSION) tally-web:$(VERSION) tally-walg:$(VERSION)
TRIVY = docker run --rm -v tally-trivy-cache:/root/.cache/trivy

security-scan:
	@status=0; \
	 echo "== govulncheck"; ( cd apps/api && go run $(GOVULNCHECK) ./... ) || status=1; \
	 echo "== gosec"; ( cd apps/api && go run $(GOLANGCI_LINT) run --enable-only gosec ./... ) || status=1; \
	 echo "== pnpm audit --prod"; ( cd apps/web && $(PNPM) audit --prod --audit-level high ) || status=1; \
	 echo "== trivy fs (dependencies, Dockerfile and compose misconfiguration)"; \
	 $(TRIVY) -v "$(CURDIR):/src:ro" $(TRIVY_IMAGE) fs --quiet --scanners vuln,misconfig \
		--severity HIGH,CRITICAL --exit-code 1 --skip-dirs '**/node_modules' /src || status=1; \
	 for image in $(SCAN_IMAGES); do \
		if docker image inspect "$$image" >/dev/null 2>&1; then \
			echo "== trivy image $$image"; \
			$(TRIVY) -v /var/run/docker.sock:/var/run/docker.sock:ro $(TRIVY_IMAGE) image --quiet \
				--severity HIGH,CRITICAL --ignore-unfixed --exit-code 1 "$$image" || status=1; \
		else echo "== trivy image $$image: not present locally, skipped"; fi; \
	 done; \
	 echo "== gitleaks (git history)"; \
	 docker run --rm -u "$$(id -u):$$(id -g)" -v "$(CURDIR):/repo:ro" $(GITLEAKS_IMAGE) \
		git --redact --no-banner /repo || status=1; \
	 exit $$status
