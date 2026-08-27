-- +goose Up
--
-- Where to deliver an alert.
--
-- Until now the device token was FCM_DEVICE_TOKEN in the environment, which
-- meant the value could only be set by pasting it into a file on the VPS.
-- That is not where a token comes from: FCM issues it to the app on the phone,
-- rotates it without asking, and issues a fresh one after a reinstall. A
-- deployment holding the previous one looks configured and delivers nothing.
--
-- So the phone registers itself, through POST /api/v1/device, and this is
-- where that lands. See ADR 0026.

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS devices (
    -- One row, enforced rather than assumed.
    --
    -- This is a single-owner system with one phone, and the delivery queue
    -- says so in its own shape: notifications is unique on
    -- (signal_id, channel), so it can express "this signal was delivered over
    -- FCM" and not "delivered to which of several devices". A second row here
    -- would silently mean the second device never gets anything, or that one
    -- signal needs two queue rows — a design change, not a configuration.
    --
    -- Making it a real constraint means a future second device fails loudly
    -- here, next to this comment, rather than half-working in the queue.
    id          integer     PRIMARY KEY DEFAULT 1 CHECK (id = 1),

    -- The FCM registration token. Long, opaque, and rotated by Firebase.
    token       text        NOT NULL CHECK (length(token) > 0),

    -- android or ios. Recorded rather than used: the sender is the same
    -- either way, and a row that cannot say what it belongs to is harder to
    -- reason about when the alerts stop.
    platform    text        NOT NULL DEFAULT 'android'
                CHECK (platform IN ('android', 'ios')),

    -- Free-form, for a person reading the table. "Pixel 7a" beats a token
    -- prefix when deciding whether a registration is the phone in your hand.
    label       text        NOT NULL DEFAULT '',

    -- When this token first arrived, and when it was last confirmed.
    --
    -- registered_at survives a re-registration of the same token and
    -- refreshed_at does not, so the pair says both "this phone has been
    -- registered since March" and "the app checked in an hour ago". A token
    -- that has not refreshed in weeks is either an app nobody opens or a
    -- registration that is quietly stale.
    registered_at timestamptz NOT NULL DEFAULT now(),
    refreshed_at  timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS devices;
