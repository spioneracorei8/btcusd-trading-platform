-- name: InsertGap :one
-- Records a detected hole in the candle series so backfill can chase it and
-- so a backtest can refuse to trust the period.
--
-- Detection runs on a ticker and re-finds an unfilled gap on every pass, so
-- this is an upsert: a repeated scan must not grow a duplicate row. The
-- existing row is returned untouched, preserving detected_at and the attempt
-- count.
INSERT INTO data_gaps (
    symbol, market_type, timeframe, gap_start, gap_end, note
) VALUES (
    sqlc.arg(symbol), sqlc.arg(market_type), sqlc.arg(timeframe),
    sqlc.arg(gap_start), sqlc.arg(gap_end), sqlc.arg(note)
)
ON CONFLICT (symbol, market_type, timeframe, gap_start, gap_end) DO UPDATE SET
    symbol = EXCLUDED.symbol
RETURNING *;

-- name: MarkGapFilled :exec
-- Called once a range has been backfilled successfully.
UPDATE data_gaps SET
    filled_at = now()
WHERE id = sqlc.arg(id);

-- name: RecordGapFillAttempt :one
-- Counts one failed attempt and records why. Returns the updated row so the
-- caller can see whether the retry budget is spent.
UPDATE data_gaps SET
    fill_attempts = fill_attempts + 1,
    note          = sqlc.arg(note)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: ListUnfilledGaps :many
-- Gaps still awaiting a successful backfill, oldest first, excluding those
-- whose retry budget is spent.
SELECT * FROM data_gaps
WHERE symbol = sqlc.arg(symbol)
  AND market_type = sqlc.arg(market_type)
  AND timeframe = sqlc.arg(timeframe)
  AND filled_at IS NULL
  AND fill_attempts < sqlc.arg(max_attempts)::int
ORDER BY gap_start;

-- name: CountUnfilledGaps :one
-- Every unfilled gap for a timeframe, including those that have exhausted
-- their retries: the status endpoint reports what is missing, not what is
-- still being chased.
SELECT count(*) FROM data_gaps
WHERE symbol = sqlc.arg(symbol)
  AND market_type = sqlc.arg(market_type)
  AND timeframe = sqlc.arg(timeframe)
  AND filled_at IS NULL;
