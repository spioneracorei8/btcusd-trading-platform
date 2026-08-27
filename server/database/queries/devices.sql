-- name: RegisterDevice :one
-- Record the phone this deployment delivers to, replacing whatever was there.
--
-- Upsert on the singleton row rather than insert-if-absent: FCM rotates
-- tokens, and the app re-registers on every refresh. A statement that
-- refused the second registration would leave the deployment holding a token
-- Firebase has already retired, which fails permanently on the next signal
-- and looks like a broken strategy rather than a stale token.
--
-- registered_at is kept when the token is unchanged and reset when it is not:
-- a refresh of the same token is the same phone still there, a different
-- token is a new registration.
INSERT INTO devices (id, token, platform, label)
VALUES (1, sqlc.arg(token), sqlc.arg(platform), sqlc.arg(label))
ON CONFLICT (id) DO UPDATE
SET token    = EXCLUDED.token,
    platform = EXCLUDED.platform,
    label    = EXCLUDED.label,
    registered_at = CASE
        WHEN devices.token = EXCLUDED.token THEN devices.registered_at
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
