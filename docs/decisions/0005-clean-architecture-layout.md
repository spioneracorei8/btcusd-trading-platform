# 0005 — Clean architecture layout and the rename to server/

**Status:** accepted · **Date:** 2026-08-01 · **Supersedes part of:** [0001](0001-go-module-path-and-layout.md)

## Context

Phase 01 shipped with the layout from `CLAUDE.md` section 5: `backend/` with
`cmd/`, `internal/domain`, `internal/storage` and so on.

The owner asked for the repository to follow the same clean architecture as
their other services — `go-clean-arch-auth-service`, `go-clean-arch-user-service`
and `job-queue` — and for `backend/` to be renamed `server/`.

Those reference repositories share one convention:

- `services/<domain>/` holds the **interfaces** (`handler.go`, `usecase.go`,
  `repository.go`) and the **implementations** live in the `handler/`,
  `usecase/` and `repository/` subpackages.
- Constructors are named `New<Thing>Impl` and return the interface, not the
  struct.
- Supporting packages sit at the root: `config/`, `constants/`, `helper/`,
  `logger/`, `middleware/`, `models/`, `routes/`, `server/`.
- `server/server.go` wires repositories into usecases into handlers, under
  banner comments, and is the only place that knows the implementations.

They also use gin, GORM, logrus and `AutoMigrate`, which contradict
`CLAUDE.md`: ORM is forbidden outright, logging must be `log/slog`, and phase
01 specified chi, sqlc and goose.

## Decision

Adopt the **layout and layering** from the reference repositories; keep the
**tech stack** mandated by `CLAUDE.md`. Confirmed with the owner before the
work started.

| Concern | Reference repos | Here |
|---|---|---|
| Layering, folders, `New…Impl` | ✅ adopted | ✅ |
| HTTP router | gin | chi |
| Database access | GORM | pgx v5 + sqlc |
| Migrations | `AutoMigrate` | goose |
| Logging | logrus | `log/slog` |
| Config | globals + `init()` | validated struct, fail-fast |
| Module path | `module user-service` | `github.com/spioneracorei8/btcusd-trading-platform/server` |

Two deviations from the reference convention are deliberate:

- **Config stays a validated struct** rather than package-level globals set in
  `init()`. `CLAUDE.md` requires a process to fail at startup naming every
  missing variable, which `init()` cannot do cleanly, and the struct is what
  makes the config tests possible.
- **`database/` exists**, which the reference repos have no equivalent of
  because GORM needs no generated code. It holds the pgx pool, the sqlc output
  and the wire-type conversions.

## Mapping

| Before | After |
|---|---|
| `backend/` | `server/` |
| `cmd/api/main.go` | `main.go` + `server/server.go` |
| `cmd/collector`, `cmd/backtest` | `collector/`, `backtest/` |
| `cmd/api/router.go` | `routes/api.go` |
| `cmd/api/middleware.go` | `middleware/middleware.go` |
| `cmd/api/handlers.go` | `services/health/handler/health_handler.go` |
| `internal/config` | `config/env.go` |
| `internal/logging` | `logger/logger.go` |
| `internal/domain` (structs) | `models/` |
| `internal/domain` (enums) | `constants/enum.go` |
| `internal/storage` (pool, convert) | `database/` |
| `internal/storage.Store` | `services/{candle,signal,datagap}/repository/` |

## Consequences

- The one god-object repository (`storage.Store`) is gone; each domain owns
  its own repository behind its own interface.
- `health` is a full vertical slice (handler → usecase → repository) and is
  the template every later service follows.
- The rule that an unclosed candle is never stored moved from the repository
  to `candle.CandleUsecase`, where it belongs: it is a rule about what the
  system may reason over, not about how rows are written. It now has a unit
  test that needs no database.
- Behaviour is unchanged. The same integration tests pass against a real
  PostgreSQL, and `/health`, `/ready`, graceful shutdown and the config
  validation all behave exactly as before the move.
- `CLAUDE.md` section 5 was rewritten to describe this layout, since it is the
  contract later phases are checked against.
