// Package notify declares the notification service contracts.
//
// A notification is a convenience; the signal is the artefact. Delivery is
// attempted from a queue rather than inline with signal generation, so a
// Firebase outage costs an alert and never a signal.
package notify

import (
	"context"
	"time"

	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
)

// NotifyRepository stores the delivery queue.
type NotifyRepository interface {
	// InsertNotification queues one signal for delivery. Only SignalId and
	// Channel are read; the rest of the row is the table's business.
	//
	// Queuing is idempotent. A signal already queued for the same channel
	// returns queued=false and no error, because offering it again — after a
	// retry, or after a restart re-walked a bar — is the caller being careful
	// rather than the caller being wrong.
	InsertNotification(ctx context.Context, n models.Notification) (models.Notification, bool, error)

	// FetchDueNotifications returns queued deliveries that may be attempted
	// now, oldest first, so a backlog drains in the order the signals
	// happened. A row scheduled into the future by a failed attempt is not
	// due and is not returned.
	FetchDueNotifications(ctx context.Context, asOf time.Time, limit int32) ([]models.Notification, error)

	// MarkNotificationSent records a delivery that worked.
	MarkNotificationSent(ctx context.Context, id int64, sentAt time.Time) (models.Notification, error)

	// RescheduleNotification records a failed attempt that is worth repeating,
	// leaving the row pending and not due until nextAttemptAt.
	RescheduleNotification(ctx context.Context, id int64, lastError string, nextAttemptAt time.Time) (models.Notification, error)

	// FailNotification gives up on a delivery. Nothing retries a failed row,
	// so lastError is the last thing said about it and has to be enough to
	// explain the silence weeks later.
	FailNotification(ctx context.Context, id int64, lastError string) (models.Notification, error)
}
