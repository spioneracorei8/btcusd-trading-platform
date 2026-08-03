-- +goose Up
--
-- Add market_type to the signal de-duplication key.
--
-- 00003 created the constraint as phase-01.md specifies:
--   UNIQUE (strategy_name, strategy_version, symbol, timeframe, signal_time)
--
-- That treats BTCUSDT spot and BTCUSDT futures as the same instrument, so a
-- futures signal is rejected as a duplicate of the spot signal for the same
-- bar. CLAUDE.md section 3.5 requires market type to be a real dimension from
-- day one, and the candles primary key already includes it; signals must
-- match, otherwise enabling futures silently drops half the signals.
--
-- The key still guarantees what the spec wanted — one strategy version emits
-- at most one signal per bar — it just scopes that per market.

-- +goose StatementBegin
ALTER TABLE signals
    DROP CONSTRAINT IF EXISTS signals_unique_per_bar;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE signals
    ADD CONSTRAINT signals_unique_per_bar
    UNIQUE (strategy_name, strategy_version, symbol, market_type, timeframe, signal_time);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE signals
    DROP CONSTRAINT IF EXISTS signals_unique_per_bar;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE signals
    ADD CONSTRAINT signals_unique_per_bar
    UNIQUE (strategy_name, strategy_version, symbol, timeframe, signal_time);
-- +goose StatementEnd
