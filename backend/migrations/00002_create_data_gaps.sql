-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS data_gaps (
    id          bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    symbol      text        NOT NULL,
    market_type text        NOT NULL,
    timeframe   text        NOT NULL,
    -- gap_start is the open time of the first missing candle, gap_end the
    -- open time of the first candle that is present again.
    gap_start   timestamptz NOT NULL,
    gap_end     timestamptz NOT NULL,
    detected_at timestamptz NOT NULL DEFAULT now(),
    -- filled_at stays NULL until the range has been backfilled successfully;
    -- a backtest must treat unfilled ranges as untrustworthy.
    filled_at   timestamptz,
    note        text        NOT NULL DEFAULT '',

    CONSTRAINT data_gaps_market_type_check CHECK (market_type IN ('spot', 'futures')),
    CONSTRAINT data_gaps_range_check CHECK (gap_end > gap_start)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS data_gaps_open_idx
    ON data_gaps (symbol, market_type, timeframe, gap_start)
    WHERE filled_at IS NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS data_gaps;
-- +goose StatementEnd
