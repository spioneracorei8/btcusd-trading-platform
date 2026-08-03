-- +goose Up
-- +goose StatementBegin
CREATE EXTENSION IF NOT EXISTS timescaledb;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS candles (
    symbol       text          NOT NULL,
    market_type  text          NOT NULL,
    timeframe    text          NOT NULL,
    open_time    timestamptz   NOT NULL,
    close_time   timestamptz   NOT NULL,
    open         numeric(20,8) NOT NULL,
    high         numeric(20,8) NOT NULL,
    low          numeric(20,8) NOT NULL,
    close        numeric(20,8) NOT NULL,
    volume       numeric(30,8) NOT NULL,
    quote_volume numeric(30,8) NOT NULL,
    trade_count  integer       NOT NULL,
    is_closed    boolean       NOT NULL DEFAULT true,
    created_at   timestamptz   NOT NULL DEFAULT now(),

    -- open_time is part of the key so an upsert of the same bar is idempotent.
    CONSTRAINT candles_pkey PRIMARY KEY (symbol, market_type, timeframe, open_time),
    CONSTRAINT candles_market_type_check CHECK (market_type IN ('spot', 'futures')),
    -- Unclosed candles are display-only and live in memory; storing one here
    -- would let a flickering bar reach the strategy engine.
    CONSTRAINT candles_is_closed_check CHECK (is_closed),
    CONSTRAINT candles_time_order_check CHECK (close_time > open_time),
    CONSTRAINT candles_high_low_check CHECK (high >= low)
);
-- +goose StatementEnd

-- Turn candles into a TimescaleDB hypertable partitioned on open_time with
-- 7 day chunks. The by_range() form is the current API (TimescaleDB 2.13+);
-- the older positional form is kept as a fallback so the migration works on
-- whatever the timescale/timescaledb:latest-pg16 tag currently points at.
-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'timescaledb') THEN
        RAISE EXCEPTION 'timescaledb extension is required to create the candles hypertable';
    END IF;

    -- Deliberately not restricted to the public schema: TimescaleDB can be
    -- installed elsewhere, and pinning the lookup to public would silently
    -- pick the deprecated branch on such an installation.
    IF EXISTS (SELECT 1 FROM pg_proc WHERE proname = 'by_range') THEN
        PERFORM create_hypertable(
            'candles'::regclass,
            by_range('open_time', INTERVAL '7 days'),
            if_not_exists => TRUE,
            migrate_data  => TRUE
        );
    ELSE
        PERFORM create_hypertable(
            'candles',
            'open_time',
            chunk_time_interval => INTERVAL '7 days',
            if_not_exists       => TRUE,
            migrate_data        => TRUE
        );
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS candles_symbol_timeframe_open_time_idx
    ON candles (symbol, timeframe, open_time DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS candles;
-- +goose StatementEnd
