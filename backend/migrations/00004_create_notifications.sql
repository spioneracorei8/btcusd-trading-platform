-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS notifications (
    id         bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    signal_id  uuid        NOT NULL REFERENCES signals (id) ON DELETE CASCADE,
    channel    text        NOT NULL,
    status     text        NOT NULL DEFAULT 'pending',
    attempts   integer     NOT NULL DEFAULT 0,
    last_error text        NOT NULL DEFAULT '',
    sent_at    timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT notifications_status_check CHECK (status IN ('pending', 'sent', 'failed')),
    CONSTRAINT notifications_attempts_check CHECK (attempts >= 0)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS notifications_pending_idx
    ON notifications (created_at)
    WHERE status = 'pending';
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS notifications_signal_id_idx
    ON notifications (signal_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS notifications;
-- +goose StatementEnd
