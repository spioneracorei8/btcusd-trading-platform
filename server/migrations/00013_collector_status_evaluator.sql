-- +goose Up
--
-- What the live signal evaluator is doing, published where the api can read it.
--
-- # Why this has to be in the database
--
-- The collector evaluates strategies and the api serves the app; they are
-- separate processes in separate containers and share nothing but this
-- database. Readiness lives in the collector's memory, so without a column it
-- is unobservable from outside — and the audit of phase 07 found exactly that:
-- nothing anywhere could answer whether the signal pipeline had stopped.
--
-- Silence is genuinely ambiguous here. A strategy at a tenth of a signal a day
-- is quiet for weeks by design, and "warming up", "refusing to evaluate" and
-- "found no setup" look identical from outside. These columns are what
-- separates them.

-- +goose StatementBegin
ALTER TABLE collector_status
    ADD COLUMN IF NOT EXISTS strategy_name      text    NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS strategy_timeframe text    NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS evaluator_ready    boolean NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS evaluator_reason   text    NOT NULL DEFAULT '';
-- +goose StatementEnd

-- +goose StatementBegin
COMMENT ON COLUMN collector_status.evaluator_reason IS
    'Why the evaluator is not deciding, when it is not. Empty when it is, and '
    'when no strategy is configured at all — strategy_name distinguishes those.';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE collector_status
    DROP COLUMN IF EXISTS strategy_name,
    DROP COLUMN IF EXISTS strategy_timeframe,
    DROP COLUMN IF EXISTS evaluator_ready,
    DROP COLUMN IF EXISTS evaluator_reason;
-- +goose StatementEnd
