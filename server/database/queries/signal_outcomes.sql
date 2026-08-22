-- name: EnsureSignalOutcomes :many
-- Open an outcome row for every signal that has none yet.
--
-- Done as a set rather than one at a time so a follower starting against a
-- table of existing signals — a first deploy, or a restart after an outage —
-- picks them up without one round trip each.
--
-- The two halves do different jobs and both are needed. The LEFT JOIN filter
-- is what makes the batch find signals that still need a row: without it the
-- LIMIT would return the oldest signals, which are the ones already followed,
-- and a new signal would never be picked up once the backlog exceeded the
-- batch size — silently, because every pass would still look like it worked.
-- ON CONFLICT DO NOTHING is what makes it safe to run twice: a signal already
-- being followed is left exactly as it is, progress included.
INSERT INTO signal_outcomes (signal_id)
SELECT s.id
FROM signals s
LEFT JOIN signal_outcomes o ON o.signal_id = s.id
WHERE o.signal_id IS NULL
  AND s.symbol = sqlc.arg(symbol)
  AND s.market_type = sqlc.arg(market_type)
ORDER BY s.signal_time
LIMIT sqlc.arg(row_limit)
ON CONFLICT (signal_id) DO NOTHING
RETURNING *;

-- name: FetchOpenSignalOutcomes :many
-- Signals still being followed, oldest first, so a backlog is worked through
-- in the order the signals happened.
--
-- The signals table is joined to order and filter, and nothing is selected
-- from it: the outcome service reads a signal through the signal service's
-- usecase, not by reaching into its rows. That costs one point lookup per
-- open signal per pass, against a batch of fifty once a minute.
SELECT o.*
FROM signal_outcomes o
JOIN signals s ON s.id = o.signal_id
WHERE o.status = 'open'
  AND s.symbol = sqlc.arg(symbol)
  AND s.market_type = sqlc.arg(market_type)
ORDER BY s.signal_time, o.signal_id
LIMIT sqlc.arg(row_limit);

-- name: UpdateSignalOutcome :one
-- Record progress or a resolution. The same statement serves both: an
-- outcome still open carries its running excursions and bar count, and a
-- resolved one adds when and where it ended.
UPDATE signal_outcomes
SET status = sqlc.arg(status),
    resolved_at = sqlc.narg(resolved_at),
    resolved_price = sqlc.narg(resolved_price),
    mae = sqlc.narg(mae),
    mfe = sqlc.narg(mfe),
    bars_held = sqlc.arg(bars_held),
    backtest_would_have = sqlc.narg(backtest_would_have),
    divergence_note = sqlc.arg(divergence_note),
    updated_at = now()
WHERE signal_id = sqlc.arg(signal_id)
RETURNING *;

-- name: FetchSignalOutcome :one
SELECT * FROM signal_outcomes WHERE signal_id = sqlc.arg(signal_id);
