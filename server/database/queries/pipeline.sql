-- name: PipelineSignalActivity :one
-- When the signal pipeline last produced anything, and how much is waiting.
--
-- One row, because a status endpoint that made five round trips would report
-- five different instants and invite the reader to reconcile them.
SELECT
    (SELECT max(s.signal_time) FROM signals s
      WHERE s.symbol = sqlc.arg(symbol) AND s.market_type = sqlc.arg(market_type)
    )::timestamptz AS last_signal_at,

    (SELECT count(*) FROM signals s
      WHERE s.symbol = sqlc.arg(symbol) AND s.market_type = sqlc.arg(market_type)
    )::bigint AS signals_total,

    (SELECT count(*) FROM signals s
      JOIN signal_outcomes o ON o.signal_id = s.id
      WHERE s.symbol = sqlc.arg(symbol) AND s.market_type = sqlc.arg(market_type)
        AND o.status = 'open'
    )::bigint AS outcomes_open,

    -- The oldest signal still being followed. A follower that has stopped
    -- shows up here as an age that keeps growing, which is the only symptom
    -- it has.
    (SELECT min(s.signal_time) FROM signals s
      JOIN signal_outcomes o ON o.signal_id = s.id
      WHERE s.symbol = sqlc.arg(symbol) AND s.market_type = sqlc.arg(market_type)
        AND o.status = 'open'
    )::timestamptz AS oldest_open_signal_at,

    -- Signals with no outcome row at all. Non-zero for longer than a pass
    -- means the follower is not opening them.
    (SELECT count(*) FROM signals s
      LEFT JOIN signal_outcomes o ON o.signal_id = s.id
      WHERE s.symbol = sqlc.arg(symbol) AND s.market_type = sqlc.arg(market_type)
        AND o.signal_id IS NULL
    )::bigint AS outcomes_missing;

-- name: PipelineDeliveryActivity :one
-- The delivery queue, by state.
--
-- failed is the number that matters: nothing retries a failed row, so a
-- delivery path that is permanently broken shows up here and nowhere else.
SELECT
    count(*) FILTER (WHERE n.status = 'pending')::bigint AS pending,
    count(*) FILTER (WHERE n.status = 'sent')::bigint    AS sent,
    count(*) FILTER (WHERE n.status = 'failed')::bigint  AS failed,
    (max(n.sent_at) FILTER (WHERE n.status = 'sent'))::timestamptz AS last_sent_at,

    -- How many phones can be delivered to. Zero or one by construction — the
    -- devices table holds a single row — and the case worth naming is zero
    -- while the mode is notify: everything configured, nothing delivered.
    --
    -- A scalar subquery rather than a join: devices has no relationship to
    -- this symbol's notifications, and joining an unrelated table would
    -- multiply the counts above by whether a phone happens to be registered.
    (SELECT count(*) FROM devices)::bigint AS devices_registered
FROM notifications n
JOIN signals s ON s.id = n.signal_id
WHERE s.symbol = sqlc.arg(symbol)
  AND s.market_type = sqlc.arg(market_type);
