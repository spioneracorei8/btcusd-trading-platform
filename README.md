# BTC/USDT analysis platform

Realtime BTC/USDT analysis for a single owner. The server watches the market
24/7 and pushes a notification when a signal appears.

**This system never places orders.** Its only outputs are a signal, the reason
behind it, and a notification. There is no order, trade or withdraw code path,
and no API key with trading rights is used — Binance market data is public.

Read [`CLAUDE.md`](CLAUDE.md) before writing any code. Phase prompts live in
[`docs/prompts/`](docs/prompts/), decisions in [`docs/decisions/`](docs/decisions/).

## Status

Phases 01–06 of 09 are merged: skeleton and schema, market data ingestion,
the indicator engine, the backtest engine, the multi-timeframe trend filter,
and the strategy engine.

The backtest engine was deliberately built **before** any strategy, and the
trend filter before that too. Without a measuring instrument there is no way to
tell whether a strategy works.

`backtest --list-strategies` now reports three: `ema_crossover`,
`rsi_reversion` and `trend_pullback`. They are structurally different —
trend-following, counter-trend, trend-continuation — so their results say
something about the market rather than about parameter choices. They are
starting points for experiments, **not recommendations**; most rules of this
kind fail at 1m–5m once the 0.1% round trip is applied.

**No evaluation has been run yet.** `docs/acceptance-criteria.md` was written
before any strategy code, `docs/experiments.md` is the log every run is
appended to, and `docs/holdout-log.md` records each use of the 2025+ holdout.
The most probable outcome is that nothing clears the criteria; that is the
normal result, and the apparatus exists to make it believable when it arrives.

**The trend filter needs about 42 days of history before it says anything.** A
1h EMA(200) at a 5x warm-up is 1000 hourly closes, which is 60,000 1m bars. A
shorter run is vetoed end to end — the report separates `bars_vetoed` from
`bars_filter_not_ready` so that case is legible rather than mysterious.

## Quick start

```bash
cp .env.example .env      # required — then edit the password before any real deployment
make engine               # check which container engine will be used
make up                   # build and start postgres + migrate + api + collector
curl localhost:8080/health
```

The first line is not optional. The compose file has no defaults for the
values that decide what the system collects or what its numbers mean — symbol,
timeframes, fees, tick size, backfill start — so it refuses to start without
them rather than substituting something plausible. Every `make` target passes
`.env` to compose by absolute path; a bare `docker compose ...` reads no
environment file at all, because compose looks for one next to the compose file
rather than at the repository root. See
[ADR 0019](docs/decisions/0019-configuration-is-explicit.md).

The schema is applied by the `migrate` service, which runs before `api` and
`collector` start; `make migrate-up` is only needed against a database outside
the compose stack.

To browse the data by hand:

```bash
make adminer              # http://127.0.0.1:8081, server "postgres"
make adminer-stop         # when finished
```

### Running it on a server

The collector is meant to run continuously so the candle series keeps
accumulating; backtesting stays on the developer machine.
[`deploy/README.md`](deploy/README.md) is the runbook — provisioning, backups
with a tested restore, disk monitoring, and the verification checklist. The
decisions behind it are
[ADR 0017](docs/decisions/0017-vps-deployment.md).

Nothing about that host changes what this system does: it collects and
analyses, and places no orders.

### podman or docker

Both work — the compose file is the same for either. The Makefile picks podman
when it is installed and falls back to docker, which `make engine` prints.

Worth checking once, because the two engines keep entirely separate container
and volume namespaces: a target that runs under docker while the stack lives
under podman does not fail, it quietly builds a second, empty stack beside the
real one. Pin the choice in `.env` with `CONTAINER_ENGINE=podman` (or `docker`)
if the guess is wrong for your machine.

## Architecture

Clean architecture: a service declares its interfaces at the package root and
keeps the implementations in subpackages, so a usecase depends on
`CandleRepository` and never on the SQL behind it.

```
 entry point        main.go · collector/ · backtest/
       │            build config, wire everything, run
       ▼
 routes → handler   HTTP in, HTTP out. No rules live here.
       │
       ▼
 usecase            the rules. Knows no SQL, no HTTP, no vendor.
       │
       ▼
 repository         the outward edge: PostgreSQL, and Binance.
       │
       ▼
 database           pgx pool, sqlc output, wire-type conversion
```

Dependencies point one way and nothing below reaches back up.

**An outbound client is a repository, not a new layer.** `services/market/`
talks to Binance over WebSocket and REST from `repository/binance/`, because
an exchange is the same kind of thing as a database: an edge the system reads
from. Binance's DTOs stop at that package and become `models.*`. This is why
the backtest engine, which consumes `candle.CandleUsecase`, never learns that
Binance exists — live and replay differ only in where candles come from.

**The rules live in the usecase.** "An unclosed candle is never stored" is in
`candle.CandleUsecase`, not the repository: it is a statement about what the
system may reason over, not about how rows are written. The backtest engine
takes the usecase rather than the repository for the same reason — it must not
be able to reach around the rule by taking the faster route.

Four rules are checkable mechanically, and all four currently hold:

| rule | why |
|---|---|
| `constants` imports nothing from the project | any layer can depend on it without a cycle |
| `models` imports only `constants` | an entity cannot drag a layer in behind it |
| no `usecase/` package imports pgx | the rules stay testable without a database |
| a service's interface file *reaches* only `models` + `constants` | it may name another interface file, because that one is bound by the same rule; what it must never reach is a layer |

```
server/
  main.go          API entry point
  collector/       Binance ingestion worker (phase 02)
  backtest/        backtest CLI (phase 04)
  config/          environment-only configuration and validation
  constants/       enums, fixed values, sentinel errors
  helper/          small pure utilities
  logger/          slog handler construction
  middleware/      request id, request log, panic recovery
  models/          entities; imports only constants
  routes/          path -> handler wiring
  server/          repo -> usecase -> handler wiring, HTTP lifecycle
  database/        pgx v5 pool, sqlc output, wire-type conversion
  services/
    candle/        repository.go, usecase.go + repository/, usecase/
    signal/        repository.go, usecase.go + repository/, usecase/
    datagap/       repository.go, usecase.go + repository/, usecase/
    health/        handler.go, usecase.go, repository.go + impls
    market/        Binance client + ingestion (phase 02)
    indicator/     EMA, RSI, ATR, VWAP (phase 03)
    strategy/      strategy.go, params.go + usecase/ (phase 06)
    backtest/      backtest.go, outage.go, dataset.go + usecase/, report/ (phase 04)
    trend/         trend.go, config.go + usecase/ (phase 05)
  migrations/      goose migrations
  testhelper/      shared setup for repository integration tests
deploy/            compose files, VPS runbook, provisioning and backup scripts
docs/              decisions/, prompts/
mobile/            React Native app (phase 09)
```

`health` is the one complete vertical slice (handler → usecase → repository)
and is the template the later services follow.

Two things deliberately depart from the shape, both documented in
[ADR 0014](docs/decisions/0014-clean-architecture-in-practice.md):

- **The root of `strategy/`** is a contract, not a service. It reaches only
  `models` and `constants`, so it sits beside them in the dependency graph
  rather than above them, and `backtest.RunParams` naming a
  `strategy.Strategy` — or a `trend.Filter` — drags in no layer. Phase 06 added
  `strategy/usecase/`, which points down the graph like any other
  implementation package and changes nothing about what the root pulls in. The
  rule above is checked on the transitive closure, so this stays legal exactly
  as long as it stays true.
- **`backtest/report/`** renders for a CLI, not for HTTP. It does a handler's
  job but is not called `handler/`, because that name means HTTP everywhere
  else here and reusing it would make the convention useless as a signal.

Two rules with no exceptions are checked the same way, in the same file:
nothing references an order, account or withdrawal endpoint, and no code path
branches on whether it is running a backtest. Both are the sort of thing that
gets verified once by eye and then quietly violated three phases later.

The choice itself is [ADR 0005](docs/decisions/0005-clean-architecture-layout.md);
[ADR 0014](docs/decisions/0014-clean-architecture-in-practice.md) records how it
held up over four phases, including what it costs, and
[ADR 0016](docs/decisions/0016-strategy-evaluation-discipline.md) records what
phase 06 decided about strategies and how they get evaluated.

## Make targets

| target | what it does |
|---|---|
| `make build` | build every binary into `server/bin` |
| `make test` | unit tests; integration tests skip without `TEST_DATABASE_URL` |
| `make test-integration` | start the database, migrate it, run every test |
| `make lint` | golangci-lint when installed, otherwise gofmt + go vet |
| `make migrate-up` / `make migrate-down` | apply / roll back migrations |
| `make verify-hypertable` | prove `candles` really is a hypertable |
| `make sqlc` | regenerate the query layer from the migrations |
| `make up` / `make down` / `make logs` | manage the compose stack |
| `make engine` | show which container engine the targets will use |
| `make adminer` / `make adminer-stop` | database browser on `127.0.0.1:8081` |
| `go run ./backtest --help` | backtest CLI flags |
| `go run ./backtest --list-strategies` | strategies this binary can run, and their defaults |
| `scripts/sweep.sh --dry-run` | print the full strategy × timeframe matrix without running it |
| `scripts/sweep.sh` | run the matrix; every run appends to the experiment log |
| `go run ./backtest --compare ...` | same strategy, filter on and off, side by side |
| `make check` | build + vet + lint + test |

### Running a backtest

```bash
go run ./backtest --strategy=trend_pullback --timeframe=5m       # dev set, filtered
go run ./backtest --strategy=trend_pullback --compare            # filter on and off
go run ./backtest --strategy=trend_pullback --cost-sweep         # 1x, 1.5x, 2x costs
go run ./backtest --strategy=trend_pullback --dataset=holdout \
                  --note="final run"                             # logged, and spends the set
```

| flag | default | what it decides |
|---|---|---|
| `--strategy` | — | which registered strategy to run |
| `--dataset` | `dev` | `dev` (2023–2024, iterate freely) or `holdout` (2025+, every use logged) |
| `--from` / `--to` | — | explicit dates; the run is then labelled `custom`, neither set |
| `--risk-pct` | `1` | percent of equity risked per trade, sized off the stop |
| `--all-in` | off | commit the whole account per trade instead of sizing against the stop |
| `--cost-sweep` | off | rerun at 1.5x and 2x the assumed cost and print the sensitivity |
| `--trend-filter` / `--no-trend-filter` | filtered | gate entries on the multi-timeframe filter |
| `--compare` | off | run filtered and unfiltered, print both side by side |
| `--allow-gaps` | `halt` | `halt`, `skip` or `ignore` unfilled gaps |
| `--out` | — | also write the JSON report to this path |
| `--note` | — | recorded alongside a holdout use |

Costs are configuration (`FEE_TAKER_PCT`, `SLIPPAGE_TICKS`, `MARKET_TICK_SIZE`)
and there is no flag that changes them. A backtest whose costs could be tuned
from the command line would eventually be tuned until it looked good.

## Design rules that shape the code

These come from `CLAUDE.md` and are enforced here, not just documented.

- **Only closed candles reach the strategy.** `CandleUsecase.SaveCandle`
  rejects a candle with `IsClosed == false`, and the `candles` table has a
  `CHECK (is_closed)` constraint. Unclosed bars are display-only and stay in
  memory.
- **Every candle write is idempotent.** The primary key is
  `(symbol, market_type, timeframe, open_time)` and the insert is
  `ON CONFLICT ... DO UPDATE`, because a reconnect and a REST backfill routinely
  deliver the same bar twice.
- **No float64 for money.** Prices and volumes are `decimal.Decimal` end to end;
  see [ADR 0002](docs/decisions/0002-numeric-money-mapping.md).
- **Fees and slippage are configuration from day one** (`FEE_TAKER_PCT`,
  `SLIPPAGE_TICKS`, `MARKET_TICK_SIZE`), so no backtest can accidentally report
  gross results. The backtest report leads with net return and shows total
  costs on their own line.
- **Look-ahead is impossible to express, not merely forbidden.**
  `strategy.BarContext` carries the closed bar, its indicator snapshot and a
  copy of the position — no series, no index, no clock. A probe strategy that
  reaches for future data is compiled by the test suite and must fail to build;
  see [ADR 0011](docs/decisions/0011-look-ahead-is-structural.md).
- **A backtest refuses to run over data it does not trust.** Unfilled gaps halt
  the run by default, and known exchange outages are excluded under every
  policy; see [ADR 0013](docs/decisions/0013-untradeable-windows.md).
- **No look-ahead across timeframes either.** A higher-timeframe candle
  contributes only once its `close_time` is at or before the decision instant,
  so at 14:23 the newest usable 1h bar is the 13:00–14:00 one. The wrong
  implementation is kept beside the right one and a test asserts the check
  still rejects it; see
  [ADR 0015](docs/decisions/0015-cross-timeframe-alignment.md).
- **A trend filter is a veto, never a signal.** It decides when entering is
  permitted and cannot express an entry. Keeping the two apart is what makes
  "does the filter help" answerable — `--compare` runs the same strategy with
  and without it and prints the deltas.
- **An entry carries its own stop and target.** `strategy.Intent` has no way to
  express an entry without them, because phase 04 had a defect where a
  separately-issued stop was silently dropped and the position ran unprotected.
  Stops are ATR-scaled, and a configuration whose reward cannot clear the 0.1%
  round trip fails at construction rather than quietly in the results.
- **A signal stores the evidence behind it.** `signals.reason` is jsonb holding
  the bar, the indicator values, the trend verdict with each timeframe's
  `close_time`, and the levels. Indicators are never persisted, so this cannot
  be reconstructed later — and later is when a surprising alert needs auditing.
  A NaN indicator is refused rather than stored as zero.
- **The holdout set is a mirror, not a lock.** `--dataset=holdout` appends every
  use to `docs/holdout-log.md` before printing the numbers, so a run whose
  result was disliked is still on the record. It cannot be enforced; making a
  second use visible is the whole mechanism, and claiming more for it would be
  the same error the discipline exists to prevent.
- **Reports never select.** Cost sensitivity, regime splits, concentration and
  the parameter neighbourhood are printed and nothing chooses from them.
  Picking the best neighbour is the overfitting the report exists to detect;
  see [ADR 0016](docs/decisions/0016-strategy-evaluation-discipline.md).
- **Every timestamp is UTC**, normalised on the way into and out of the database.
- **Spot vs futures is a config enum**, not a hardcoded endpoint, so the choice
  stays open.

## Configuration

All configuration comes from environment variables; there is no config file.
`APP_ENV`, `LOG_LEVEL`, `HTTP_PORT` and `DATABASE_URL` are required and a
process refuses to start without them, naming every variable that is missing or
invalid. See [`.env.example`](.env.example) for the full list.
