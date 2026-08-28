package repository_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/notify"
	_notify_repo "github.com/spioneracorei8/btcusd-trading-platform/server/services/notify/repository"
	_signal_repo "github.com/spioneracorei8/btcusd-trading-platform/server/services/signal/repository"
	"github.com/spioneracorei8/btcusd-trading-platform/server/testhelper"
)

// storeSignal writes one signal so a notification has something to point at.
func storeSignal(t *testing.T, pool *pgxpool.Pool, symbol string, at time.Time) models.Signal {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	stored, err := _signal_repo.NewSignalRepoImpl(pool).InsertSignal(ctx, models.Signal{
		Symbol:          symbol,
		MarketType:      constants.MarketTypeSpot,
		Timeframe:       constants.Timeframe4h,
		SignalTime:      at,
		Direction:       constants.DirectionLong,
		Strength:        decimal.NewFromInt(constants.SignalStrengthNotReported),
		SignalPrice:     decimal.NullDecimal{Decimal: decimal.RequireFromString("64000"), Valid: true},
		StrategyName:    "ema_crossover",
		StrategyVersion: "v1",
	})
	if err != nil {
		t.Fatalf("InsertSignal() returned error: %v", err)
	}
	return stored
}

// TestQueuingTheSameSignalTwiceQueuesItOnce.
//
// The signal and its notification are two writes, so a process can die
// between them and leave a signal with nothing queued. Recovering from that
// means offering the signal again, which is only safe if a second offer does
// nothing — otherwise the recovery is itself a second alert.
func TestQueuingTheSameSignalTwiceQueuesItOnce(t *testing.T) {
	pool := testhelper.NewTestPool(t)
	const symbol = "TESTNOTIFYONCE"
	testhelper.CleanupSymbol(t, pool, symbol)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	signal := storeSignal(t, pool, symbol, time.Date(2026, 8, 1, 4, 0, 0, 0, time.UTC))
	repo := _notify_repo.NewNotifyRepoImpl(pool)
	row := models.Notification{SignalId: signal.Id, Channel: constants.NotificationChannelWebPush}

	first, queued, err := repo.InsertNotification(ctx, row)
	if err != nil {
		t.Fatalf("first InsertNotification() returned error: %v", err)
	}
	if !queued {
		t.Fatal("the first offer of a signal was not queued")
	}
	if first.Status != constants.NotificationStatusPending {
		t.Errorf("Status = %q, want pending", first.Status)
	}
	if first.Attempts != 0 {
		t.Errorf("Attempts = %d, want 0", first.Attempts)
	}
	if first.SentAt != nil {
		t.Errorf("SentAt = %v, want unset on a row nothing has delivered", first.SentAt)
	}

	second, queued, err := repo.InsertNotification(ctx, row)
	if err != nil {
		t.Fatalf("second InsertNotification() returned error: %v", err)
	}
	if queued {
		t.Errorf("the same signal was queued a second time as %+v", second)
	}

	pending, err := repo.FetchDueNotifications(ctx, time.Now(), 10)
	if err != nil {
		t.Fatalf("FetchDueNotifications() returned error: %v", err)
	}
	mine := 0
	for _, n := range pending {
		if n.SignalId == signal.Id {
			mine++
		}
	}
	if mine != 1 {
		t.Errorf("the queue holds %d rows for one signal, want 1", mine)
	}
}

// TestThePendingQueueDrainsOldestFirst.
//
// A backlog is the case that matters: after an outage the owner should see
// what happened in the order it happened, not the newest alert first with the
// rest arriving behind it.
func TestThePendingQueueDrainsOldestFirst(t *testing.T) {
	pool := testhelper.NewTestPool(t)
	const symbol = "TESTNOTIFYORDER"
	testhelper.CleanupSymbol(t, pool, symbol)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	repo := _notify_repo.NewNotifyRepoImpl(pool)
	base := time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC)

	var want []int64
	for i := range 3 {
		signal := storeSignal(t, pool, symbol, base.Add(time.Duration(i)*4*time.Hour))
		queued, ok, err := repo.InsertNotification(ctx, models.Notification{
			SignalId: signal.Id, Channel: constants.NotificationChannelWebPush,
		})
		if err != nil || !ok {
			t.Fatalf("signal %d: InsertNotification() = %v, %v", i, ok, err)
		}
		want = append(want, queued.Id)
	}

	pending, err := repo.FetchDueNotifications(ctx, time.Now(), 10)
	if err != nil {
		t.Fatalf("FetchDueNotifications() returned error: %v", err)
	}

	var got []int64
	for _, n := range pending {
		for _, id := range want {
			if n.Id == id {
				got = append(got, n.Id)
			}
		}
	}
	if len(got) != len(want) {
		t.Fatalf("the queue returned %d of %d rows", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("the queue drains in order %v, want %v", got, want)
		}
	}
}

// TestANonPositiveLimitIsRefused, because LIMIT 0 silently returns an empty
// queue, which a worker cannot tell from a queue that is genuinely empty.
func TestANonPositiveLimitIsRefused(t *testing.T) {
	pool := testhelper.NewTestPool(t)
	repo := _notify_repo.NewNotifyRepoImpl(pool)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	for _, limit := range []int32{0, -1} {
		if _, err := repo.FetchDueNotifications(ctx, time.Now(), limit); err == nil {
			t.Errorf("a limit of %d was accepted", limit)
		}
	}
}

// TestABackoffOutlivesTheProcessThatDecidedIt.
//
// Backoff held only in a worker's memory is not backoff: a restart forgets it
// and retries at once, so an outage plus a crash loop becomes a tight loop
// against a service that is already struggling, and the five-attempt budget
// is spent in seconds instead of minutes.
func TestABackoffOutlivesTheProcessThatDecidedIt(t *testing.T) {
	pool := testhelper.NewTestPool(t)
	const symbol = "TESTNOTIFYBACKOFF"
	testhelper.CleanupSymbol(t, pool, symbol)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	repo := _notify_repo.NewNotifyRepoImpl(pool)
	signal := storeSignal(t, pool, symbol, time.Date(2026, 8, 3, 4, 0, 0, 0, time.UTC))

	queued, ok, err := repo.InsertNotification(ctx, models.Notification{
		SignalId: signal.Id, Channel: constants.NotificationChannelWebPush,
	})
	if err != nil || !ok {
		t.Fatalf("InsertNotification() = %v, %v", ok, err)
	}

	// A new row is due immediately; that is what an alert wants.
	now := time.Now().UTC()
	if !contains(dueIds(t, repo, ctx, now), queued.Id) {
		t.Fatal("a newly queued notification is not due")
	}

	retryAt := now.Add(30 * time.Second)
	failed, err := repo.RescheduleNotification(ctx, queued.Id, "502 Bad Gateway", retryAt)
	if err != nil {
		t.Fatalf("RescheduleNotification() returned error: %v", err)
	}
	if failed.Attempts != 1 {
		t.Errorf("Attempts = %d, want 1", failed.Attempts)
	}
	if failed.Status != constants.NotificationStatusPending {
		t.Errorf("Status = %q, want pending; a transient failure is not the end", failed.Status)
	}
	if failed.LastError != "502 Bad Gateway" {
		t.Errorf("LastError = %q", failed.LastError)
	}

	// Read back by a different call, which is what a restarted process does.
	if contains(dueIds(t, repo, ctx, retryAt.Add(-time.Second)), queued.Id) {
		t.Error("a rescheduled notification is due before its backoff has passed")
	}
	if !contains(dueIds(t, repo, ctx, retryAt), queued.Id) {
		t.Error("a rescheduled notification is not due once its backoff has passed")
	}
}

// TestASentNotificationLeavesTheQueue, and a failed one does too.
func TestASentNotificationLeavesTheQueue(t *testing.T) {
	pool := testhelper.NewTestPool(t)
	const symbol = "TESTNOTIFYFINAL"
	testhelper.CleanupSymbol(t, pool, symbol)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	repo := _notify_repo.NewNotifyRepoImpl(pool)
	base := time.Date(2026, 8, 4, 0, 0, 0, 0, time.UTC)

	queue := func(i int) models.Notification {
		t.Helper()
		signal := storeSignal(t, pool, symbol, base.Add(time.Duration(i)*4*time.Hour))
		queued, ok, err := repo.InsertNotification(ctx, models.Notification{
			SignalId: signal.Id, Channel: constants.NotificationChannelWebPush,
		})
		if err != nil || !ok {
			t.Fatalf("InsertNotification() = %v, %v", ok, err)
		}
		return queued
	}

	sent, failed := queue(0), queue(1)

	at := time.Now().UTC()
	delivered, err := repo.MarkNotificationSent(ctx, sent.Id, at)
	if err != nil {
		t.Fatalf("MarkNotificationSent() returned error: %v", err)
	}
	if delivered.Status != constants.NotificationStatusSent {
		t.Errorf("Status = %q, want sent", delivered.Status)
	}
	if delivered.SentAt == nil || !delivered.SentAt.Equal(at.Truncate(time.Microsecond)) {
		t.Errorf("SentAt = %v, want %v", delivered.SentAt, at)
	}
	if delivered.LastError != "" {
		t.Errorf("LastError = %q, want cleared on success", delivered.LastError)
	}

	givenUp, err := repo.FailNotification(ctx, failed.Id, "gave up after 5 attempts")
	if err != nil {
		t.Fatalf("FailNotification() returned error: %v", err)
	}
	if givenUp.Status != constants.NotificationStatusFailed {
		t.Errorf("Status = %q, want failed", givenUp.Status)
	}

	// Neither is due again, ever.
	due := dueIds(t, repo, ctx, time.Now().UTC().Add(365*24*time.Hour))
	for _, id := range []int64{sent.Id, failed.Id} {
		if contains(due, id) {
			t.Errorf("notification %d is still in the queue after being finished", id)
		}
	}
}

// TestFinishingANotificationThatIsNotThereIsReported.
//
// An UPDATE matching no row would otherwise let the caller believe it just
// recorded an outcome that nothing recorded.
func TestFinishingANotificationThatIsNotThereIsReported(t *testing.T) {
	pool := testhelper.NewTestPool(t)
	repo := _notify_repo.NewNotifyRepoImpl(pool)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const absent = int64(-1)
	if _, err := repo.MarkNotificationSent(ctx, absent, time.Now()); err == nil {
		t.Error("marking a missing notification sent returned no error")
	}
	if _, err := repo.RescheduleNotification(ctx, absent, "x", time.Now()); err == nil {
		t.Error("rescheduling a missing notification returned no error")
	}
	if _, err := repo.FailNotification(ctx, absent, "x"); err == nil {
		t.Error("failing a missing notification returned no error")
	}
}

// dueIds is the ids the queue would hand a worker at a given instant.
func dueIds(
	t *testing.T, repo notify.NotifyRepository, ctx context.Context, at time.Time,
) []int64 {
	t.Helper()

	due, err := repo.FetchDueNotifications(ctx, at, 100)
	if err != nil {
		t.Fatalf("FetchDueNotifications() returned error: %v", err)
	}

	ids := make([]int64, 0, len(due))
	for _, n := range due {
		ids = append(ids, n.Id)
	}
	return ids
}

func contains(ids []int64, want int64) bool {
	return slices.Contains(ids, want)
}
