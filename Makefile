# BTC/USDT analysis platform.
#
# Run `make help` for the list of targets.

SHELL := /bin/bash
.DEFAULT_GOAL := help

SERVER       := server
MIGRATIONS   := $(SERVER)/migrations
COMPOSE_FILE := deploy/docker-compose.yml

# Pinned tool versions. They are run with `go run <pkg>@<version>` instead of
# being required in go.mod: the goose CLI alone would add fifty indirect
# dependencies (ClickHouse, MSSQL, YDB, gRPC) to the application module.
GOOSE_VERSION := v3.24.3
GOOSE         := go run github.com/pressly/goose/v3/cmd/goose@$(GOOSE_VERSION)
SQLC_VERSION  := v1.29.0
SQLC          := go run github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION)

# Load the root .env when it exists so DATABASE_URL and friends are available
# to migration and compose targets without exporting them by hand.
ifneq (,$(wildcard ./.env))
include .env
export
endif

# Container engine. Both podman and docker provide a `compose` subcommand and
# read the same compose file, but they keep entirely separate container and
# volume namespaces: running one target under docker while the stack lives
# under podman silently builds a second, empty stack next to the real one
# rather than failing.
#
# podman wins the auto-detection because a machine with only docker has no
# podman to find, while a podman host often has the docker CLI installed as a
# shim. Override it when that guess is wrong:
#   make up CONTAINER_ENGINE=docker
# or set CONTAINER_ENGINE in .env to make the choice stick.
CONTAINER_ENGINE ?= $(shell command -v podman >/dev/null 2>&1 && echo podman || echo docker)

COMPOSE := $(CONTAINER_ENGINE) compose $(if $(wildcard ./.env),--env-file .env,) -f $(COMPOSE_FILE)

.PHONY: help
help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

# ---------------------------------------------------------------------------
# Go
# ---------------------------------------------------------------------------

.PHONY: build
build: ## Build every binary into server/bin
	cd $(SERVER) && go build -trimpath -o bin/api . && go build -trimpath -o bin/ ./collector ./backtest

# Tests run with the trenddebug tag so the cross-timeframe look-ahead
# assertion is compiled in and panics loudly. Shipped binaries build without
# it: CLAUDE.md §4 keeps panics out of business logic, and a collector must not
# die of an assertion on a VPS at 3am.
GOTAGS := -tags trenddebug

.PHONY: test
test: ## Run unit tests (integration tests skip without TEST_DATABASE_URL)
	cd $(SERVER) && go test $(GOTAGS) ./...

.PHONY: test-integration
test-integration: ## Start the database, migrate it and run every test
	$(COMPOSE) up -d postgres
	$(MAKE) migrate-up
	cd $(SERVER) && TEST_DATABASE_URL="$(DATABASE_URL)" go test $(GOTAGS) -count=1 ./...

.PHONY: vet
vet: ## Run go vet
	cd $(SERVER) && go vet ./... && go vet $(GOTAGS) ./...

.PHONY: fmt
fmt: ## Format the Go sources
	cd $(SERVER) && go fmt ./...

.PHONY: lint
lint: ## Run golangci-lint when installed, otherwise gofmt + go vet
	@cd $(SERVER) && \
	if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run ./...; \
	else \
		echo "golangci-lint not installed, falling back to gofmt + go vet"; \
		unformatted=$$(gofmt -l .); \
		if [ -n "$$unformatted" ]; then echo "not gofmt-clean:"; echo "$$unformatted"; exit 1; fi; \
		go vet ./...; \
	fi

.PHONY: tidy
tidy: ## Tidy go.mod / go.sum
	cd $(SERVER) && go mod tidy

.PHONY: check
check: build vet lint test ## Everything the definition of done requires

# ---------------------------------------------------------------------------
# Database
# ---------------------------------------------------------------------------

# require-db-url fails early with a useful message instead of handing an empty
# connection string to goose.
.PHONY: require-db-url
require-db-url:
	@if [ -z "$(DATABASE_URL)" ]; then \
		echo "DATABASE_URL is not set. Copy .env.example to .env or export it."; \
		exit 1; \
	fi

.PHONY: migrate-up
migrate-up: require-db-url ## Apply all migrations
	$(GOOSE) -dir $(MIGRATIONS) postgres "$(DATABASE_URL)" up

.PHONY: migrate-down
migrate-down: require-db-url ## Roll back the last migration
	$(GOOSE) -dir $(MIGRATIONS) postgres "$(DATABASE_URL)" down

.PHONY: migrate-status
migrate-status: require-db-url ## Show which migrations are applied
	$(GOOSE) -dir $(MIGRATIONS) postgres "$(DATABASE_URL)" status

.PHONY: verify-hypertable
verify-hypertable: require-db-url ## Prove that candles really is a hypertable
	@$(CONTAINER_ENGINE) run --rm -i --network host docker.io/library/postgres:16-alpine \
		psql "$(DATABASE_URL)" -c \
		"SELECT hypertable_name FROM timescaledb_information.hypertables WHERE hypertable_name = 'candles';"

.PHONY: sqlc
sqlc: ## Regenerate the sqlc query layer from the migrations
	cd $(SERVER) && $(SQLC) generate

# ---------------------------------------------------------------------------
# Containers
# ---------------------------------------------------------------------------

.PHONY: engine
engine: ## Show which container engine these targets will use
	@echo "CONTAINER_ENGINE = $(CONTAINER_ENGINE)"
	@$(CONTAINER_ENGINE) --version
	@echo "compose command  = $(COMPOSE)"

.PHONY: up
up: ## Build and start the stack
	$(COMPOSE) up --build -d

.PHONY: down
down: ## Stop the stack (keeps the pgdata volume)
	$(COMPOSE) down

.PHONY: logs
logs: ## Follow the container logs
	$(COMPOSE) logs -f

.PHONY: ps
ps: ## Show the container status
	$(COMPOSE) ps

.PHONY: adminer
adminer: ## Start Adminer to browse the database, then print its URL
	$(COMPOSE) --profile tools up -d adminer
	@echo
	@echo "Engine:    $(CONTAINER_ENGINE)"
	@echo "Adminer:   http://127.0.0.1:$(or $(ADMINER_HOST_PORT),8081)"
	@echo "System:    PostgreSQL"
	@echo "Server:    postgres        (pre-filled)"
	@echo "Username:  $(or $(POSTGRES_USER),trading)"
	@echo "Database:  $(or $(POSTGRES_DB),btcusd)"

.PHONY: adminer-stop
adminer-stop: ## Stop and remove Adminer, leaving the rest of the stack up
	$(COMPOSE) --profile tools rm -sf adminer
