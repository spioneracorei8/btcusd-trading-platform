-- +goose Up
--
-- One notification per signal per channel.
--
-- The unique constraint on signals already stops a second signal being
-- recorded for the same bar, which is what stops the owner being alerted
-- twice. This is the same rule stated where the alert actually lives, and it
-- buys something the signals constraint cannot: queuing becomes idempotent.
--
-- That matters because the signal and its notification are two writes. A
-- process that dies between them leaves a signal with nothing queued, and the
-- recovery — notice it and queue it — is only safe if re-queuing something
-- already queued does nothing. With this constraint it does nothing; without
-- it, recovery risks a second alert for a signal that was already delivered.

-- +goose StatementBegin
ALTER TABLE notifications
    ADD CONSTRAINT notifications_one_per_signal_channel
    UNIQUE (signal_id, channel);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE notifications
    DROP CONSTRAINT IF EXISTS notifications_one_per_signal_channel;
-- +goose StatementEnd
