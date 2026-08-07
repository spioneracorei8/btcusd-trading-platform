-- +goose Up
--
-- Records which phase of its lifecycle the collector is in.
--
-- Without it the status endpoint cannot distinguish "catching up on three
-- years of history" from "ingestion has silently stopped": both show a very
-- old newest candle, and only one of them is fine. The staleness check is
-- only meaningful in the live state, so the state is what decides whether it
-- runs at all.
--
-- never_started is not stored — it is the absence of a row.

-- +goose StatementBegin
ALTER TABLE collector_status
    ADD COLUMN IF NOT EXISTS state text NOT NULL DEFAULT 'starting';
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE collector_status
    ADD COLUMN IF NOT EXISTS state_changed_at timestamptz NOT NULL DEFAULT now();
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE collector_status
    DROP CONSTRAINT IF EXISTS collector_status_state_check;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE collector_status
    ADD CONSTRAINT collector_status_state_check
    CHECK (state IN ('starting', 'backfilling', 'live', 'reconnecting'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE collector_status
    DROP CONSTRAINT IF EXISTS collector_status_state_check;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE collector_status DROP COLUMN IF EXISTS state_changed_at;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE collector_status DROP COLUMN IF EXISTS state;
-- +goose StatementEnd
