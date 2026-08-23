package usecase_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/market"
	_market_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/market/usecase"
)

// watchedCandles records what the closed-candle observer was shown.
type watchedCandles struct {
	mu   sync.Mutex
	seen []models.Candle
}

func (w *watchedCandles) observe(_ context.Context, c models.Candle) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.seen = append(w.seen, c)
}

func (w *watchedCandles) times() []time.Time {
	w.mu.Lock()
	defer w.mu.Unlock()

	out := make([]time.Time, 0, len(w.seen))
	for _, c := range w.seen {
		out = append(out, c.OpenTime)
	}
	return out
}

// TestEveryStoredCandleReachesTheObserver.
//
// # The defect this pins
//
// writeLoop called the observer and drainRemaining did not. Every closed
// candle still buffered when the context was cancelled was therefore stored
// without ever being shown to the strategy, and the signals for those bars
// were lost permanently: warm-up replays stored history and deliberately
// emits nothing, so a restart never revisits them.
//
// Nothing reported it. The candle series stayed complete, no error was
// logged, and a strategy producing a tenth of a signal a day gives nobody a
// baseline to miss one against. It is the same shape as the writer defect
// phase 02 fixed — confirmed work dropped on cancellation, invisibly.
//
// The invariant is therefore not "the drain calls the observer" but the one
// that has to hold however the code is later rearranged: a candle that was
// stored was seen.
func TestEveryStoredCandleReachesTheObserver(t *testing.T) {
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)

	stream := []market.StreamedKline{
		{Candle: makeCandle(start, true)},
		{Candle: makeCandle(start.Add(time.Minute), true)},
		{Candle: makeCandle(start.Add(2*time.Minute), false)},
		{Candle: makeCandle(start.Add(3*time.Minute), true)},
	}

	watcher := &watchedCandles{}
	cfg := testConfig()
	cfg.OnClosedCandle = watcher.observe

	candles := &recordingCandleUsecase{}
	us := _market_us.NewMarketUsecaseImpl(cfg, silentLogger(),
		&fakeMarketData{stream: stream, now: start}, &stubStatusRepo{}, candles, &stubGapUsecase{})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = us.Run(ctx)

	stored := candles.storedCandles()
	seen := watcher.times()

	if len(stored) == 0 {
		t.Fatal("nothing was stored, so the test proves nothing")
	}
	if len(seen) != len(stored) {
		t.Fatalf("stored %d candles and showed the observer %d.\n"+
			"A stored candle that was never evaluated is a signal lost with nothing to report it.",
			len(stored), len(seen))
	}
	for i := range stored {
		if !stored[i].OpenTime.Equal(seen[i]) {
			t.Errorf("candle %d: stored %s, observed %s",
				i, stored[i].OpenTime.Format(time.RFC3339), seen[i].Format(time.RFC3339))
		}
	}
}
