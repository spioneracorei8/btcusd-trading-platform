# BTC/USDT analysis platform

Realtime BTC/USDT analysis for a single owner. The server watches the market
24/7 and pushes a notification when a signal appears.

**This system never places orders.** Its only outputs are a signal, the reason
behind it, and a notification. There is no order, trade or withdraw code path,
and no API key with trading rights is used — Binance market data is public.

Read [`CLAUDE.md`](CLAUDE.md) before writing any code. Phase prompts live in
[`docs/prompts/`](docs/prompts/), decisions in [`docs/decisions/`](docs/decisions/).

## Status

Phase 01 of 09 — skeleton, config, Docker, database schema. Market data,
indicators, the backtest engine and strategies are not implemented yet; the
`collector` and `backtest` binaries log their configuration and exit.

## Quick start

```bash
cp .env.example .env      # then edit the password before any real deployment
make up                   # build and start postgres + api + collector
make migrate-up           # create the tables and the candles hypertable
curl localhost:8080/health
```

## Layout

```
backend/
  cmd/api          REST server: /health, /ready
  cmd/collector    Binance ingestion worker (phase 02)
  cmd/backtest     backtest CLI (phase 04)
  internal/config  environment-only configuration and validation
  internal/domain  core types; imports no other package of this project
  internal/storage pgx v5 pool, sqlc queries, domain mapping
  internal/logging slog handler construction
  migrations       goose migrations
deploy/            docker-compose.yml, Caddyfile
docs/              decisions/, prompts/
mobile/            React Native app (phase 09)
```

## Make targets

| target | what it does |
|---|---|
| `make build` | build every binary into `backend/bin` |
| `make test` | unit tests; integration tests skip without `TEST_DATABASE_URL` |
| `make test-integration` | start the database, migrate it, run every test |
| `make lint` | golangci-lint when installed, otherwise gofmt + go vet |
| `make migrate-up` / `make migrate-down` | apply / roll back migrations |
| `make verify-hypertable` | prove `candles` really is a hypertable |
| `make sqlc` | regenerate the query layer from the migrations |
| `make up` / `make down` / `make logs` | manage the compose stack |
| `make check` | build + vet + lint + test |

## Design rules that shape the code

These come from `CLAUDE.md` and are enforced here, not just documented.

- **Only closed candles reach the strategy.** `UpsertCandle` rejects a candle
  with `IsClosed == false`, and the `candles` table has a `CHECK (is_closed)`
  constraint. Unclosed bars are display-only and stay in memory.
- **Every candle write is idempotent.** The primary key is
  `(symbol, market_type, timeframe, open_time)` and the insert is
  `ON CONFLICT ... DO UPDATE`, because a reconnect and a REST backfill routinely
  deliver the same bar twice.
- **No float64 for money.** Prices and volumes are `decimal.Decimal` end to end;
  see [ADR 0002](docs/decisions/0002-numeric-money-mapping.md).
- **Fees and slippage are configuration from day one** (`FEE_TAKER_PCT`,
  `SLIPPAGE_TICKS`), so no backtest can accidentally report gross results.
- **Every timestamp is UTC**, normalised on the way into and out of the database.
- **Spot vs futures is a config enum**, not a hardcoded endpoint, so the choice
  stays open.

## Configuration

All configuration comes from environment variables; there is no config file.
`APP_ENV`, `LOG_LEVEL`, `HTTP_PORT` and `DATABASE_URL` are required and a
process refuses to start without them, naming every variable that is missing or
invalid. See [`.env.example`](.env.example) for the full list.
