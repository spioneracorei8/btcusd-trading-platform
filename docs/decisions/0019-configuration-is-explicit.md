# 0019 — The environment file is passed explicitly, and compose has no defaults worth guessing

**Status:** accepted · **Date:** 2026-08-15 · **Amends:** [0010](0010-migrations-run-as-a-compose-service.md), [0017](0017-vps-deployment.md)

## Context

`deploy/docker-compose.yml` shipped with defaults for everything —
`${MARKET_SYMBOL:-BTCUSDT}`, `${MARKET_TIMEFRAMES:-1m,5m,15m,1h}`,
`${POSTGRES_PASSWORD:-trading}` — so that `docker compose up` worked with no
`.env` at all. The Makefile passed `--env-file .env` only when that file
happened to exist.

Both looked harmless. Together they produced a failure with no visible cause.

**Compose resolves `.env` relative to the project directory, which is the
directory holding the first `-f` file** — `deploy/`, not the repository root.
There is no `deploy/.env` and there never has been. So any compose command that
did not pass `--env-file` read no environment file whatsoever and fell straight
through to the baked-in defaults.

Reproduced against the real file: with a root `.env` setting
`MARKET_SYMBOL=ETHUSDT` and `MARKET_TIMEFRAMES=1m,5m,15m,1h,4h,1d`, a bare
`docker compose -f deploy/docker-compose.yml config` resolved to `BTCUSDT` and
`1m,5m,15m,1h`. No warning, exit status zero.

What that costs an operator: edit `.env`, recreate the containers, observe that
nothing changed. The stack is healthy, the values are plausible, and the file
just edited is simply not being read. There is no thread to pull on, because
nothing anywhere is wrong-looking.

## Decisions

### 1. Every compose invocation names the environment file, absolutely

`ENV_FILE := $(CURDIR)/.env`, passed unconditionally by every Makefile target
that shells out to compose. Absolute, because `$(CURDIR)` is where the file
lives regardless of which directory a target was invoked from — the same reason
`btcusd.service` spells out `/opt/btcusd/.env` rather than relying on
`WorkingDirectory`.

Unconditional, because the conditional form produced exactly the silent failure
above whenever the file was missing. `require-env` now explains that before
compose gets a chance to fail less helpfully.

### 2. Anything that decides what the system collects, or what its numbers
mean, has no default

These are `${VAR:?message}` and compose refuses to start without them:

`APP_ENV`, `POSTGRES_PASSWORD`, `MARKET_SYMBOL`, `MARKET_TYPE`,
`MARKET_TIMEFRAMES`, `FEE_TAKER_PCT`, `SLIPPAGE_TICKS`, `MARKET_TICK_SIZE`,
`MARKET_BACKFILL_FROM`.

The principle: **a default that is plausible but not what the operator chose is
worse than no default.** A missing value fails immediately, names itself, and
costs a minute. A wrong-but-reasonable value produces a healthy stack
collecting the wrong instrument, or backtests reported net of a cost model
nobody selected — and CLAUDE.md §3.4 exists precisely because a wrong cost
assumption makes every number downstream meaningless while looking entirely
normal.

This is the same reasoning that already governed `TAILSCALE_IP` in the
production overlay, where an empty value would have silently bound the public
interface. That case was treated as special. It was not special; it was the
first instance of a general rule.

### 3. Convenience values keep their defaults

`POSTGRES_USER`, `POSTGRES_DB`, `LOG_LEVEL`, `DATABASE_MAX_CONNS`, the host
port mappings, `MARKET_GAPCHECK_INTERVAL`, `COLLECTOR_HEARTBEAT_INTERVAL`,
`NOTIFY_ENABLED`, `FCM_*`, and the Binance base URLs.

Not because they matter less in principle, but because getting them wrong
announces itself. A wrong port fails to bind or fails to connect. A wrong
database name fails at the first query. `NOTIFY_ENABLED=false` fails closed.
None of them can quietly change what a number means.

The test for whether a default is acceptable is not "is this value sensible"
but "if this were wrong, how long before anyone noticed".

## Consequences

- `cp .env.example .env` is now a required first step rather than a suggested
  one, and `.env.example` carries every REQUIRED value already filled in, so
  copying it is sufficient to start.
- A bare `docker compose ...` against these files now fails with the name of
  the first variable it could not resolve. That is the intended outcome. The
  runbook says so, so it is not mistaken for a regression.
- `make prod-config` is the way to see what the containers will actually
  receive, and is the first thing to run when configuration appears not to
  apply.
- The promise in the compose file's header that it "works with no `.env` at
  all" is withdrawn. It was true, and it was the problem.
