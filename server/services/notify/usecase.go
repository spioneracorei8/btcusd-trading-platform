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

// DeviceUsecase holds the rules that apply to registering a phone.
//
// Separate from NotifyUsecase because the API process registers devices and
// does not deliver, and the collector delivers and does not register. Wiring
// one interface into both would give each a capability it has no use for.
type DeviceUsecase interface {
	// RegisterDevice records the phone to deliver to, replacing whatever was
	// there.
	//
	// Re-registering the same token is a success, not a conflict: the app
	// does it on every FCM refresh, and that is the mechanism that keeps a
	// rotated token from silently ending delivery.
	RegisterDevice(ctx context.Context, d models.Device) (models.Device, error)

	// FetchDevice returns the registered device, or constants.ErrNotFound.
	//
	// Not-found is an ordinary state — every deployment is in it until the
	// app is first opened — and callers report it rather than failing on it.
	FetchDevice(ctx context.Context) (models.Device, error)

	// ForgetDevice removes the registration, reporting whether there was one.
	ForgetDevice(ctx context.Context) (bool, error)
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

	// Waiting counts rows that were due and were not touched, because no
	// device is registered to deliver them to.
	//
	// They are not attempts and must not be counted as any: a row here has
	// spent nothing from its retry budget and is still due, so it delivers
	// the moment a phone registers. Counting them as failures would burn five
	// attempts over eight minutes on every signal produced before the app was
	// first installed — and the recorded reason would read like a network
	// problem.
	Waiting int
}

// Quiet reports whether the pass did nothing, so a caller can stay silent
// rather than logging an empty sweep every few seconds forever.
func (r DeliveryReport) Quiet() bool { return r.Attempted == 0 && r.Waiting == 0 }
