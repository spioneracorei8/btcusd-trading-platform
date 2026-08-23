-- name: ReconcileLiveSignals :many
-- Every live signal in the window, one row each, with what became of it.
--
-- # Why rows rather than an aggregate
--
-- The report needs three views of the same population: everything the live
-- path produced, the subset the engine also emitted, and the surplus it did
-- not. Which signals fall in which is only knowable after the engine has run,
-- so the split cannot be pushed into SQL without running the engine first and
-- passing its timestamps back in.
--
-- One projection and one aggregation in Go is the alternative to three
-- aggregates that must agree. The window is bounded and the strategies here
-- trade at most a few times a day, so the row count is small.
--
-- The grouping key comes back with each row: a parameter change between two
-- signals leaves two incomparable groups, and averaging across it produces a
-- number describing nothing.
SELECT
    s.strategy_name,
    s.strategy_version,
    (COALESCE(s.reason -> 'strategy' -> 'params', '[]'::jsonb))::jsonb AS params,

    s.signal_time,
    s.entry_price,

    o.status,
    o.bars_held,
    (o.divergence_note <> '') AS rested_on_assumption,

    -- Null while a signal is open, and for one whose window had missing data:
    -- what happened there is not knowable, and a number would make a guess
    -- look like a measurement.
    ((o.backtest_would_have ->> 'net_return_pct')::numeric) AS net_return_pct,
    ((o.backtest_would_have ->> 'cost_pct')::numeric)       AS cost_pct
FROM signals s
JOIN signal_outcomes o ON o.signal_id = s.id
WHERE s.symbol = sqlc.arg(symbol)
  AND s.market_type = sqlc.arg(market_type)
  AND s.signal_time >= sqlc.arg(from_time)
  AND s.signal_time <= sqlc.arg(to_time)
ORDER BY s.strategy_name, s.strategy_version, s.signal_time;
