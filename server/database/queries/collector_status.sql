-- name: RegisterCollectorStart :one
-- Called once when the collector process starts.
--
-- This is the only statement that writes started_at, so a heartbeat cannot
-- disguise a crash loop as continuous uptime: a process that keeps restarting
-- moves started_at forward every time, while one that has been up for days
-- leaves it alone.
INSERT INTO collector_status (
    symbol, market_type, ws_connected, state, state_changed_at, started_at, updated_at
) VALUES (
    sqlc.arg(symbol), sqlc.arg(market_type), false, 'starting', now(), now(), now()
)
ON CONFLICT (symbol, market_type) DO UPDATE SET
    ws_connected         = false,
    last_disconnected_at = NULL,
    last_disconnect_note = '',
    reconnect_count      = 0,
    state                = 'starting',
    state_changed_at     = now(),
    started_at           = now(),
    updated_at           = now()
RETURNING *;

-- name: HeartbeatCollector :exec
-- Called on every heartbeat tick. Deliberately leaves started_at untouched.
UPDATE collector_status SET
    ws_connected = sqlc.arg(ws_connected),
    updated_at   = now()
WHERE symbol = sqlc.arg(symbol)
  AND market_type = sqlc.arg(market_type);

-- name: MarkCollectorConnected :exec
-- Called when the stream comes up.
UPDATE collector_status SET
    ws_connected      = true,
    last_connected_at = now(),
    reconnect_count   = reconnect_count + sqlc.arg(reconnect_increment)::int,
    updated_at        = now()
WHERE symbol = sqlc.arg(symbol)
  AND market_type = sqlc.arg(market_type);

-- name: MarkCollectorDisconnected :exec
-- Called when the stream goes down, with the reason kept for later.
UPDATE collector_status SET
    ws_connected         = false,
    last_disconnected_at = now(),
    last_disconnect_note = sqlc.arg(note),
    updated_at           = now()
WHERE symbol = sqlc.arg(symbol)
  AND market_type = sqlc.arg(market_type);

-- name: GetCollectorStatus :one
SELECT * FROM collector_status
WHERE symbol = sqlc.arg(symbol)
  AND market_type = sqlc.arg(market_type);

-- name: SetCollectorState :exec
-- Records a lifecycle transition. state_changed_at only moves when the state
-- actually changes, so the time spent in a state is measurable.
UPDATE collector_status SET
    state            = sqlc.arg(state),
    state_changed_at = CASE WHEN state IS DISTINCT FROM sqlc.arg(state) THEN now() ELSE state_changed_at END,
    updated_at       = now()
WHERE symbol = sqlc.arg(symbol)
  AND market_type = sqlc.arg(market_type);
