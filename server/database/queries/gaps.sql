-- name: InsertGap :one
-- Records a detected hole in the candle series so backfill can chase it and
-- so a backtest can refuse to trust the period.
INSERT INTO data_gaps (
    symbol, market_type, timeframe, gap_start, gap_end, note
) VALUES (
    sqlc.arg(symbol), sqlc.arg(market_type), sqlc.arg(timeframe),
    sqlc.arg(gap_start), sqlc.arg(gap_end), sqlc.arg(note)
)
RETURNING *;
