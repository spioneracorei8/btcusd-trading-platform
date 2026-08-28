-- +goose Up
--
-- FCM becomes Web Push.
--
-- The device is an iPhone and the app is a PWA (ADR 0028), which cannot use
-- FCM at all. Keeping both transports would leave one of them exercised by
-- nothing, and an untested delivery path is a broken one nobody has noticed —
-- so this replaces rather than adds.
--
-- What changes here is what a registration *is*. An FCM registration is one
-- opaque token; a Web Push registration is three values: the endpoint the push
-- service listens on, and the two keys (RFC 8291) the payload is encrypted
-- against. One column cannot hold that, and there is no conversion between
-- them — a token is issued by Firebase for an app, a subscription is issued by
-- the browser's own push service for an origin.

-- +goose StatementBegin
-- Any existing registration is an FCM token. It cannot be migrated and it
-- cannot be used, so it goes; the phone re-registers the first time the app is
-- opened, which is the mechanism ADR 0026 chose precisely so that this kind of
-- change costs nothing but a launch.
DELETE FROM devices;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE devices
    DROP COLUMN token,

    -- Where the push service listens for this subscription. Long, opaque, and
    -- origin-specific: a subscription made against one origin is meaningless
    -- to another, which is part of why the app and the API share one.
    ADD COLUMN endpoint text NOT NULL CHECK (length(endpoint) > 0),

    -- The subscriber's public key and the auth secret, base64url as the
    -- browser hands them over. The payload is encrypted against these before
    -- it leaves this host, so the push service forwards ciphertext it cannot
    -- read — which is the reason a signal's entry, stop and target may travel
    -- through somebody else's infrastructure at all.
    ADD COLUMN p256dh text NOT NULL CHECK (length(p256dh) > 0),
    ADD COLUMN auth   text NOT NULL CHECK (length(auth) > 0);
-- +goose StatementEnd

-- +goose StatementBegin
-- 'web' is what a home-screen PWA actually is: a browser on an OS, not the OS.
-- Recording it as 'ios' would be a column whose value had quietly stopped
-- meaning what its comment says.
ALTER TABLE devices
    DROP CONSTRAINT IF EXISTS devices_platform_check;

ALTER TABLE devices
    ADD CONSTRAINT devices_platform_check
    CHECK (platform IN ('web', 'android', 'ios'));

ALTER TABLE devices
    ALTER COLUMN platform SET DEFAULT 'web';
-- +goose StatementEnd

-- +goose StatementBegin
-- Pending rows on the retired channel can never be delivered.
--
-- Nothing sends over 'fcm' after this migration, so a row left pending would
-- sit in the queue for a transport that no longer exists — picked up by every
-- pass, failing to match any sender, and never resolving. Marking them failed
-- with the reason is the honest end: the row says what happened rather than
-- waiting forever for something that is not coming.
--
-- Rows already 'sent' are history and are left exactly as they are.
UPDATE notifications
SET status     = 'failed',
    last_error = 'the fcm channel was retired in phase 09b; this was never delivered'
WHERE channel = 'fcm'
  AND status = 'pending';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM devices;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE devices
    DROP COLUMN endpoint,
    DROP COLUMN p256dh,
    DROP COLUMN auth,
    ADD COLUMN token text NOT NULL CHECK (length(token) > 0);
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE devices
    DROP CONSTRAINT IF EXISTS devices_platform_check;

ALTER TABLE devices
    ADD CONSTRAINT devices_platform_check
    CHECK (platform IN ('android', 'ios'));

ALTER TABLE devices
    ALTER COLUMN platform SET DEFAULT 'android';
-- +goose StatementEnd

-- +goose StatementBegin
-- The reason is specific enough to reverse without touching rows that failed
-- for their own reasons.
UPDATE notifications
SET status     = 'pending',
    last_error = ''
WHERE channel = 'fcm'
  AND status = 'failed'
  AND last_error = 'the fcm channel was retired in phase 09b; this was never delivered';
-- +goose StatementEnd
