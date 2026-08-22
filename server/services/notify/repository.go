// Package notify declares the notification service contracts.
//
// A notification is a convenience; the signal is the artefact. Delivery is
// attempted from a queue rather than inline with signal generation, so a
// Firebase outage costs an alert and never a signal.
package notify

import (
	"context"

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

	// FetchPendingNotifications returns queued deliveries, oldest first, so a
	// backlog drains in the order the signals happened.
	FetchPendingNotifications(ctx context.Context, limit int32) ([]models.Notification, error)
}
