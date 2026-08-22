package usecase_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	_notify_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/notify/usecase"
)

// recordingQueue is a queue that remembers what reached it, and nothing else.
type recordingQueue struct {
	mu       sync.Mutex
	queued   []models.Notification
	err      error
	conflict bool
}

func (q *recordingQueue) InsertNotification(
	_ context.Context, n models.Notification,
) (models.Notification, bool, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.err != nil {
		return models.Notification{}, false, q.err
	}
	if q.conflict {
		return models.Notification{}, false, nil
	}

	n.Id = int64(len(q.queued) + 1)
	n.Status = constants.NotificationStatusPending
	q.queued = append(q.queued, n)
	return n, true, nil
}

func (q *recordingQueue) FetchPendingNotifications(
	context.Context, int32,
) ([]models.Notification, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return append([]models.Notification(nil), q.queued...), nil
}

func (q *recordingQueue) rows() []models.Notification {
	q.mu.Lock()
	defer q.mu.Unlock()
	return append([]models.Notification(nil), q.queued...)
}

func aSignal() models.Signal {
	return models.Signal{
		Id:              uuid.New(),
		Symbol:          "BTCUSDT",
		MarketType:      constants.MarketTypeSpot,
		Timeframe:       constants.Timeframe4h,
		Direction:       constants.DirectionLong,
		StrategyName:    "ema_crossover",
		StrategyVersion: "v1",
	}
}

// TestSilentQueuesNothing.
//
// The default mode, and the one that has to be right: a deploy that has not
// been told to alert must not alert. Silence is structural here — the usecase
// never reaches its repository — rather than a check somewhere further down
// that a later change could forget.
func TestSilentQueuesNothing(t *testing.T) {
	queue := &recordingQueue{}
	usecase, err := _notify_us.NewNotifyUsecaseImpl(queue, constants.SignalModeSilent)
	if err != nil {
		t.Fatalf("NewNotifyUsecaseImpl() returned error: %v", err)
	}

	if usecase.Delivers() {
		t.Error("silent mode reports that it delivers")
	}

	_, ok, err := usecase.QueueSignal(context.Background(), aSignal())
	if err != nil {
		t.Fatalf("QueueSignal() returned error: %v", err)
	}
	if ok {
		t.Error("silent mode reported a signal as queued")
	}
	if got := queue.rows(); len(got) != 0 {
		t.Errorf("silent mode wrote %d rows to the queue", len(got))
	}
}

// TestNotifyQueuesTheSignalRatherThanSendingIt.
//
// The queue row is the deliverable of this path. Sending from here would put
// a third party's availability in the collector's candle loop, and a Firebase
// outage would then cost signals rather than alerts.
func TestNotifyQueuesTheSignalRatherThanSendingIt(t *testing.T) {
	queue := &recordingQueue{}
	usecase, err := _notify_us.NewNotifyUsecaseImpl(queue, constants.SignalModeNotify)
	if err != nil {
		t.Fatalf("NewNotifyUsecaseImpl() returned error: %v", err)
	}

	if !usecase.Delivers() {
		t.Error("notify mode reports that it does not deliver")
	}

	signal := aSignal()
	queued, ok, err := usecase.QueueSignal(context.Background(), signal)
	if err != nil || !ok {
		t.Fatalf("QueueSignal() = %v, %v", ok, err)
	}

	rows := queue.rows()
	if len(rows) != 1 {
		t.Fatalf("queued %d rows for one signal", len(rows))
	}
	if rows[0].SignalId != signal.Id {
		t.Errorf("the queued row points at %s, want %s", rows[0].SignalId, signal.Id)
	}
	if rows[0].Channel != constants.NotificationChannelFCM {
		t.Errorf("Channel = %q, want fcm", rows[0].Channel)
	}
	if queued.Status != constants.NotificationStatusPending {
		t.Errorf("Status = %q, want pending", queued.Status)
	}
}

// TestAlreadyQueuedIsNotAnError.
//
// Offering the same signal twice happens on a restart that re-walks a bar.
// The right answer is one alert and no noise, so the repository's idempotent
// insert has to read as success and not as a fault.
func TestAlreadyQueuedIsNotAnError(t *testing.T) {
	queue := &recordingQueue{conflict: true}
	usecase, err := _notify_us.NewNotifyUsecaseImpl(queue, constants.SignalModeNotify)
	if err != nil {
		t.Fatalf("NewNotifyUsecaseImpl() returned error: %v", err)
	}

	_, ok, err := usecase.QueueSignal(context.Background(), aSignal())
	if err != nil {
		t.Errorf("an already-queued signal was reported as an error: %v", err)
	}
	if ok {
		t.Error("an already-queued signal was reported as newly queued")
	}
}

// TestAQueueFailureIsReported, because it is not the same as a duplicate and
// the caller decides what to do about it.
func TestAQueueFailureIsReported(t *testing.T) {
	queue := &recordingQueue{err: errors.New("the database is unreachable")}
	usecase, err := _notify_us.NewNotifyUsecaseImpl(queue, constants.SignalModeNotify)
	if err != nil {
		t.Fatalf("NewNotifyUsecaseImpl() returned error: %v", err)
	}

	_, ok, err := usecase.QueueSignal(context.Background(), aSignal())
	if err == nil {
		t.Fatal("a queue failure was swallowed")
	}
	if ok {
		t.Error("a failed write was reported as queued")
	}
}

// TestASignalWithNoIdIsRefused.
//
// A row pointing at nothing would fail the foreign key later, inside a
// delivery worker, where the cause is much harder to see than here.
func TestASignalWithNoIdIsRefused(t *testing.T) {
	queue := &recordingQueue{}
	usecase, err := _notify_us.NewNotifyUsecaseImpl(queue, constants.SignalModeNotify)
	if err != nil {
		t.Fatalf("NewNotifyUsecaseImpl() returned error: %v", err)
	}

	unsaved := aSignal()
	unsaved.Id = uuid.Nil

	if _, _, err := usecase.QueueSignal(context.Background(), unsaved); err == nil {
		t.Error("a signal that was never persisted was queued")
	}
	if got := queue.rows(); len(got) != 0 {
		t.Errorf("wrote %d rows for a signal with no id", len(got))
	}
}

// TestAnUnknownModeIsRefusedAtConstruction, rather than being treated as one
// of the two and picked at random by a switch's default arm.
func TestAnUnknownModeIsRefusedAtConstruction(t *testing.T) {
	for _, mode := range []constants.SignalMode{"", "uat", "test", "enabled"} {
		if _, err := _notify_us.NewNotifyUsecaseImpl(&recordingQueue{}, mode); err == nil {
			t.Errorf("mode %q was accepted", mode)
		}
	}
}
