# Development defaults matching infra/docker-compose.yml (project "tally").
# Override any of them in the environment or in apps/api/.env (git-ignored).
POSTGRES_HOST_PORT    ?= 5442
APP_ENV               ?= development
APP_TZ                ?= Asia/Ho_Chi_Minh
LOG_LEVEL             ?= info
PUBLIC_BASE_URL       ?= http://localhost:5173
DATABASE_URL          ?= postgres://tally_app:tally_app_dev@localhost:$(POSTGRES_HOST_PORT)/tally?sslmode=disable
MIGRATE_DATABASE_URL  ?= postgres://tally_owner:tally_owner_dev@localhost:$(POSTGRES_HOST_PORT)/tally?sslmode=disable
SMTP_HOST             ?= localhost
SMTP_PORT             ?= 1025
MAIL_FROM             ?= Tally <no-reply@tally.localhost>
OBJECT_STORE_ENDPOINT ?= http://localhost:9000
OBJECT_STORE_REGION   ?= us-east-1
OBJECT_STORE_BUCKET   ?= tally-dev
OBJECT_STORE_ACCESS_KEY ?= tally-dev
OBJECT_STORE_SECRET_KEY ?= tally-dev-secret
export APP_ENV APP_TZ LOG_LEVEL PUBLIC_BASE_URL DATABASE_URL MIGRATE_DATABASE_URL SMTP_HOST SMTP_PORT MAIL_FROM \
	OBJECT_STORE_ENDPOINT OBJECT_STORE_REGION OBJECT_STORE_BUCKET OBJECT_STORE_ACCESS_KEY OBJECT_STORE_SECRET_KEY

# apps/api/.env (optional) wins over the defaults above.
WITH_ENV = set -a; if [ -f .env ]; then . ./.env; fi; set +a;
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS  = -s -w -X main.version=$(VERSION)

.PHONY: run worker migrate-up migrate-down seed test test-integration lint fmt bin

run:
	@$(WITH_ENV) go run -ldflags "$(LDFLAGS)" ./cmd/tally serve

worker:
	@$(WITH_ENV) go run -ldflags "$(LDFLAGS)" ./cmd/tally worker

migrate-up:
	@$(WITH_ENV) go run ./cmd/tally migrate up

migrate-down:
	@$(WITH_ENV) go run ./cmd/tally migrate down

# seed dev runs only with APP_ENV set to development or test; the default above sets development,
# and an APP_ENV=production from the environment or apps/api/.env makes it refuse.
seed:
	@$(WITH_ENV) go run ./cmd/tally seed dev

test:
	go test -race -count=1 ./...

# Integration tests start their own postgres:16-alpine through testcontainers (Docker required) and
# use the dev mailpit and minio from infra/docker-compose.yml (`make db-up` at the repository root).
test-integration:
	@$(WITH_ENV) go test -race -count=1 -tags integration ./...

lint:
	go vet ./...
	go vet -tags integration ./...
	golangci-lint run ./...

fmt:
	gofmt -w .

bin:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/tally ./cmd/tally
