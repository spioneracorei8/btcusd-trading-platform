# 0001 — Go module path and repository layout

**Status:** superseded in part by [0005](0005-clean-architecture-layout.md) — the module path stands, the layout moved to clean architecture and `backend/` is now `server/`
**Original status:** accepted · **Date:** 2026-08-01 · **Phase:** 01

## Context

`CLAUDE.md` section 5 fixes the folder structure and says the module should be
named `github.com/<user>/trading-platform/backend`, with `<user>` to be
confirmed. The repository this code lives in is
`spioneracorei8/btcusd-trading-platform`.

## Decision

The module path is:

```
github.com/spioneracorei8/btcusd-trading-platform/server
```

The path matches the actual repository rather than the placeholder
`trading-platform`, so `go get` and any future tooling resolve it without a
`replace` directive.

Two directories exist that are not listed in `CLAUDE.md` section 5:

- `backend/internal/logging` — slog handler construction, needed by all three
  binaries. Putting it in `cmd/api` would have meant copying it into
  `cmd/collector` and `cmd/backtest`.
- `backend/internal/storage/db` — the sqlc output package. `CLAUDE.md` already
  designates `storage/` as the home of "postgres, sqlc generated"; a subpackage
  keeps generated files from mixing with hand-written ones.

The HTTP router, middleware and handlers live in `cmd/api` rather than a new
`internal/http` package, because nothing else in the system serves HTTP.

## Consequences

- The import direction stays `cmd → internal/* → domain`. `internal/domain`
  imports only the standard library, `shopspring/decimal` and `google/uuid`.
- If the repository is ever renamed, the module path and every import must be
  updated together.
