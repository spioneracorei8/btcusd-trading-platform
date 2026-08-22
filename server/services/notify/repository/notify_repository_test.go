package repository_test

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
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
	row := models.Notification{SignalId: signal.Id, Channel: constants.NotificationChannelFCM}

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

	pending, err := repo.FetchPendingNotifications(ctx, 10)
	if err != nil {
		t.Fatalf("FetchPendingNotifications() returned error: %v", err)
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
			SignalId: signal.Id, Channel: constants.NotificationChannelFCM,
		})
		if err != nil || !ok {
			t.Fatalf("signal %d: InsertNotification() = %v, %v", i, ok, err)
		}
		want = append(want, queued.Id)
	}

	pending, err := repo.FetchPendingNotifications(ctx, 10)
	if err != nil {
		t.Fatalf("FetchPendingNotifications() returned error: %v", err)
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
		if _, err := repo.FetchPendingNotifications(ctx, limit); err == nil {
			t.Errorf("a limit of %d was accepted", limit)
		}
	}
}
