# BTC/USDT analysis platform

Realtime BTC/USDT analysis for a single owner. The server watches the market
24/7 and pushes a notification when a signal appears.

**This system never places orders.** Its only outputs are a signal, the reason
behind it, and a notification. There is no order, trade or withdraw code path,
and no API key with trading rights is used — Binance market data is public.

Read [`CLAUDE.md`](CLAUDE.md) before writing any code. Phase prompts live in
[`docs/prompts/`](docs/prompts/), decisions in [`docs/decisions/`](docs/decisions/).

## Status

Phases 01–04 of 09 are merged: skeleton and schema, market data ingestion,
the indicator engine, and the backtest engine.

The backtest engine is deliberately built **before** any strategy. Without a
measuring instrument there is no way to tell whether a strategy works, so
`backtest --list-strategies` reports an empty registry on purpose; strategies
arrive in phase 06.

## Quick start

```bash
cp .env.example .env      # then edit the password before any real deployment
make engine               # check which container engine will be used
make up                   # build and start postgres + migrate + api + collector
curl localhost:8080/health
```

The schema is applied by the `migrate` service, which runs before `api` and
`collector` start; `make migrate-up` is only needed against a database outside
the compose stack.

To browse the data by hand:

```bash
make adminer              # http://127.0.0.1:8081, server "postgres"
make adminer-stop         # when finished
```

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
| a service's interface file imports only `models` + `constants` | one documented exception, see [ADR 0014](docs/decisions/0014-clean-architecture-in-practice.md) |

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
    strategy/      strategy.go — the interface only, no implementations
    backtest/      backtest.go, outage.go + usecase/, report/ (phase 04)
  migrations/      goose migrations
  testhelper/      shared setup for repository integration tests
deploy/            docker-compose.yml, Caddyfile
docs/              decisions/, prompts/
mobile/            React Native app (phase 09)
```

`health` is the one complete vertical slice (handler → usecase → repository)
and is the template the later services follow.

Two services deliberately break the shape, both documented in
[ADR 0014](docs/decisions/0014-clean-architecture-in-practice.md):

- **`strategy/`** is a contract package, not a service. It holds no
  implementations and none are coming — phase 04 built the measuring
  instrument before there was anything to measure — so it sits beside `models`
  and `constants` in the dependency graph rather than above them.
- **`backtest/report/`** renders for a CLI, not for HTTP. It does a handler's
  job but is not called `handler/`, because that name means HTTP everywhere
  else here and reusing it would make the convention useless as a signal.

The choice itself is [ADR 0005](docs/decisions/0005-clean-architecture-layout.md);
[ADR 0014](docs/decisions/0014-clean-architecture-in-practice.md) records how it
held up over four phases, including what it costs.

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
| `make check` | build + vet + lint + test |

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
- **Every timestamp is UTC**, normalised on the way into and out of the database.
- **Spot vs futures is a config enum**, not a hardcoded endpoint, so the choice
  stays open.

## Configuration

All configuration comes from environment variables; there is no config file.
`APP_ENV`, `LOG_LEVEL`, `HTTP_PORT` and `DATABASE_URL` are required and a
process refuses to start without them, naming every variable that is missing or
invalid. See [`.env.example`](.env.example) for the full list.
