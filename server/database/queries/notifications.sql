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

-- name: FetchDueNotifications :many
-- The delivery queue, oldest first, so a backlog drains in the order the
-- signals happened rather than newest-first.
--
-- A row is due when nothing has scheduled it into the future. A newly queued
-- one defaults to now() and is therefore due at once; a failed one carries its
-- backoff in the column, so the wait survives the process that decided it.
SELECT * FROM notifications
WHERE status = 'pending'
  AND next_attempt_at <= sqlc.arg(as_of)
ORDER BY created_at, id
LIMIT sqlc.arg(row_limit);

-- name: MarkNotificationSent :one
-- Delivered. attempts counts the one that worked, so a row that took four
-- tries says four rather than three.
UPDATE notifications
SET status = 'sent',
    attempts = attempts + 1,
    last_error = '',
    sent_at = sqlc.arg(sent_at)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: RescheduleNotification :one
-- Failed, and worth trying again. The row stays pending and carries both what
-- went wrong and when it may be retried.
UPDATE notifications
SET attempts = attempts + 1,
    last_error = sqlc.arg(last_error),
    next_attempt_at = sqlc.arg(next_attempt_at)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: FailNotification :one
-- Given up on. last_error is the reason, and it is the last thing written
-- about this row: nothing retries a failed notification.
UPDATE notifications
SET status = 'failed',
    attempts = attempts + 1,
    last_error = sqlc.arg(last_error)
WHERE id = sqlc.arg(id)
RETURNING *;
