package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
)

// stubEvaluator records every bar it is shown and answers with a fixed result.
type stubEvaluator struct {
	seen   []models.Candle
	signal models.Signal
	ok     bool
	err    error
}

func (s *stubEvaluator) Warmup(context.Context) error { return nil }
func (s *stubEvaluator) Ready() (bool, string)        { return true, "" }
func (s *stubEvaluator) OnClosedCandle(
	_ context.Context, bar models.Candle,
) (models.Signal, bool, error) {
	s.seen = append(s.seen, bar)
	return s.signal, s.ok, s.err
}

// stubQueue counts what was offered and can fail on demand.
type stubQueue struct {
	offered  []models.Signal
	err      error
	delivers bool
}

func (q *stubQueue) Delivers() bool { return q.delivers }
func (q *stubQueue) QueueSignal(
	_ context.Context, signal models.Signal,
) (models.Notification, bool, error) {
	q.offered = append(q.offered, signal)
	if q.err != nil {
		return models.Notification{}, false, q.err
	}
	if !q.delivers {
		return models.Notification{}, false, nil
	}
	return models.Notification{Id: int64(len(q.offered)), SignalId: signal.Id}, true, nil
}

func aRecordedSignal() models.Signal {
	return models.Signal{
		Id:              uuid.New(),
		Symbol:          "BTCUSDT",
		MarketType:      constants.MarketTypeSpot,
		Timeframe:       constants.Timeframe4h,
		SignalTime:      time.Date(2026, 8, 1, 4, 0, 0, 0, time.UTC),
		Direction:       constants.DirectionLong,
		SignalPrice:     decimal.NullDecimal{Decimal: decimal.NewFromInt(64000), Valid: true},
		StrategyName:    "ema_crossover",
		StrategyVersion: "v1",
	}
}

func aClosedBar() models.Candle {
	at := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	return models.Candle{
		Symbol: "BTCUSDT", MarketType: constants.MarketTypeSpot,
		Timeframe: constants.Timeframe4h,
		OpenTime:  at, CloseTime: at.Add(4 * time.Hour),
		Open: decimal.NewFromInt(64000), High: decimal.NewFromInt(64100),
		Low: decimal.NewFromInt(63900), Close: decimal.NewFromInt(64000),
		Volume: decimal.NewFromInt(10), QuoteVolume: decimal.NewFromInt(640000),
		IsClosed: true,
	}
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// TestAQueueFailureDoesNotCostTheSignal.
//
// The signal is the artefact and the notification is a convenience. A
// collector that stopped, or unwound a committed signal, because a second row
// could not be written would have the priority backwards — the owner would
// lose the record of what the strategy said in order to protect an alert
// about it.
func TestAQueueFailureDoesNotCostTheSignal(t *testing.T) {
	recorded := aRecordedSignal()
	evaluator := &stubEvaluator{signal: recorded, ok: true}
	queue := &stubQueue{delivers: true, err: errors.New("the database is unreachable")}

	observe := closedCandleObserver(quiet(), evaluator, queue)
	if observe == nil {
		t.Fatal("a configured evaluator produced no observer")
	}

	// The observer returns nothing and must not panic: it is called from the
	// collector's write loop, and taking that down would stop candle storage.
	observe(context.Background(), aClosedBar())

	if len(evaluator.seen) != 1 {
		t.Errorf("the bar reached the evaluator %d times, want 1", len(evaluator.seen))
	}
	if len(queue.offered) != 1 {
		t.Fatalf("the signal was offered to the queue %d times, want 1", len(queue.offered))
	}
	if queue.offered[0].Id != recorded.Id {
		t.Errorf("the queue was offered %s, want the recorded signal %s",
			queue.offered[0].Id, recorded.Id)
	}
}

// TestNothingIsQueuedForABarThatProducedNoSignal, because an empty queue row
// would be delivered as an alert about nothing.
func TestNothingIsQueuedForABarThatProducedNoSignal(t *testing.T) {
	for name, evaluator := range map[string]*stubEvaluator{
		"no decision":      {ok: false},
		"recording failed": {ok: false, err: errors.New("the database is unreachable")},
	} {
		t.Run(name, func(t *testing.T) {
			queue := &stubQueue{delivers: true}
			closedCandleObserver(quiet(), evaluator, queue)(context.Background(), aClosedBar())

			if len(queue.offered) != 0 {
				t.Errorf("%d signals were queued for a bar that produced none", len(queue.offered))
			}
		})
	}
}

// TestNoEvaluatorMeansNoObserver, so a collector with no strategy configured
// does exactly what it did before: it collects candles.
func TestNoEvaluatorMeansNoObserver(t *testing.T) {
	if observe := closedCandleObserver(quiet(), nil, &stubQueue{}); observe != nil {
		t.Error("an unconfigured collector installed a signal observer")
	}
}
