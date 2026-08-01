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
