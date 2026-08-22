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

	// DeliverDue attempts every notification that is due now and reports what
	// happened to them.
	//
	// It returns an error only for a failure that stops the pass — reading
	// the queue. One message that could not be delivered is an outcome
	// recorded against its own row, not a reason to abandon the ones behind
	// it.
	DeliverDue(ctx context.Context) (DeliveryReport, error)

	// Run sweeps the queue until ctx is cancelled. It returns nil on
	// cancellation, which is the ordinary way it ends.
	Run(ctx context.Context) error
}

// DeliveryReport is what one pass over the queue did.
type DeliveryReport struct {
	// Attempted counts the rows that were due and tried.
	Attempted int

	// Sent counts the deliveries that worked.
	Sent int

	// Retrying counts the failures that will be tried again later.
	Retrying int

	// GaveUp counts the rows marked failed: either out of attempts, or
	// rejected in a way that retrying cannot fix.
	GaveUp int
}

// Quiet reports whether the pass did nothing, so a caller can stay silent
// rather than logging an empty sweep every few seconds forever.
func (r DeliveryReport) Quiet() bool { return r.Attempted == 0 }
