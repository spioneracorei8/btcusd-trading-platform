package notify

import (
	"context"

	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
)

// NotifyUsecase holds the rules that apply to alerting the owner.
type NotifyUsecase interface {
	// QueueSignal offers a recorded signal for delivery.
	//
	// It returns queued=false, and no error, in the two cases that are not
	// failures: the mode is silent, and the signal is already queued.
	//
	// It never delivers. Delivery happens from the queue, on its own
	// schedule, so a Firebase outage delays an alert instead of costing a
	// signal — and so the collector's candle loop is never waiting on a
	// network call to a third party.
	QueueSignal(ctx context.Context, signal models.Signal) (models.Notification, bool, error)

	// Delivers reports whether this usecase sends anything, so a caller can
	// say so at start-up rather than leaving the owner to infer it from an
	// absence of alerts.
	Delivers() bool
}
