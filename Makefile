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

COMPOSE := docker compose $(if $(wildcard ./.env),--env-file .env,) -f $(COMPOSE_FILE)

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

.PHONY: test
test: ## Run unit tests (integration tests skip without TEST_DATABASE_URL)
	cd $(SERVER) && go test ./...

.PHONY: test-integration
test-integration: ## Start the database, migrate it and run every test
	$(COMPOSE) up -d postgres
	$(MAKE) migrate-up
	cd $(SERVER) && TEST_DATABASE_URL="$(DATABASE_URL)" go test -count=1 ./...

.PHONY: vet
vet: ## Run go vet
	cd $(SERVER) && go vet ./...

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
	@docker run --rm -i --network host postgres:16-alpine \
		psql "$(DATABASE_URL)" -c \
		"SELECT hypertable_name FROM timescaledb_information.hypertables WHERE hypertable_name = 'candles';"

.PHONY: sqlc
sqlc: ## Regenerate the sqlc query layer from the migrations
	cd $(SERVER) && $(SQLC) generate

# ---------------------------------------------------------------------------
# Docker
# ---------------------------------------------------------------------------

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
