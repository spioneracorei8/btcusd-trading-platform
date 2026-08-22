-- name: ReconcileLiveGroups :many
-- The live side of the comparison, grouped so only like is compared with like.
--
-- # Why the parameter set is part of the grouping key
--
-- A parameter change between two signals leaves two incomparable groups in
-- one table looking alike. Averaging across it produces a number describing
-- nothing — and it would look exactly like a number describing something.
-- The resolved set is recorded on every signal for this reason, and it is
-- grouped on here rather than assumed constant.
--
-- # What counts
--
-- Invalidated outcomes are counted and then excluded from every statistic.
-- Their window has missing data, so whether they would have won is not
-- knowable; a win rate that quietly counted guesses would be worse than one
-- with a smaller sample. They are still reported, because a period where many
-- signals were invalidated is itself a finding.
--
-- A win is a positive return after modelled cost, which is the same
-- definition the backtest's win rate uses. Scalping at these timeframes is
-- dominated by cost, so a gross figure would flatter every strategy equally.
SELECT
    s.strategy_name,
    s.strategy_version,
    (COALESCE(s.reason -> 'strategy' -> 'params', '[]'::jsonb))::jsonb AS params,

    count(*)                                            AS signals,
    count(*) FILTER (WHERE o.status = 'open')           AS still_open,
    count(*) FILTER (WHERE o.status = 'invalidated')    AS invalidated,
    count(*) FILTER (WHERE o.status IN ('target', 'stop', 'expired')) AS resolved,

    count(*) FILTER (WHERE o.status = 'target')         AS targets,
    count(*) FILTER (WHERE o.status = 'stop')           AS stops,
    count(*) FILTER (WHERE o.status = 'expired')        AS expired,

    -- Resolutions that rested on an assumption rather than on the data: a bar
    -- reaching both levels, or an entry that gapped past one.
    count(*) FILTER (WHERE o.status IN ('target', 'stop', 'expired')
                       AND o.divergence_note <> '')     AS noted,

    count(*) FILTER (WHERE o.status IN ('target', 'stop', 'expired')
                       AND (o.backtest_would_have ->> 'net_return_pct')::numeric > 0) AS wins,
    count(*) FILTER (WHERE o.status IN ('target', 'stop', 'expired')
                       AND (o.backtest_would_have ->> 'net_return_pct')::numeric <= 0) AS losses,

    (avg((o.backtest_would_have ->> 'net_return_pct')::numeric)
        FILTER (WHERE o.status IN ('target', 'stop', 'expired')
                  AND (o.backtest_would_have ->> 'net_return_pct')::numeric > 0))::numeric  AS average_win_pct,
    (avg((o.backtest_would_have ->> 'net_return_pct')::numeric)
        FILTER (WHERE o.status IN ('target', 'stop', 'expired')
                  AND (o.backtest_would_have ->> 'net_return_pct')::numeric <= 0))::numeric AS average_loss_pct,

    -- The cost actually modelled on these signals, so the report quotes what
    -- was applied rather than what the configuration currently says.
    (avg((o.backtest_would_have ->> 'cost_pct')::numeric)
        FILTER (WHERE o.status IN ('target', 'stop', 'expired')))::numeric        AS average_cost_pct,

    (avg(s.entry_price) FILTER (WHERE s.entry_price IS NOT NULL))::numeric        AS average_entry_price,
    (avg(o.bars_held) FILTER (WHERE o.status IN ('target', 'stop', 'expired')))::numeric AS average_bars_held,

    (min(s.signal_time))::timestamptz AS first_signal,
    (max(s.signal_time))::timestamptz AS last_signal
FROM signals s
JOIN signal_outcomes o ON o.signal_id = s.id
WHERE s.symbol = sqlc.arg(symbol)
  AND s.market_type = sqlc.arg(market_type)
  AND s.signal_time >= sqlc.arg(from_time)
  AND s.signal_time <= sqlc.arg(to_time)
GROUP BY s.strategy_name, s.strategy_version, COALESCE(s.reason -> 'strategy' -> 'params', '[]'::jsonb)
ORDER BY s.strategy_name, s.strategy_version, min(s.signal_time);
