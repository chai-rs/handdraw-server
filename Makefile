.PHONY: help run build fmt test test-integration mocks vet lint check migrate-create migrate-up migrate-down migrate-version test-migrations test-database test-content-compat check-all

help:
	@printf '%s\n' 'run / openapi / build / fmt / test / test-integration / mocks / vet / lint / check' 'migrate-create NAME=change_name' 'migrate-up [STEPS=N] / migrate-down STEPS=N / migrate-version' 'test-migrations (PostgreSQL migration suite)'

run:
	go run ./cmd/app

build:
	mkdir -p bin
	go build -o bin/handdraw-server ./cmd/app
	go build -o bin/handdraw-openapi ./cmd/openapi

fmt:
	golangci-lint fmt

test:
	go test ./...

test-integration:
	sh script/test-postgres.sh

mocks:
	go tool mockery

vet:
	go vet ./...

lint:
	golangci-lint run ./...

check: test vet lint build

migrate-create:
	sh script/migrate.sh create "$(NAME)"

migrate-up:
	sh script/migrate.sh up $(STEPS)

migrate-down:
	sh script/migrate.sh down "$(STEPS)"

migrate-version:
	sh script/migrate.sh version

# Migration tests execute reviewed SQL with the pinned golang-migrate CLI.
test-migrations:
	go test -tags=migrationtest -count=1 -timeout=5m ./internal/migrationtest

# Database suites use reviewed migrations in disposable PostgreSQL containers.
test-database:
	go test -tags=integration,migrationtest -race -count=1 -timeout=5m ./internal/migrationtest ./internal/board/infra/db ./internal/identity/infra/db ./app/access/infra/db

# The real editor and provider live in the adjacent client checkout.
CLIENT_DIR ?= ../handdraw-client
test-content-compat:
	go build -o bin/contentprobe ./tools/contentprobe
	cd "$(CLIENT_DIR)" && HANDDRAW_CONTENT_PROBE="$(CURDIR)/bin/contentprobe" npm run test:contracts

check-all: check test-database test-content-compat

.PHONY: openapi
openapi:
	go run ./cmd/openapi

.PHONY: local-cloud
# Explicit disposable fixture only; writes local endpoints to /private/tmp/handdraw-localstack.json.
local-cloud:
	HANDDRAW_LOCAL_STACK=1 go test -tags=integration -run '^TestLocalCloudStack$$' -count=1 -v -timeout=95m ./app/access/infra/db
