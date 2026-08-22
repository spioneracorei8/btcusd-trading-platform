-- +goose Up
--
-- When a failed delivery may be tried again.
--
-- Retry backoff has to survive a restart or it is not backoff. Held only in
-- the worker's memory, a process that restarts forgets that a row was failing
-- and retries it immediately — so a Firebase outage plus a crash loop becomes
-- a tight retry loop against a service that is already struggling, and the
-- five-attempt budget is spent in seconds instead of minutes.
--
-- Defaulting to now() means a newly queued row is due immediately, which is
-- the behaviour that existed before this column and the one an alert wants.

-- +goose StatementBegin
ALTER TABLE notifications
    ADD COLUMN IF NOT EXISTS next_attempt_at timestamptz NOT NULL DEFAULT now();
-- +goose StatementEnd

-- +goose StatementBegin
COMMENT ON COLUMN notifications.next_attempt_at IS
    'Not before this instant. Set into the future by a failed attempt so the '
    'backoff outlives the process that decided it.';
-- +goose StatementEnd

-- The queue is now read by due time rather than by age, so the index that
-- serves it has to lead with the same column.
-- +goose StatementBegin
DROP INDEX IF EXISTS notifications_pending_idx;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS notifications_due_idx
    ON notifications (next_attempt_at, created_at, id)
    WHERE status = 'pending';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS notifications_due_idx;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS notifications_pending_idx
    ON notifications (created_at)
    WHERE status = 'pending';
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE notifications DROP COLUMN IF EXISTS next_attempt_at;
-- +goose StatementEnd
