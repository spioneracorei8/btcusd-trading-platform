# 0004 — goose/sqlc invocation and how integration tests get a database

**Status:** accepted · **Date:** 2026-08-01 · **Phase:** 01

## Context

Two questions had to be settled in phase 01.

**How are goose and sqlc invoked?** The usual `tools.go` pattern puts the CLI
in `go.mod`. Measured: adding `github.com/pressly/goose/v3/cmd/goose` brought in
50 indirect dependencies — ClickHouse, MSSQL, YDB, Vertica, libsql, gRPC,
OpenTelemetry — and grew `go.sum` from 34 to 369 lines. All of it would be
downloaded by `go mod download` in every Docker build, for a tool the
application never imports.

**How do the storage integration tests get a database?** `phase-01.md` offers
testcontainers or skipping when Docker is unavailable.

## Decision

Tools are pinned in the `Makefile` and run with `go run <pkg>@<version>`:

```make
GOOSE_VERSION := v3.24.3
SQLC_VERSION  := v1.29.0
```

They stay out of `go.mod`, so the application module has four direct
dependencies and the Docker build downloads only what the binaries import.

Integration tests read `TEST_DATABASE_URL` and skip when it is unset, rather
than using testcontainers. `make test-integration` starts the compose database,
migrates it and sets the variable.

## Consequences

- `go test ./...` passes on a machine with no Docker; those tests report SKIP.
- `make migrate-up` and `make sqlc` need network access on first use, until the
  module cache is warm. On an air-gapped host, install the binaries manually.
- Rejecting testcontainers keeps the dependency out of the module, at the cost
  of the tests not provisioning their own database. `make test-integration`
  covers the gap.
- Tests write to symbols prefixed `TEST` and delete their rows in `t.Cleanup`,
  so a run against a database holding real candles cannot damage it.
