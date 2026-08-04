# Phase 02 — Market Data Ingestion

> Read `CLAUDE.md` fully before starting.
> Phase 01 must be complete and merged: schema exists, config loads, `docker compose up` works.
> **No indicators, no strategy, no signals in this phase.**

This is the foundation everything else rests on. If the candle data is wrong, every indicator, backtest, and signal built on top of it is wrong — silently. Correctness matters far more than speed or elegance here.

---

## 0. Structure

The project uses clean architecture (`CLAUDE.md` section 5, ADR 0005): interfaces at the service root, implementations in subpackages. This phase adds one new service, `market`, and extends two existing ones.

```
server/
├── collector/main.go                 # entry point, wires the ingestion pipeline
├── constants/
│   ├── constant.go                   # + backoff schedule, WS/REST limits, timeouts
│   └── error.go                      # + ErrRateLimited, ErrStaleConnection, ...
├── config/env.go                     # + MARKET_BACKFILL_FROM, MARKET_GAPCHECK_INTERVAL,
│                                     #   BINANCE_REST_BASE_URL, BINANCE_WS_BASE_URL
├── models/
│   ├── candle.go                     # unchanged — the boundary type
│   └── market.go                     # + MarketStatus (for the status endpoint)
├── database/queries/
│   ├── candles.sql                   # + BatchUpsertCandles, FindCandleGaps (window function)
│   └── gaps.sql                      # + MarkGapFilled, CountUnfilledGaps, ListUnfilledGaps
└── services/
    ├── market/                       # NEW — ingestion domain
    │   ├── repository.go             #   MarketRepository: FetchKlines, StreamKlines
    │   ├── repository/binance/       #   Binance impl: DTOs, REST client, WS client
    │   ├── usecase.go                #   MarketUsecase: Ingest, Backfill, Status
    │   ├── usecase/                   #   pipeline, backfill, cache, backoff
    │   ├── handler.go                #   MarketHandler: GET /internal/market/status
    │   └── handler/
    ├── candle/                       # extended: batch upsert, latest open_time
    └── datagap/                      # extended: gap detection + fill (was "gapcheck")
```

**Mapping from the original phase-02 draft**, which was written against the pre-clean-architecture layout:

| Draft | Here |
|---|---|
| `internal/market/binance` | `services/market/repository/binance/` |
| `internal/market/gapcheck` | `services/datagap/usecase/` + SQL in `database/queries/gaps.sql` |
| `internal/storage` | `services/<domain>/repository/` |
| `domain.Candle` | `models.Candle` |
| `internal/indicator` (downstream) | `services/indicator/` (phase 03) |

**Layering rules that this phase must not break:**

- The Binance client is a *repository*. It is the outbound edge, exactly like PostgreSQL is.
- `MarketUsecase` orchestrates ingestion and knows nothing about WebSocket frames, HTTP headers or SQL.
- No Binance-shaped type may appear outside `services/market/repository/binance/`. Convert to `models.Candle` at the package boundary.

---

## Goal

The `collector` binary runs continuously, keeps `candles` complete and accurate for all configured timeframes, survives disconnects and restarts without losing or duplicating data, and records any gap it cannot immediately fill.

Definition of "complete": for every configured timeframe, there is exactly one row per expected `open_time` between the first and last stored candle, with no missing intervals.

---

## 1. Binance client

Create `services/market/repository/binance`.

**Endpoints (public, no API key required):**
- REST klines: `GET /api/v3/klines` (spot)
- WebSocket stream: `wss://stream.binance.com:9443/stream?streams=btcusdt@kline_1m/btcusdt@kline_5m/...`

Do not hardcode base URLs. Put them in config so `futures` can be swapped in later without touching call sites. Do not implement futures endpoints in this phase.

**Requirements:**
- A single combined WebSocket connection for all configured timeframes, not one connection per timeframe.
- Respect Binance REST rate limits. Read the `X-MBX-USED-WEIGHT-1M` response header and back off before hitting the cap. Treat HTTP 429 and 418 as hard stops with backoff, never as retryable-immediately.
- Parse all prices and volumes as `decimal.Decimal`, never `float64`. Binance returns them as JSON strings — keep them as strings until decimal parsing.
- Every REST and WS struct gets its own DTO type in the binance package. Convert to `models.Candle` at the package boundary. No Binance-shaped types may leak into the repository interface, the usecases, or anywhere downstream.

---

## 2. Closed-candle discipline

This is the single most important rule in this phase.

The kline payload contains `k.x` (boolean, "is this kline closed"). Behaviour:

- `k.x == true` → this is a final candle. Upsert it into `candles`. Emit it on the closed-candle channel.
- `k.x == false` → this is an in-progress candle. Store it **in memory only**, in a `LatestCandleCache` keyed by `(symbol, timeframe)`. Never write it to the `candles` table. Never emit it on the closed-candle channel.

The `candles` table must only ever contain closed candles. `is_closed` exists in the schema as a defensive assertion, not as a flag to filter on later — reject any write where it would be false.

`CandleUsecase.SaveCandle` already enforces this and returns `constants.ErrUnclosedCandle`; the batch path must enforce it too rather than bypassing the usecase.

Add a unit test that feeds a synthetic stream containing both open and closed klines and asserts that only closed ones reach the storage layer.

---

## 3. Connection lifecycle

WebSocket disconnects are routine, not exceptional. Design for them.

- Reconnect with exponential backoff: start 1s, double up to 60s max, with jitter (±20%) to avoid thundering-herd on Binance's side after an outage.
- Binance closes idle connections after 24 hours. Handle this as a normal event, not an error — reconnect without logging at error level.
- Respond to WebSocket ping frames with pong. If no message of any kind arrives for 3 minutes, treat the connection as dead and reconnect even if the socket appears open — a silently stalled connection is the failure mode that quietly corrupts data.
- The collector must exit cleanly on SIGTERM: stop accepting new messages, finish in-flight writes, close the pool, exit within 10 seconds.

Log every disconnect and reconnect at info level with the reason and downtime duration. You will need this history later when a backtest result looks suspicious.

---

## 4. Backfill

Two triggers:

**A. Startup backfill.** On every start, for each configured timeframe:
- Query the latest stored `open_time`.
- If none exists, fetch history back to `MARKET_BACKFILL_FROM` (new env var, RFC3339, default `2023-01-01T00:00:00Z`).
- If one exists, fetch from that timestamp forward to now.

**B. Reconnect backfill.** After every successful WebSocket reconnect, backfill from the last stored candle to now, for every timeframe. Do this **before** resuming normal ingestion of live closed candles, so the sequence stays ordered.

**Implementation notes:**
- Binance returns max 1000 klines per REST call. Page with `startTime`/`endTime`, advancing by the last received `open_time + 1ms`.
- Never trust the final candle returned by a REST kline call — it may be the currently-open one. Discard any returned kline whose `close_time` is in the future relative to server time.
- Batch upserts: use `pgx.CopyFrom` into a temp table then merge, or batched `INSERT ... ON CONFLICT DO UPDATE` in chunks of 1000. A row-at-a-time loop over three years of 1m candles is unacceptable.
- Backfill must be resumable. If the process dies at 60% through a three-year backfill, restarting continues from where it stopped — this falls out naturally from keying on latest stored `open_time`, so verify it rather than adding bespoke state.

---

## 5. Gap detection

Extend `services/datagap` (the draft called this `internal/market/gapcheck`).

A gap is any missing `open_time` in the expected sequence for a timeframe, where the expected sequence is every interval boundary between the earliest and latest stored candle.

- Run a full scan after startup backfill completes, and on a ticker (default every 15 minutes, configurable via `MARKET_GAPCHECK_INTERVAL`).
- Detect gaps with a SQL window function comparing each row's `open_time` to the previous row's — do not pull the whole series into Go memory to diff it.
- For each gap found: insert a row into `data_gaps`, then attempt an immediate REST backfill of that range. On success, set `filled_at`. On failure, leave it unfilled and log at warn level with the range.
- If a gap remains unfilled after 3 attempts, stop retrying it automatically and mark it in `note`. Some ranges genuinely do not exist — Binance has had real outages, and a symbol may have no trades in a low-volume minute.

**Important:** an unfilled gap is not a crash condition. The collector keeps running. But Phase 4's backtest engine will read `data_gaps` and refuse to produce results over ranges that contain them, so record them faithfully.

---

## 6. Ingestion pipeline structure

```
WS reader goroutine ──► parse+validate ──► closedCandleCh ──► writer goroutine ──► storage
                                    └────► LatestCandleCache (in-memory, open candles)
```

- One reader goroutine per connection, one writer goroutine. Bounded channel (capacity 1000).
- If the channel fills, log at error level and block rather than dropping. Dropping a candle silently is the worst possible failure here.
- Every goroutine takes a `context.Context` and returns when it is cancelled.
- Use `errgroup` to supervise; if any component returns a non-recoverable error, the whole collector shuts down cleanly so the container restarts.

The reader and writer live in `services/market/usecase`; the WS connection itself lives in `services/market/repository/binance`.

---

## 7. Observability

Add `GET /internal/market/status` to the api service returning, per timeframe:
- latest stored `open_time` and its age in seconds
- whether the WS connection is currently up
- time of last reconnect
- count of unfilled rows in `data_gaps`

Served by `services/market/handler`, registered in `routes/api.go` like the health endpoints.

This endpoint is how you will answer "is my data healthy right now" without opening psql. Keep it simple JSON, no auth needed yet (it is behind the VPS firewall).

Add a staleness check: if the latest 1m candle is more than 3 minutes old while the WS reports connected, log at error level. That combination means something is wrong in a way no other check will catch.

**Note:** the api and collector are separate processes. WS-connection state lives in the collector, so the api cannot read it from memory — decide and record how the status endpoint obtains it (shared table, or the collector exposing its own status port).

---

## 8. Tests

- Unit: kline DTO → `models.Candle` conversion, including decimal precision on prices with 8 decimal places.
- Unit: open vs closed kline filtering (see section 2).
- Unit: gap detection over a synthetic series with known holes at the start, middle, and end.
- Unit: backoff schedule produces increasing delays capped at 60s.
- Integration: upsert the same candle batch twice, assert row count is unchanged and values match.
- Integration: insert a series with a deliberate hole, run gapcheck, assert a `data_gaps` row is created with the correct boundaries.

All tests use fixtures in `testdata/`. No test may hit the network.

---

## Definition of Done

- [ ] `go build ./... && go vet ./... && go test ./...` passes
- [ ] `docker compose up` starts collector and api; collector connects and begins storing 1m candles within 30 seconds
- [ ] Backfill of at least 30 days of 1m candles completes and the row count matches the expected interval count exactly
- [ ] Killing the collector mid-backfill and restarting resumes without duplicate rows
- [ ] Manually deleting 10 rows from the middle of the series, then triggering gapcheck, results in a `data_gaps` row that is subsequently filled and marked `filled_at`
- [ ] Simulated WS disconnect (kill the connection) triggers reconnect and backfill; no gap remains afterwards
- [ ] `/internal/market/status` returns accurate values for all configured timeframes
- [ ] No open (`k.x == false`) candle exists anywhere in the `candles` table
- [ ] No code touches any order, trade, account, or withdrawal endpoint

---

## Out of scope for this phase

- Indicators of any kind
- Strategy or signal logic
- Notifications
- Futures-specific endpoints, funding rate, open interest
- Redis, message queues, any additional infrastructure
- Aggregating lower timeframes into higher ones locally — fetch each timeframe from Binance directly for now

---

## How to start

Summarise your implementation plan as a short numbered list and wait for approval before writing code. Commit in small conventional-commit increments. Flag any new dependency with a justification before adding it.
