# 0010 — Migrations run as a compose service

**Status:** accepted · **Date:** 2026-08-07 · **Phase:** 02 (fix)

## Context

A fresh `compose up` on an empty volume crashed the collector on a missing
`collector_status` table. Nothing in the stack applied the schema — `make
migrate-up` did, and the assumption that someone would run it by hand before
starting the stack held right up until the first deployment on a clean machine.

The obvious fix, migrating from inside the application at startup, is worse than
it looks: two containers start at once, so both would race on the same schema,
and a migration failure would be entangled with an application failure in the
same log.

## Decision

A `migrate` service runs goose once and exits. `api` and `collector` wait for it
with `depends_on: service_completed_successfully`, and it waits for postgres
with `service_healthy`. goose is idempotent, so running it on every startup is
both safe and the intent — the schema is brought to head before anything
connects, every time.

goose is compiled from source in our own builder stage rather than pulled as a
separate image, and stays out of `go.mod`. Its dialect drivers pull in
ClickHouse, MSSQL, YDB and Docker — 86 modules for a tool the application never
imports. This holds for the library too, not only the CLI, which is why ADR 0004
kept it out in the first place; this only extends that decision to the image.

The migrate image is configured entirely through `GOOSE_DRIVER`, `GOOSE_DBSTRING`
and `GOOSE_MIGRATION_DIR`. The distroless base has no shell, so a `command:`
containing `$VAR` would be passed through literally rather than expanded —
environment variables are the only form that works.

## Consequences

- The stack is reproducible from an empty volume with one command, which is the
  property the phase-01 definition of done actually meant by "`docker compose up`
  แล้วรันได้จริง".
- A bad migration stops the stack at the migrate service, with the failure in
  its own log rather than buried in application startup.
- `restart: "no"` on the migrate service: a migration that failed will fail the
  same way again, and a restart loop would only obscure the error.
- The `app` stage is last in the Dockerfile so it stays the default build
  target; `api` and `collector` name it explicitly anyway, so reordering the
  file cannot silently ship the migrate image as the application.
- `make migrate-up` still exists and is unchanged. It is the right tool against
  a database that is not part of the compose stack.
