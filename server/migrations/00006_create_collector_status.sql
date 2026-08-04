-- +goose Up
--
-- The collector and the api run in separate containers, so the api cannot
-- read the collector's WebSocket state from memory. The collector writes it
-- here on a heartbeat and the api reads it to answer
-- GET /internal/market/status.
--
-- started_at and updated_at answer two different questions and are written by
-- two different statements:
--
--   started_at  set once, when the process starts. now() - started_at is how
--               long this collector has been up, so a process that has been
--               running for days is distinguishable from one that is crash
--               looping and re-registering every few seconds.
--   updated_at  bumped on every heartbeat. now() - updated_at is how fresh
--               the reading is; if it stops advancing the collector is gone,
--               whatever ws_connected still claims.
--
-- Keyed per (symbol, market_type) so two collectors sharing one database
-- cannot overwrite each other's status.

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS collector_status (
    symbol               text        NOT NULL,
    market_type          text        NOT NULL,

    -- Whether the collector believes its stream is up right now. Only
    -- meaningful alongside a fresh updated_at.
    ws_connected         boolean     NOT NULL DEFAULT false,

    last_connected_at    timestamptz,
    last_disconnected_at timestamptz,

    -- Why the stream last went down, for the history a suspicious backtest
    -- result sends you looking for.
    last_disconnect_note text        NOT NULL DEFAULT '',
    reconnect_count      integer     NOT NULL DEFAULT 0,

    started_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT collector_status_pkey PRIMARY KEY (symbol, market_type),
    CONSTRAINT collector_status_market_type_check CHECK (market_type IN ('spot', 'futures')),
    CONSTRAINT collector_status_reconnect_count_check CHECK (reconnect_count >= 0)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS collector_status;
-- +goose StatementEnd
