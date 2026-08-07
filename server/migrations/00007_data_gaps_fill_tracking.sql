-- +goose Up
--
-- Gap detection runs on a ticker, so it re-finds the same hole every pass
-- until it is filled. Without a key the table would grow a duplicate row per
-- scan, and "how many gaps do I have" would become meaningless.
--
-- fill_attempts supports the rule that a gap is retried a bounded number of
-- times. Some ranges genuinely do not exist: Binance has had real outages, and
-- a thin symbol can have a minute with no trades at all. Retrying those for
-- ever would hide the ones that are actually recoverable.

-- +goose StatementBegin
ALTER TABLE data_gaps
    ADD COLUMN IF NOT EXISTS fill_attempts integer NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE data_gaps
    DROP CONSTRAINT IF EXISTS data_gaps_fill_attempts_check;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE data_gaps
    ADD CONSTRAINT data_gaps_fill_attempts_check CHECK (fill_attempts >= 0);
-- +goose StatementEnd

-- One row per detected range, so a repeated scan updates rather than inserts.
-- +goose StatementBegin
ALTER TABLE data_gaps
    DROP CONSTRAINT IF EXISTS data_gaps_unique_range;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE data_gaps
    ADD CONSTRAINT data_gaps_unique_range
    UNIQUE (symbol, market_type, timeframe, gap_start, gap_end);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE data_gaps
    DROP CONSTRAINT IF EXISTS data_gaps_unique_range;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE data_gaps
    DROP CONSTRAINT IF EXISTS data_gaps_fill_attempts_check;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE data_gaps
    DROP COLUMN IF EXISTS fill_attempts;
-- +goose StatementEnd
