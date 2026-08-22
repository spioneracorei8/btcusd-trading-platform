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

# The environment file, as an absolute path.
#
# # Why this is passed explicitly, always
#
# Compose resolves a relative --env-file against the current directory, but
# when none is given it looks for `.env` in the *project directory* — which is
# the directory holding the first -f file, so `deploy/`. There is no
# `deploy/.env` and there never was, so an omitted --env-file silently fell
# through to the defaults baked into the compose file: MARKET_SYMBOL=BTCUSDT,
# MARKET_TIMEFRAMES=1m,5m,15m,1h, and so on.
#
# Silently is the whole problem. Editing .env, recreating the containers and
# seeing nothing change gives the reader no thread to pull on — the values are
# plausible, the stack is healthy, and the file they just edited is simply not
# being read.
#
# It was previously conditional, which meant a missing .env produced exactly
# that failure. Passing it unconditionally turns the same situation into
# compose refusing to start, and require-env explains it first. Absolute
# because $(CURDIR) is where .env lives whatever directory a target is invoked
# from, which is the same reason the systemd unit on the VPS spells it out.
ENV_FILE := $(CURDIR)/.env

COMPOSE := $(CONTAINER_ENGINE) compose --env-file $(ENV_FILE) -f $(COMPOSE_FILE)

.PHONY: help
help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

# ---------------------------------------------------------------------------
# Go
# ---------------------------------------------------------------------------

.PHONY: build
build: ## Build every binary into server/bin
	cd $(SERVER) && go build -trimpath -o bin/api . && go build -trimpath -o bin/ ./collector ./backtest ./reconcile

# The two read-only CLIs. Both take their flags through ARGS:
#
#   make backtest  ARGS="--strategy ema_crossover --timeframe 4h"
#   make reconcile ARGS="--days 90"
#
# Neither places an order. backtest scores history; reconcile compares live
# outcomes against what the engine says the same period should have produced.

.PHONY: backtest
backtest: require-db-url ## Run the backtest CLI (flags via ARGS)
	cd $(SERVER) && go run ./backtest $(ARGS)

.PHONY: reconcile
reconcile: require-db-url ## Compare live outcomes against backtest predictions (flags via ARGS)
	cd $(SERVER) && go run ./reconcile $(ARGS)

# Tests run with the trenddebug tag so the cross-timeframe look-ahead
# assertion is compiled in and panics loudly. Shipped binaries build without
# it: CLAUDE.md §4 keeps panics out of business logic, and a collector must not
# die of an assertion on a VPS at 3am.
GOTAGS := -tags trenddebug

.PHONY: test
test: ## Run unit tests (integration tests skip without TEST_DATABASE_URL)
	cd $(SERVER) && go test $(GOTAGS) ./...

.PHONY: test-integration
test-integration: require-env ## Start the database, migrate it and run every test
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

# require-env explains a missing .env before compose does.
#
# Compose's own message for an absent --env-file names the path and nothing
# else, which is a poor place to learn that the file was supposed to exist.
.PHONY: require-env
require-env:
	@if [ ! -f "$(ENV_FILE)" ]; then \
		echo "$(ENV_FILE) does not exist."; \
		echo; \
		echo "Every compose target reads it, and the compose file no longer"; \
		echo "carries defaults for the values that decide what this system"; \
		echo "collects and what its numbers mean. Create it with:"; \
		echo; \
		echo "    cp .env.example .env"; \
		echo; \
		echo "then set POSTGRES_PASSWORD before deploying anywhere real."; \
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
up: require-env ## Build and start the stack
	$(COMPOSE) up --build -d

.PHONY: down
down: require-env ## Stop the stack (keeps the pgdata volume)
	$(COMPOSE) down

.PHONY: logs
logs: require-env ## Follow the container logs
	$(COMPOSE) logs -f

.PHONY: ps
ps: require-env ## Show the container status
	$(COMPOSE) ps

.PHONY: adminer
adminer: require-env ## Start Adminer to browse the database, then print its URL
	$(COMPOSE) --profile tools up -d adminer
	@echo
	@echo "Engine:    $(CONTAINER_ENGINE)"
	@echo "Adminer:   http://127.0.0.1:$(or $(ADMINER_HOST_PORT),8081)"
	@echo "System:    PostgreSQL"
	@echo "Server:    postgres        (pre-filled)"
	@echo "Username:  $(or $(POSTGRES_USER),trading)"
	@echo "Database:  $(or $(POSTGRES_DB),btcusd)"

.PHONY: adminer-stop
adminer-stop: require-env ## Stop and remove Adminer, leaving the rest of the stack up
	$(COMPOSE) --profile tools rm -sf adminer

# ---------------------------------------------------------------------------
# VPS deployment
# ---------------------------------------------------------------------------
# These are meant to be run ON the VPS, from /opt/btcusd. On the host itself
# the stack is normally driven by systemd (`systemctl start btcusd`); these
# targets are for the times you are logged in and want the compose command
# without retyping both -f flags.
#
# The production overlay adds the Tailscale binding and the 4 GB PostgreSQL
# tuning, and refuses to start when TAILSCALE_IP is unset.

PROD_COMPOSE := $(CONTAINER_ENGINE) compose --env-file $(ENV_FILE) \
	-f $(COMPOSE_FILE) -f deploy/docker-compose.prod.yml

.PHONY: prod-up
prod-up: require-env ## VPS: build and start the production stack
	$(PROD_COMPOSE) up --build -d --remove-orphans

.PHONY: prod-down
prod-down: require-env ## VPS: stop the production stack (keeps the pgdata volume)
	$(PROD_COMPOSE) down

.PHONY: prod-ps
prod-ps: require-env ## VPS: show the production container status
	$(PROD_COMPOSE) ps

.PHONY: prod-logs
prod-logs: require-env ## VPS: follow the production container logs
	$(PROD_COMPOSE) logs -f

.PHONY: prod-config
prod-config: require-env ## VPS: print the merged production compose configuration
	$(PROD_COMPOSE) config

.PHONY: backup
backup: ## VPS: dump the database now, with rotation
	sudo deploy/backup.sh

.PHONY: restore-test
restore-test: ## VPS: restore the newest dump into a scratch database and verify it
	sudo deploy/restore-test.sh

.PHONY: disk-check
disk-check: ## VPS: report disk usage for the root and postgres filesystems
	sudo deploy/disk-check.sh
