-- name: RegisterDevice :one
-- Record the phone this deployment delivers to, replacing whatever was there.
--
-- Upsert on the singleton row rather than insert-if-absent: a push
-- subscription expires, and is replaced outright by a reinstall or a browser
-- clearing site data. The app re-subscribes on every launch and posts the
-- result. A statement that refused the second registration would leave the
-- deployment holding a subscription the push service has already retired,
-- which fails permanently on the next signal and looks like a broken strategy
-- rather than a stale registration.
--
-- registered_at is kept when the endpoint is unchanged and reset when it is
-- not. The endpoint is the identity: the keys are rotated with it, so two
-- registrations sharing an endpoint are the same phone still there.
INSERT INTO devices (id, endpoint, p256dh, auth, platform, label)
VALUES (1, sqlc.arg(endpoint), sqlc.arg(p256dh), sqlc.arg(auth),
        sqlc.arg(platform), sqlc.arg(label))
ON CONFLICT (id) DO UPDATE
SET endpoint = EXCLUDED.endpoint,
    p256dh   = EXCLUDED.p256dh,
    auth     = EXCLUDED.auth,
    platform = EXCLUDED.platform,
    label    = EXCLUDED.label,
    registered_at = CASE
        WHEN devices.endpoint = EXCLUDED.endpoint THEN devices.registered_at
        ELSE now()
    END,
    refreshed_at = now()
RETURNING *;

-- name: FetchDevice :one
-- The registered device, or no row when the phone has never registered.
SELECT * FROM devices WHERE id = 1;

-- name: DeleteDevice :execrows
-- Forget the registered device, so notify mode stops claiming it can deliver.
DELETE FROM devices WHERE id = 1;
