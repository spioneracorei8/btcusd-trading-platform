-- name: UpsertCandle :exec
-- Idempotent write of one closed candle. Re-delivering the same bar (after a
-- reconnect or a REST backfill) updates it in place instead of duplicating it.
INSERT INTO candles (
    symbol, market_type, timeframe, open_time, close_time,
    open, high, low, close, volume, quote_volume, trade_count, is_closed
) VALUES (
    sqlc.arg(symbol), sqlc.arg(market_type), sqlc.arg(timeframe),
    sqlc.arg(open_time), sqlc.arg(close_time),
    sqlc.arg(open), sqlc.arg(high), sqlc.arg(low), sqlc.arg(close),
    sqlc.arg(volume), sqlc.arg(quote_volume), sqlc.arg(trade_count),
    sqlc.arg(is_closed)
)
ON CONFLICT (symbol, market_type, timeframe, open_time) DO UPDATE SET
    close_time   = EXCLUDED.close_time,
    open         = EXCLUDED.open,
    high         = EXCLUDED.high,
    low          = EXCLUDED.low,
    close        = EXCLUDED.close,
    volume       = EXCLUDED.volume,
    quote_volume = EXCLUDED.quote_volume,
    trade_count  = EXCLUDED.trade_count,
    is_closed    = EXCLUDED.is_closed;

-- name: GetCandles :many
-- Closed candles in [from_time, to_time], oldest first. The bounds are
-- inclusive on open_time so a caller asking for a window gets exactly the
-- bars whose open falls inside it.
SELECT * FROM candles
WHERE symbol = sqlc.arg(symbol)
  AND market_type = sqlc.arg(market_type)
  AND timeframe = sqlc.arg(timeframe)
  AND open_time >= sqlc.arg(from_time)
  AND open_time <= sqlc.arg(to_time)
ORDER BY open_time;

-- name: GetLatestCandle :one
-- Most recent stored candle, used to work out where a backfill must resume.
SELECT * FROM candles
WHERE symbol = sqlc.arg(symbol)
  AND market_type = sqlc.arg(market_type)
  AND timeframe = sqlc.arg(timeframe)
ORDER BY open_time DESC
LIMIT 1;

-- name: CountCandles :one
-- Row count for a symbol/market/timeframe; used by tests to prove that
-- repeating an upsert does not create a second row.
SELECT count(*) FROM candles
WHERE symbol = sqlc.arg(symbol)
  AND market_type = sqlc.arg(market_type)
  AND timeframe = sqlc.arg(timeframe);

-- name: BatchUpsertCandle :batchexec
-- The batched form of UpsertCandle, used by backfill.
--
-- pgx sends the whole batch in one round trip, so a three year backfill costs
-- thousands of round trips rather than millions. The conflict clause is
-- identical to the single-row version: re-running a backfill over data that is
-- already stored must change nothing.
INSERT INTO candles (
    symbol, market_type, timeframe, open_time, close_time,
    open, high, low, close, volume, quote_volume, trade_count, is_closed
) VALUES (
    sqlc.arg(symbol), sqlc.arg(market_type), sqlc.arg(timeframe),
    sqlc.arg(open_time), sqlc.arg(close_time),
    sqlc.arg(open), sqlc.arg(high), sqlc.arg(low), sqlc.arg(close),
    sqlc.arg(volume), sqlc.arg(quote_volume), sqlc.arg(trade_count),
    sqlc.arg(is_closed)
)
ON CONFLICT (symbol, market_type, timeframe, open_time) DO UPDATE SET
    close_time   = EXCLUDED.close_time,
    open         = EXCLUDED.open,
    high         = EXCLUDED.high,
    low          = EXCLUDED.low,
    close        = EXCLUDED.close,
    volume       = EXCLUDED.volume,
    quote_volume = EXCLUDED.quote_volume,
    trade_count  = EXCLUDED.trade_count,
    is_closed    = EXCLUDED.is_closed;

-- name: FindCandleGaps :many
-- Holes in the expected sequence, found with a window function.
--
-- The diff is computed in the database on purpose: pulling three years of 1m
-- candles into Go to difference them would move hundreds of megabytes to
-- discover a handful of gaps.
--
-- gap_start is the open time of the first MISSING candle and gap_end the open
-- time of the first candle present again, which is the convention
-- models.DataGap documents.
WITH ordered AS (
    SELECT
        open_time,
        LAG(open_time) OVER (ORDER BY open_time) AS previous_open_time
    FROM candles
    WHERE symbol = sqlc.arg(symbol)
      AND market_type = sqlc.arg(market_type)
      AND timeframe = sqlc.arg(timeframe)
)
SELECT
    (previous_open_time + sqlc.arg(interval)::interval)::timestamptz AS gap_start,
    open_time::timestamptz AS gap_end
FROM ordered
WHERE previous_open_time IS NOT NULL
  AND open_time - previous_open_time > sqlc.arg(interval)::interval
ORDER BY gap_start;

-- name: GetEarliestCandle :one
-- Oldest stored candle for a series, which bounds the expected sequence.
SELECT * FROM candles
WHERE symbol = sqlc.arg(symbol)
  AND market_type = sqlc.arg(market_type)
  AND timeframe = sqlc.arg(timeframe)
ORDER BY open_time
LIMIT 1;

-- name: GetCandlesAfter :many
-- One page of a keyset scan, oldest first.
--
-- The backtest engine streams three years of 1m candles — roughly 1.5 million
-- rows — and must not hold them all. Keyset paging on open_time is used rather
-- than OFFSET because OFFSET re-scans everything it skips, so the last page of
-- a long run would cost far more than the first.
--
-- after_time is exclusive, so passing the previous page's last open_time
-- continues exactly where it stopped. to_time stays inclusive, matching
-- GetCandles.
SELECT * FROM candles
WHERE symbol = sqlc.arg(symbol)
  AND market_type = sqlc.arg(market_type)
  AND timeframe = sqlc.arg(timeframe)
  AND open_time > sqlc.arg(after_time)
  AND open_time <= sqlc.arg(to_time)
ORDER BY open_time
LIMIT sqlc.arg(page_size)::int;
