package usecase

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/notify"
)

// notifyUsecase queues recorded signals for delivery.
type notifyUsecase struct {
	repo    notify.NotifyRepository
	mode    constants.SignalMode
	channel constants.NotificationChannel
}

// NewNotifyUsecaseImpl builds the queueing rules for a signal mode.
//
// The mode lives here rather than at the call site so that silence is
// structural: in silent mode there is no path from a signal to a delivery,
// and nothing further down has to remember to check.
func NewNotifyUsecaseImpl(
	repo notify.NotifyRepository, mode constants.SignalMode,
) (notify.NotifyUsecase, error) {
	if repo == nil {
		return nil, fmt.Errorf("notify: no repository")
	}
	if !mode.Valid() {
		return nil, fmt.Errorf("notify: %q is not a signal mode", mode)
	}

	return &notifyUsecase{
		repo: repo,
		mode: mode,
		// One channel today. It is stored rather than assumed at the query so
		// that adding a second is a change here and not a search for every
		// place "fcm" was written down.
		channel: constants.NotificationChannelFCM,
	}, nil
}

// Delivers reports whether this usecase sends anything.
func (u *notifyUsecase) Delivers() bool { return u.mode.Delivers() }

// QueueSignal offers a recorded signal for delivery.
//
// # Why this never sends
//
// It writes a row and returns. Delivery is a separate worker reading that
// row, for two reasons that both matter: a Firebase outage must cost an alert
// and never a signal, and the collector's candle loop must never be parked on
// a network call to a third party while the next bar closes.
func (u *notifyUsecase) QueueSignal(
	ctx context.Context, signal models.Signal,
) (models.Notification, bool, error) {
	if !u.mode.Delivers() {
		return models.Notification{}, false, nil
	}
	if signal.Id == uuid.Nil {
		// A signal that was never persisted has nothing for the queue to point
		// at, and a row with a nil signal_id would fail the foreign key at the
		// far end of a delivery worker rather than here.
		return models.Notification{}, false, fmt.Errorf("notify: the signal has no id")
	}

	queued, ok, err := u.repo.InsertNotification(ctx, models.Notification{
		SignalId: signal.Id,
		Channel:  u.channel,
	})
	if err != nil {
		return models.Notification{}, false, fmt.Errorf("notify: %w", err)
	}
	return queued, ok, nil
}
