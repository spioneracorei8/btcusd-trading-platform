package usecase

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/notify"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/signal"
)

// Config is what the delivery path needs.
type Config struct {
	// Mode decides whether anything is queued or delivered at all.
	Mode constants.SignalMode

	// Sender delivers a message. Required in notify mode, unused in silent.
	Sender notify.Sender

	// Signals turns a queued id back into the signal it points at.
	Signals signal.SignalUsecase

	// Devices is where to deliver. Read at send time rather than held as a
	// string, because FCM rotates the token and the app re-registers: a value
	// captured at start-up would be the previous one from the moment Firebase
	// decided otherwise, and the collector would go on looking configured
	// while delivering nothing. See ADR 0026.
	Devices notify.DeviceUsecase

	// Interval is how often Run sweeps the queue.
	Interval time.Duration

	// Now is the clock. A test supplies its own so backoff can be observed
	// without waiting for it.
	Now func() time.Time
}

// notifyUsecase queues recorded signals and delivers what is due.
type notifyUsecase struct {
	repo    notify.NotifyRepository
	log     *slog.Logger
	cfg     Config
	channel constants.NotificationChannel
}

// NewNotifyUsecaseImpl builds the queueing and delivery rules for a mode.
//
// The mode lives here rather than at the call site so that silence is
// structural: in silent mode there is no path from a signal to a delivery,
// and nothing further down has to remember to check.
func NewNotifyUsecaseImpl(
	repo notify.NotifyRepository, log *slog.Logger, cfg Config,
) (notify.NotifyUsecase, error) {
	if repo == nil {
		return nil, fmt.Errorf("notify: no repository")
	}
	if log == nil {
		return nil, fmt.Errorf("notify: no logger")
	}
	if !cfg.Mode.Valid() {
		return nil, fmt.Errorf("notify: %q is not a signal mode", cfg.Mode)
	}

	// A mode that claims to deliver and cannot is worse than one that says it
	// will not: it looks like it is working, and the missing alert is noticed
	// only when it matters.
	if cfg.Mode.Delivers() {
		if cfg.Sender == nil {
			return nil, fmt.Errorf("notify: %s mode with nothing to send through", cfg.Mode)
		}
		if cfg.Signals == nil {
			return nil, fmt.Errorf("notify: %s mode with no way to read a queued signal", cfg.Mode)
		}
		// Deliberately not "and a device is registered". The phone registers
		// itself after the app is installed, so requiring one at start-up
		// makes the first deploy impossible: nothing can register against a
		// process that refuses to run. What is required is the ability to
		// look one up.
		if cfg.Devices == nil {
			return nil, fmt.Errorf("notify: %s mode with no way to look up a device", cfg.Mode)
		}
	}

	if cfg.Interval <= 0 {
		cfg.Interval = constants.DefaultNotifyInterval
	}
	if cfg.Now == nil {
		cfg.Now = func() time.Time { return time.Now().UTC() }
	}

	channel := constants.NotificationChannelFCM
	if cfg.Sender != nil {
		// The queue row records which sender owns it, so it comes from the
		// sender rather than from an assumption made here.
		channel = cfg.Sender.Channel()
	}

	return &notifyUsecase{repo: repo, log: log, cfg: cfg, channel: channel}, nil
}

// Delivers reports whether this usecase sends anything.
func (u *notifyUsecase) Delivers() bool { return u.cfg.Mode.Delivers() }

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
	if !u.cfg.Mode.Delivers() {
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

// Run sweeps the queue on a ticker until ctx is cancelled.
//
// In silent mode it returns immediately: there is nothing to deliver, and a
// goroutine waking every ten seconds to confirm that would be pure noise in
// the logs of a system whose default is to say nothing.
func (u *notifyUsecase) Run(ctx context.Context) error {
	if !u.cfg.Mode.Delivers() {
		u.log.InfoContext(ctx, "no delivery worker; signals are recorded and not sent",
			"mode", u.cfg.Mode.String())
		return nil
	}

	u.log.InfoContext(ctx, "delivery worker started",
		"channel", u.channel.String(),
		"interval", u.cfg.Interval.String(),
		"max_attempts", constants.NotificationMaxAttempts)

	ticker := time.NewTicker(u.cfg.Interval)
	defer ticker.Stop()

	for {
		// A pass first, so a queue that built up while the process was down
		// drains at start-up rather than after one interval of silence.
		report, err := u.DeliverDue(ctx)
		switch {
		case errors.Is(err, context.Canceled):
			return nil
		case err != nil:
			// The queue could not be read. That is worth saying and not worth
			// stopping for: the database may be briefly unavailable, and
			// giving up would mean every later signal goes undelivered.
			u.log.ErrorContext(ctx, "could not read the delivery queue", "error", err)
		case !report.Quiet():
			u.log.InfoContext(ctx, "delivery pass",
				"attempted", report.Attempted, "sent", report.Sent,
				"retrying", report.Retrying, "gave_up", report.GaveUp,
				"waiting_for_a_device", report.Waiting)
		}

		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// DeliverDue attempts every notification that is due now.
//
// One message that cannot be delivered is an outcome recorded against its own
// row, not a reason to abandon the ones behind it — a single bad device token
// must not stop the queue.
//
// # Delivery is at least once, deliberately
//
// Sending and recording the send are two steps, and nothing can make them one:
// a process that dies between them leaves a row that will be delivered again.
// There is no lease on a due row either, so two collectors running at once
// would both send. Both are accepted rather than engineered away — a duplicate
// alert is a nuisance and a missed one is a missed signal, and this is a
// single-owner system running a single collector. If a second one is ever run,
// this is the thing to fix first.
func (u *notifyUsecase) DeliverDue(ctx context.Context) (notify.DeliveryReport, error) {
	var report notify.DeliveryReport
	if !u.cfg.Mode.Delivers() {
		return report, nil
	}

	due, err := u.repo.FetchDueNotifications(ctx, u.cfg.Now(), constants.NotifyBatchSize)
	if err != nil {
		return report, fmt.Errorf("notify: %w", err)
	}

	// Whether a phone is registered is a fact about the deployment, not about
	// any one row, so it is asked once rather than per message. Without this
	// a pass with fifty due rows and no device would make fifty identical
	// lookups to reach the same answer.
	if _, err := u.cfg.Devices.FetchDevice(ctx); errors.Is(err, constants.ErrNotFound) {
		report.Waiting = len(due)
		return report, nil
	}

	for _, queued := range due {
		if ctx.Err() != nil {
			return report, ctx.Err()
		}

		result := u.deliver(ctx, queued)
		if result == outcomeNoDevice {
			// The registration went away mid-pass. Everything behind this row
			// is in the same position, and none of it has been touched.
			report.Waiting += len(due) - report.Attempted
			return report, nil
		}

		report.Attempted++
		switch result {
		case outcomeSent:
			report.Sent++
		case outcomeRetrying:
			report.Retrying++
		case outcomeGaveUp:
			report.GaveUp++
		}
	}
	return report, nil
}

// outcome is what happened to one notification.
type outcome int

const (
	outcomeSent outcome = iota
	outcomeRetrying
	outcomeGaveUp
	outcomeUnrecorded

	// outcomeNoDevice is a row left exactly as it was found: still pending,
	// still due, no attempt spent. It is the only outcome that does not
	// write to the queue.
	outcomeNoDevice
)

// deliver attempts one notification and records what happened to it.
func (u *notifyUsecase) deliver(ctx context.Context, queued models.Notification) outcome {
	signalRow, err := u.cfg.Signals.FetchSignalById(ctx, queued.SignalId)
	if err != nil {
		// The row points at a signal that cannot be read. The foreign key
		// cascades, so a missing signal means the signal was deleted — there
		// is nothing left to tell the owner about, and retrying would only
		// re-read the same absence.
		if errors.Is(err, constants.ErrNotFound) {
			return u.giveUp(ctx, queued, "the signal this notification points at no longer exists")
		}
		// Anything else is the database being unavailable, which is worth
		// another attempt.
		return u.retryOrGiveUp(ctx, queued, err)
	}

	device, err := u.cfg.Devices.FetchDevice(ctx)
	if errors.Is(err, constants.ErrNotFound) {
		// No phone has registered yet. The alert is not undeliverable, it is
		// not yet deliverable, and the difference decides whether the signal
		// is still waiting when the app is finally installed.
		//
		// Spending an attempt here would be wrong twice over: the retry
		// budget is five attempts over about eight minutes, so the first
		// signals after switching to notify mode would all be marked failed
		// long before anyone opened the app, and the recorded reason would
		// read like a network problem.
		return outcomeNoDevice
	}
	if err != nil {
		// The registration could not be read — the database, not the phone.
		return u.retryOrGiveUp(ctx, queued, err)
	}

	if err := u.cfg.Sender.Send(ctx, notify.BuildMessage(device.Token, signalRow)); err != nil {
		if errors.Is(err, notify.ErrUndeliverable) {
			// Retrying will not fix it: an uninstalled token, a malformed
			// payload. Spending the attempt budget on it would delay every
			// alert behind it and then record a reason that reads like a
			// network problem.
			return u.giveUp(ctx, queued, err.Error())
		}
		return u.retryOrGiveUp(ctx, queued, err)
	}

	sent, err := u.repo.MarkNotificationSent(ctx, queued.Id, u.cfg.Now())
	if err != nil {
		// Delivered, and the record of it failed. Saying so matters: the row
		// stays pending and will be delivered again, so the owner may see the
		// same alert twice. That is the right way round — a duplicate alert
		// is a nuisance and a lost one is a missed signal — but it must not
		// happen silently.
		u.log.ErrorContext(ctx, "a notification was delivered and could not be recorded as sent",
			"error", err, "notification_id", queued.Id, "signal_id", queued.SignalId.String(),
			"consequence", "it stays pending and may be delivered again")
		return outcomeUnrecorded
	}

	u.log.InfoContext(ctx, "signal delivered",
		"notification_id", sent.Id, "signal_id", sent.SignalId.String(),
		"attempts", sent.Attempts)
	return outcomeSent
}

// retryOrGiveUp records a transient failure, or gives up once the attempt
// budget is spent.
func (u *notifyUsecase) retryOrGiveUp(
	ctx context.Context, queued models.Notification, cause error,
) outcome {
	// Attempts counts what has already been tried; this one makes it one more.
	attempted := int(queued.Attempts) + 1
	if attempted >= constants.NotificationMaxAttempts {
		return u.giveUp(ctx, queued, fmt.Sprintf(
			"gave up after %d attempts, last error: %v", attempted, cause))
	}

	at := u.cfg.Now().Add(backoff(queued.Attempts))
	if _, err := u.repo.RescheduleNotification(ctx, queued.Id, cause.Error(), at); err != nil {
		u.log.ErrorContext(ctx, "could not reschedule a failed delivery",
			"error", err, "notification_id", queued.Id)
		return outcomeUnrecorded
	}

	u.log.WarnContext(ctx, "delivery failed and will be retried",
		"error", cause, "notification_id", queued.Id,
		"attempt", attempted, "of", constants.NotificationMaxAttempts,
		"next_attempt_at", at.UTC().Format(time.RFC3339))
	return outcomeRetrying
}

// giveUp marks a notification failed. Nothing retries it afterwards, so the
// reason recorded is the last thing said about it.
func (u *notifyUsecase) giveUp(
	ctx context.Context, queued models.Notification, reason string,
) outcome {
	if _, err := u.repo.FailNotification(ctx, queued.Id, reason); err != nil {
		u.log.ErrorContext(ctx, "could not mark a delivery failed",
			"error", err, "notification_id", queued.Id)
		return outcomeUnrecorded
	}

	u.log.ErrorContext(ctx, "gave up delivering a signal; the signal is still recorded",
		"notification_id", queued.Id, "signal_id", queued.SignalId.String(),
		"reason", reason)
	return outcomeGaveUp
}

// backoff is how long to wait before the next attempt, doubling each time.
//
// It is computed from the attempts already made rather than held in memory,
// so the wait survives a restart: a process that forgot would retry at once,
// turning an outage plus a crash loop into a tight loop against a service
// that is already struggling.
func backoff(attemptsSoFar int32) time.Duration {
	if attemptsSoFar < 0 {
		attemptsSoFar = 0
	}
	if attemptsSoFar > constants.NotificationMaxAttempts {
		attemptsSoFar = constants.NotificationMaxAttempts
	}
	return constants.NotifyRetryBase << attemptsSoFar
}
