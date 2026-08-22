-- name: InsertNotification :one
-- Queue one signal for delivery.
--
-- ON CONFLICT DO NOTHING makes queuing idempotent: the same signal offered
-- twice — by a retry, or by a restart that re-walked a bar — leaves the one
-- row that is already there. No row comes back in that case, which the
-- repository reads as "already queued" rather than as a failure.
INSERT INTO notifications (signal_id, channel)
VALUES (sqlc.arg(signal_id), sqlc.arg(channel))
ON CONFLICT (signal_id, channel) DO NOTHING
RETURNING *;

-- name: FetchPendingNotifications :many
-- The delivery queue, oldest first, so a backlog drains in the order the
-- signals happened rather than newest-first.
SELECT * FROM notifications
WHERE status = 'pending'
ORDER BY created_at, id
LIMIT sqlc.arg(row_limit);
