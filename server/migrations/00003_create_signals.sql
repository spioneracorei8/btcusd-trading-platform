-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS signals (
    id               uuid          PRIMARY KEY DEFAULT gen_random_uuid(),
    symbol           text          NOT NULL,
    market_type      text          NOT NULL,
    timeframe        text          NOT NULL,
    -- signal_time is the close time of the candle that produced the signal,
    -- never the insert time: it is what makes live and backtest comparable.
    signal_time      timestamptz   NOT NULL,
    direction        text          NOT NULL,
    strength         numeric(5,2)  NOT NULL,
    -- Advisory levels only. This system never places orders.
    entry_price      numeric(20,8),
    stop_loss        numeric(20,8),
    take_profit      numeric(20,8),
    strategy_name    text          NOT NULL,
    strategy_version text          NOT NULL,
    -- reason holds the indicator values behind the decision, for auditing.
    reason           jsonb         NOT NULL DEFAULT '{}'::jsonb,
    created_at       timestamptz   NOT NULL DEFAULT now(),

    CONSTRAINT signals_market_type_check CHECK (market_type IN ('spot', 'futures')),
    CONSTRAINT signals_direction_check CHECK (direction IN ('long', 'short', 'flat')),
    CONSTRAINT signals_strength_check CHECK (strength >= 0 AND strength <= 100),
    -- One strategy version emits at most one signal per bar, so a restart or
    -- a replay cannot notify the owner twice for the same candle.
    CONSTRAINT signals_unique_per_bar
        UNIQUE (strategy_name, strategy_version, symbol, timeframe, signal_time)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS signals_symbol_timeframe_signal_time_idx
    ON signals (symbol, timeframe, signal_time DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS signals;
-- +goose StatementEnd
