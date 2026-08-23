package usecase_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/candle"
	_indicator_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/indicator/usecase"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/signal"
	_signal_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/signal/usecase"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/strategy"
)

// ---------------------------------------------------------------------------
// Fakes. Nothing here touches a database.
// ---------------------------------------------------------------------------

// storedCandles serves a prepared series and nothing else.
type storedCandles struct {
	series []models.Candle
}

func (s *storedCandles) StreamCandles(
	ctx context.Context, params candle.FetchCandlesParams, onCandle func(models.Candle) error,
) error {
	for _, c := range s.series {
		if c.OpenTime.Before(params.From) || c.OpenTime.After(params.To) {
			continue
		}
		if err := onCandle(c); err != nil {
			return err
		}
	}
	return nil
}

func (s *storedCandles) FetchLatestCandle(
	context.Context, string, constants.MarketType, constants.Timeframe,
) (models.Candle, error) {
	if len(s.series) == 0 {
		return models.Candle{}, constants.ErrNotFound
	}
	return s.series[len(s.series)-1], nil
}

func (s *storedCandles) FetchEarliestCandle(
	context.Context, string, constants.MarketType, constants.Timeframe,
) (models.Candle, error) {
	if len(s.series) == 0 {
		return models.Candle{}, constants.ErrNotFound
	}
	return s.series[0], nil
}

func (s *storedCandles) SaveCandle(context.Context, models.Candle) error    { return nil }
func (s *storedCandles) SaveCandles(context.Context, []models.Candle) error { return nil }
func (s *storedCandles) FetchCandles(context.Context, candle.FetchCandlesParams) ([]models.Candle, error) {
	return s.series, nil
}
func (s *storedCandles) FindGaps(context.Context, string, constants.MarketType, constants.Timeframe) ([]candle.Gap, error) {
	return nil, nil
}
func (s *storedCandles) CountCandles(context.Context, string, constants.MarketType, constants.Timeframe) (int64, error) {
	return int64(len(s.series)), nil
}
func (s *storedCandles) OpenCursor(candle.FetchCandlesParams) candle.CandleCursor { return nil }

// recordingSignals captures what the evaluator asked to store.
//
// It enforces the same unique key the signals table does, so a test can hand
// one store to two evaluators and see what a restart would really do.
type recordingSignals struct {
	mu        sync.Mutex
	created   []models.Signal
	bars      []models.Candle
	seen      map[string]bool
	err       error
	duplicate bool
}

func (r *recordingSignals) CreateSignal(
	_ context.Context, s models.Signal, bar models.Candle,
) (models.Signal, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.duplicate {
		return models.Signal{}, constants.ErrDuplicateSignal
	}
	if r.err != nil {
		return models.Signal{}, r.err
	}
	if !bar.IsClosed {
		return models.Signal{}, constants.ErrUnclosedCandle
	}

	// signals_unique_per_bar, in a map.
	key := strings.Join([]string{
		s.StrategyName, s.StrategyVersion, s.Symbol,
		s.MarketType.String(), s.Timeframe.String(),
		s.SignalTime.UTC().Format(time.RFC3339Nano),
	}, "|")
	if r.seen[key] {
		return models.Signal{}, constants.ErrDuplicateSignal
	}
	if r.seen == nil {
		r.seen = map[string]bool{}
	}
	r.seen[key] = true

	s.Id = uuid.New()
	r.created = append(r.created, s)
	r.bars = append(r.bars, bar)
	return s, nil
}

// FetchSignalById is not what these tests exercise; the delivery worker is
// the only caller and it has its own.
func (r *recordingSignals) FetchSignalById(
	_ context.Context, id uuid.UUID,
) (models.Signal, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, s := range r.created {
		if s.Id == id {
			return s, nil
		}
	}
	return models.Signal{}, constants.ErrNotFound
}

// SetEntryPrice is the outcome follower's, not this path's: the evaluator
// deliberately leaves the entry unset.
func (r *recordingSignals) SetEntryPrice(
	context.Context, uuid.UUID, decimal.Decimal,
) (models.Signal, error) {
	return models.Signal{}, errors.New("the evaluator must not set an entry price")
}

func (r *recordingSignals) stored() []models.Signal {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]models.Signal(nil), r.created...)
}

// alwaysLong enters on every bar it is shown, so the plumbing is what is being
// measured rather than a rule.
type alwaysLong struct{ warmup int }

func (a *alwaysLong) OnBar(bar strategy.BarContext) []strategy.Intent {
	return []strategy.Intent{strategy.EnterLong(
		bar.Candle.Close.Sub(decimal.NewFromInt(100)),
		bar.Candle.Close.Add(decimal.NewFromInt(200)),
		"always long",
	)}
}
func (a *alwaysLong) WarmupPeriod() int { return a.warmup }
func (a *alwaysLong) Name() string      { return "always_long" }
func (a *alwaysLong) Version() string   { return "v1" }

// neverTrades is the control: a strategy that says nothing must record nothing.
type neverTrades struct{}

func (neverTrades) OnBar(strategy.BarContext) []strategy.Intent { return nil }
func (neverTrades) WarmupPeriod() int                           { return 0 }
func (neverTrades) Name() string                                { return "never_trades" }
func (neverTrades) Version() string                             { return "v1" }

var evalStart = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// evalBar builds one closed 4h candle with a range, so the ATR converges.
func evalBar(index int, price int64) models.Candle {
	at := evalStart.Add(time.Duration(index) * 4 * time.Hour)
	value := decimal.NewFromInt(price)

	return models.Candle{
		Symbol: "BTCUSDT", MarketType: constants.MarketTypeSpot,
		Timeframe: constants.Timeframe4h,
		OpenTime:  at, CloseTime: at.Add(4 * time.Hour),
		Open:   value,
		High:   value.Add(decimal.NewFromInt(50)),
		Low:    value.Sub(decimal.NewFromInt(50)),
		Close:  value,
		Volume: decimal.NewFromInt(10), QuoteVolume: decimal.NewFromInt(100000),
		TradeCount: 100, IsClosed: true,
	}
}

// series builds n closed bars that drift upward.
func series(n int) []models.Candle {
	out := make([]models.Candle, 0, n)
	for i := range n {
		out = append(out, evalBar(i, 27000+int64(i)*10))
	}
	return out
}

func silentLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// buildEvaluator wires one up over a prepared series.
func buildEvaluator(
	t *testing.T, history []models.Candle, strat strategy.Strategy, params map[string]string,
) (signal.SignalEvaluator, *recordingSignals) {
	t.Helper()
	return buildEvaluatorOn(t, &recordingSignals{}, history, strat, params)
}

// buildEvaluatorOn wires one up over a signal store the caller owns, so two
// evaluators can share it the way two runs of the collector share a table.
func buildEvaluatorOn(
	t *testing.T, signals *recordingSignals, history []models.Candle,
	strat strategy.Strategy, params map[string]string,
) (signal.SignalEvaluator, *recordingSignals) {
	t.Helper()

	evaluator, err := _signal_us.NewSignalEvaluatorImpl(
		_signal_us.EvaluatorConfig{
			Symbol:     "BTCUSDT",
			MarketType: constants.MarketTypeSpot,
			Timeframe:  constants.Timeframe4h,
			Strategy:   strat,
			Params:     params,
			Indicators: _indicator_us.DefaultSetConfig(),
		},
		silentLog(), &storedCandles{series: history}, signals,
	)
	if err != nil {
		t.Fatalf("NewSignalEvaluatorImpl() returned error: %v", err)
	}
	return evaluator, signals
}

// warmBars is enough history for the default indicator set to converge.
func warmBars() int { return _indicator_us.DefaultSetConfig().EMAPeriod * 5 * 2 }

// indicatorWarmup is how many bars the default set needs before it converges.
func indicatorWarmup(t *testing.T) int {
	t.Helper()

	set, err := _indicator_us.NewSet(_indicator_us.DefaultSetConfig())
	if err != nil {
		t.Fatalf("NewSet() returned error: %v", err)
	}
	return set.WarmupPeriod()
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestWarmupRecordsNothing.
//
// The bars it replays have already happened. A signal recorded for one would
// be dated long before the process that wrote it, which is the shape of the
// look-ahead this system exists to make impossible — and a notification about
// a bar that closed three months ago is noise.
func TestWarmupRecordsNothing(t *testing.T) {
	history := series(warmBars())

	evaluator, signals := buildEvaluator(t, history, &alwaysLong{}, nil)
	if err := evaluator.Warmup(context.Background()); err != nil {
		t.Fatalf("Warmup() returned error: %v", err)
	}

	if got := signals.stored(); len(got) != 0 {
		t.Errorf("warm-up recorded %d signals over %d replayed bars", len(got), len(history))
	}

	ready, why := evaluator.Ready()
	if !ready {
		t.Errorf("the evaluator is not ready after replaying %d bars: %s", len(history), why)
	}
}

// TestNothingIsRecordedBeforeTheIndicatorsConverge.
//
// A decision taken on values that have not converged is not a decision. The
// silence has to be explicable, so Ready says both how far along it is and
// what it is waiting for.
func TestNothingIsRecordedBeforeTheIndicatorsConverge(t *testing.T) {
	// Far too little history for the default EMA(200) to converge.
	history := series(10)

	evaluator, signals := buildEvaluator(t, history, &alwaysLong{}, nil)
	if err := evaluator.Warmup(context.Background()); err != nil {
		t.Fatalf("Warmup() returned error: %v", err)
	}

	ready, why := evaluator.Ready()
	if ready {
		t.Fatal("the evaluator reports itself warm on ten bars")
	}
	if !strings.Contains(why, "10 ") {
		t.Errorf("the reason does not say how much history there was: %q", why)
	}
	if !strings.Contains(why, "1000") {
		t.Errorf("the reason does not say how much is needed: %q", why)
	}

	_, ok, err := evaluator.OnClosedCandle(context.Background(), evalBar(10, 27100))
	if err != nil {
		t.Fatalf("OnClosedCandle() returned error: %v", err)
	}
	if ok || len(signals.stored()) != 0 {
		t.Error("a signal was recorded before the indicators had converged")
	}
}

// TestNothingIsRecordedUntilTheStrategyHasSeenItsOwnWarmup.
//
// A strategy may need more history than the indicators it reads: one counting
// twenty consecutive closes needs twenty of them regardless of how long the
// EMA took to settle. Converged indicators are therefore not sufficient, and
// the bar this separates is one where every indicator is ready and the
// strategy is still short.
func TestNothingIsRecordedUntilTheStrategyHasSeenItsOwnWarmup(t *testing.T) {
	converged := indicatorWarmup(t)
	need := converged + 40

	// Enough for every indicator, forty short of the strategy.
	history := series(converged + 10)

	evaluator, signals := buildEvaluator(t, history, &alwaysLong{warmup: need}, nil)
	if err := evaluator.Warmup(context.Background()); err != nil {
		t.Fatalf("Warmup() returned error: %v", err)
	}

	ready, why := evaluator.Ready()
	if ready {
		t.Fatalf("the evaluator is warm on %d bars for a strategy needing %d",
			len(history), need)
	}
	if !strings.Contains(why, "strategy") {
		t.Errorf("the reason blames something other than the strategy: %q", why)
	}

	// Up to one bar short of the requirement, still nothing.
	next := len(history)
	for ; next < need-1; next++ {
		if _, ok, err := evaluator.OnClosedCandle(
			context.Background(), evalBar(next, 28000)); err != nil || ok {
			t.Fatalf("bar %d: OnClosedCandle() = %v, %v", next, ok, err)
		}
	}
	if got := signals.stored(); len(got) != 0 {
		t.Fatalf("stored %d signals while the strategy was still short of its warm-up", len(got))
	}

	// The bar that completes it.
	if _, ok, err := evaluator.OnClosedCandle(
		context.Background(), evalBar(next, 28000)); err != nil || !ok {
		t.Fatalf("the bar completing the strategy warm-up recorded nothing: %v, %v", ok, err)
	}
}

// TestTheEvaluatorWarmsUpOnLiveBarsWhenThereIsNoHistory.
//
// A collector deployed against an empty database has nothing to replay. If
// warmth were decided at that moment it would be decided false for the life of
// the process, and the strategy would sit silent while the bars it was waiting
// for streamed past it one at a time.
func TestTheEvaluatorWarmsUpOnLiveBarsWhenThereIsNoHistory(t *testing.T) {
	converged := indicatorWarmup(t)

	evaluator, signals := buildEvaluator(t, nil, &alwaysLong{}, nil)
	if err := evaluator.Warmup(context.Background()); err != nil {
		t.Fatalf("Warmup() on an empty database returned error: %v", err)
	}
	if ready, _ := evaluator.Ready(); ready {
		t.Fatal("the evaluator reports itself warm with no data at all")
	}

	for i := range converged - 1 {
		if _, ok, err := evaluator.OnClosedCandle(
			context.Background(), evalBar(i, 27000+int64(i))); err != nil || ok {
			t.Fatalf("bar %d: OnClosedCandle() = %v, %v", i, ok, err)
		}
	}
	if got := signals.stored(); len(got) != 0 {
		t.Fatalf("stored %d signals before the indicators had converged", len(got))
	}

	// The bar that converges them.
	if _, ok, err := evaluator.OnClosedCandle(
		context.Background(), evalBar(converged-1, 28000)); err != nil || !ok {
		t.Fatalf("the evaluator never became warm on live bars: %v, %v", ok, err)
	}
	if ready, why := evaluator.Ready(); !ready {
		t.Errorf("the evaluator is still not ready after %d live bars: %s", converged, why)
	}
}

// TestALiveBarIsRecorded, which is the whole point of the path.
func TestALiveBarIsRecorded(t *testing.T) {
	history := series(warmBars())

	evaluator, signals := buildEvaluator(t, history, &alwaysLong{}, nil)
	if err := evaluator.Warmup(context.Background()); err != nil {
		t.Fatalf("Warmup() returned error: %v", err)
	}

	live := evalBar(len(history), 28000)
	recorded, ok, err := evaluator.OnClosedCandle(context.Background(), live)
	if err != nil {
		t.Fatalf("OnClosedCandle() returned error: %v", err)
	}
	if !ok {
		t.Fatal("a warm evaluator recorded nothing for a bar its strategy fired on")
	}

	if !recorded.SignalTime.Equal(live.CloseTime) {
		t.Errorf("signal_time is %s, want the bar's close %s", recorded.SignalTime, live.CloseTime)
	}
	if recorded.Direction != constants.DirectionLong {
		t.Errorf("direction is %s, want long", recorded.Direction)
	}
	if len(signals.stored()) != 1 {
		t.Errorf("stored %d signals for one bar", len(signals.stored()))
	}
}

// TestTheEntryPriceIsLeftForTheNextBar.
//
// A decision taken on a bar's close cannot fill on that close: the backtest
// fills at the next bar's open plus slippage. Recording the close as the entry
// would put that difference into every live-against-backtest comparison as
// though it were slippage, permanently — so signal_price carries what the
// strategy saw and entry_price waits.
func TestTheEntryPriceIsLeftForTheNextBar(t *testing.T) {
	history := series(warmBars())

	evaluator, _ := buildEvaluator(t, history, &alwaysLong{}, nil)
	if err := evaluator.Warmup(context.Background()); err != nil {
		t.Fatalf("Warmup() returned error: %v", err)
	}

	live := evalBar(len(history), 28000)
	recorded, ok, err := evaluator.OnClosedCandle(context.Background(), live)
	if err != nil || !ok {
		t.Fatalf("OnClosedCandle() = %v, %v", ok, err)
	}

	if recorded.EntryPrice.Valid {
		t.Errorf("entry_price is already %s; it cannot be known until the next bar opens",
			recorded.EntryPrice.Decimal)
	}
	if !recorded.SignalPrice.Valid {
		t.Fatal("signal_price is unset, so nothing records what the strategy decided on")
	}
	if !recorded.SignalPrice.Decimal.Equal(live.Close) {
		t.Errorf("signal_price is %s, want the bar's close %s",
			recorded.SignalPrice.Decimal, live.Close)
	}
}

// TestTheResolvedParametersAreOnEverySignal.
//
// Recorded once at startup, a parameter change between two signals leaves two
// incomparable groups in one table looking alike, and every reconciliation
// silently averages across it.
func TestTheResolvedParametersAreOnEverySignal(t *testing.T) {
	history := series(warmBars())
	params := map[string]string{"fast": "12", "slow": "26"}

	evaluator, _ := buildEvaluator(t, history, &alwaysLong{}, params)
	if err := evaluator.Warmup(context.Background()); err != nil {
		t.Fatalf("Warmup() returned error: %v", err)
	}

	for i := range 3 {
		recorded, ok, err := evaluator.OnClosedCandle(
			context.Background(), evalBar(len(history)+i, 28000+int64(i)*10))
		if err != nil || !ok {
			t.Fatalf("bar %d: OnClosedCandle() = %v, %v", i, ok, err)
		}

		reason := string(recorded.Reason)
		for name, value := range params {
			if !strings.Contains(reason, `"`+name+`"`) || !strings.Contains(reason, `"`+value+`"`) {
				t.Errorf("signal %d does not record %s=%s: %s", i, name, value, reason)
			}
		}
		if !strings.Contains(reason, "always_long") {
			t.Errorf("signal %d does not record which strategy produced it: %s", i, reason)
		}
	}
}

// TestAStrategyThatSaysNothingRecordsNothing.
func TestAStrategyThatSaysNothingRecordsNothing(t *testing.T) {
	history := series(warmBars())

	evaluator, signals := buildEvaluator(t, history, neverTrades{}, nil)
	if err := evaluator.Warmup(context.Background()); err != nil {
		t.Fatalf("Warmup() returned error: %v", err)
	}

	_, ok, err := evaluator.OnClosedCandle(context.Background(), evalBar(len(history), 28000))
	if err != nil {
		t.Fatalf("OnClosedCandle() returned error: %v", err)
	}
	if ok || len(signals.stored()) != 0 {
		t.Error("a strategy that returned no intents produced a signal")
	}
}

// TestAnUnclosedBarIsRefused. The rule the whole system rests on.
func TestAnUnclosedBarIsRefused(t *testing.T) {
	history := series(warmBars())

	evaluator, signals := buildEvaluator(t, history, &alwaysLong{}, nil)
	if err := evaluator.Warmup(context.Background()); err != nil {
		t.Fatalf("Warmup() returned error: %v", err)
	}

	forming := evalBar(len(history), 28000)
	forming.IsClosed = false

	_, ok, err := evaluator.OnClosedCandle(context.Background(), forming)
	if err != nil {
		t.Fatalf("OnClosedCandle() returned error: %v", err)
	}
	if ok || len(signals.stored()) != 0 {
		t.Error("a signal was recorded from a candle that had not closed")
	}
}

// TestABarTheStrategyHasSeenIsNotFedTwice.
//
// A reconnect backfills the bars missed while disconnected and the stream then
// delivers some of them again. Feeding one twice would double-count it in
// every indicator, and every value afterwards would be subtly wrong with
// nothing to show for it.
func TestABarTheStrategyHasSeenIsNotFedTwice(t *testing.T) {
	history := series(warmBars())

	evaluator, signals := buildEvaluator(t, history, &alwaysLong{}, nil)
	if err := evaluator.Warmup(context.Background()); err != nil {
		t.Fatalf("Warmup() returned error: %v", err)
	}

	live := evalBar(len(history), 28000)
	if _, _, err := evaluator.OnClosedCandle(context.Background(), live); err != nil {
		t.Fatalf("OnClosedCandle() returned error: %v", err)
	}

	// The same bar again, and then one older than it.
	for name, repeat := range map[string]models.Candle{
		"the same bar": live,
		"an older bar": evalBar(len(history)-1, 27990),
	} {
		_, ok, err := evaluator.OnClosedCandle(context.Background(), repeat)
		if err != nil {
			t.Fatalf("%s: OnClosedCandle() returned error: %v", name, err)
		}
		if ok {
			t.Errorf("%s produced a second signal", name)
		}
	}

	if got := signals.stored(); len(got) != 1 {
		t.Errorf("stored %d signals, want 1", len(got))
	}
}

// TestABarOfAnotherTimeframeIsIgnored. The collector stores every configured
// timeframe and hands all of them over; the strategy decides on one.
func TestABarOfAnotherTimeframeIsIgnored(t *testing.T) {
	history := series(warmBars())

	evaluator, signals := buildEvaluator(t, history, &alwaysLong{}, nil)
	if err := evaluator.Warmup(context.Background()); err != nil {
		t.Fatalf("Warmup() returned error: %v", err)
	}

	other := evalBar(len(history), 28000)
	other.Timeframe = constants.Timeframe1m

	if _, ok, _ := evaluator.OnClosedCandle(context.Background(), other); ok {
		t.Error("a 1m bar produced a signal from a 4h strategy")
	}
	if len(signals.stored()) != 0 {
		t.Error("a bar of another timeframe reached the strategy")
	}
}

// TestADuplicateIsNotAnError.
//
// The unique constraint is what stops the owner being notified twice for one
// candle, and hitting it on a restart mid-delivery is the constraint working
// rather than a failure to report.
func TestADuplicateIsNotAnError(t *testing.T) {
	history := series(warmBars())

	evaluator, signals := buildEvaluator(t, history, &alwaysLong{}, nil)
	signals.duplicate = true
	if err := evaluator.Warmup(context.Background()); err != nil {
		t.Fatalf("Warmup() returned error: %v", err)
	}

	_, ok, err := evaluator.OnClosedCandle(context.Background(), evalBar(len(history), 28000))
	if err != nil {
		t.Errorf("a duplicate was reported as an error: %v", err)
	}
	if ok {
		t.Error("a duplicate was reported as a recorded signal")
	}
}

// TestAStorageFailureIsReported, because it is not a duplicate and the caller
// has to be able to tell the two apart.
func TestAStorageFailureIsReported(t *testing.T) {
	history := series(warmBars())

	evaluator, signals := buildEvaluator(t, history, &alwaysLong{}, nil)
	signals.err = errors.New("the database is unreachable")
	if err := evaluator.Warmup(context.Background()); err != nil {
		t.Fatalf("Warmup() returned error: %v", err)
	}

	_, ok, err := evaluator.OnClosedCandle(context.Background(), evalBar(len(history), 28000))
	if err == nil {
		t.Fatal("a storage failure was swallowed")
	}
	if ok {
		t.Error("a failed write was reported as a recorded signal")
	}
}

// TestTheStrategyIsShownAFlatPosition.
//
// This path holds no position: it records what the strategy said. A strategy
// shown a held position would suppress entries and go silent forever, waiting
// for an exit that no order was ever placed to need.
func TestTheStrategyIsShownAFlatPosition(t *testing.T) {
	history := series(warmBars())

	watcher := &positionWatcher{}
	evaluator, _ := buildEvaluator(t, history, watcher, nil)
	if err := evaluator.Warmup(context.Background()); err != nil {
		t.Fatalf("Warmup() returned error: %v", err)
	}
	if _, _, err := evaluator.OnClosedCandle(context.Background(), evalBar(len(history), 28000)); err != nil {
		t.Fatalf("OnClosedCandle() returned error: %v", err)
	}

	if watcher.seen == 0 {
		t.Fatal("the strategy was never called")
	}
	if watcher.held {
		t.Error("the strategy was shown a held position by a path that holds none")
	}
}

// positionWatcher records whether it was ever shown a position.
type positionWatcher struct {
	seen int
	held bool
}

func (p *positionWatcher) OnBar(bar strategy.BarContext) []strategy.Intent {
	p.seen++
	if bar.Position.IsOpen() {
		p.held = true
	}
	return nil
}
func (p *positionWatcher) WarmupPeriod() int { return 0 }
func (p *positionWatcher) Name() string      { return "position_watcher" }
func (p *positionWatcher) Version() string   { return "v1" }

// TestARestartDoesNotAlertTwiceForOneBar.
//
// The collector goes down and comes back. Warm-up replays what is stored —
// which includes the bar it was killed on, because the candle is written
// before the signal — and the stream then redelivers that bar.
//
// This asserts the outcome the owner cares about, one alert, and deliberately
// not which of the two defences produced it: the evaluator refuses a bar it
// has already seen, and the unique key refuses a second row if it does not.
// Each is pinned on its own elsewhere — TestABarTheStrategyHasSeenIsNotFedTwice
// and TestTheConstraintStopsASecondSignalWhenTheGuardCannot. What belongs here
// is that the whole restart produces one alert with both in place.
func TestARestartDoesNotAlertTwiceForOneBar(t *testing.T) {
	history := series(warmBars())
	live := evalBar(len(history), 28000)

	// The tables survive the restart; nothing else does.
	signals := &recordingSignals{}

	before, _ := buildEvaluatorOn(t, signals, history, &alwaysLong{}, nil)
	if err := before.Warmup(context.Background()); err != nil {
		t.Fatalf("Warmup() returned error: %v", err)
	}
	if _, ok, err := before.OnClosedCandle(context.Background(), live); err != nil || !ok {
		t.Fatalf("the first run recorded nothing: %v, %v", ok, err)
	}

	// A new process: fresh indicators, fresh strategy, the same stored candles
	// — now including the bar it died on.
	after, _ := buildEvaluatorOn(t, signals, append(history, live), &alwaysLong{}, nil)
	if err := after.Warmup(context.Background()); err != nil {
		t.Fatalf("Warmup() after the restart returned error: %v", err)
	}

	recorded, ok, err := after.OnClosedCandle(context.Background(), live)
	if err != nil {
		t.Fatalf("the redelivered bar was reported as an error: %v", err)
	}
	if ok {
		t.Errorf("the restart recorded a second signal for one bar: %+v", recorded)
	}
	if got := signals.stored(); len(got) != 1 {
		t.Errorf("one bar produced %d signals across a restart, want 1", len(got))
	}
}

// TestTheConstraintStopsASecondSignalWhenTheGuardCannot.
//
// The evaluator's own last-bar check is the first line of defence, and it
// covers the ordinary restart. It cannot cover everything: two collectors
// running at once, or a warm-up whose read of the series lagged the bar the
// stream then delivered, both put a bar in front of an evaluator that has
// never seen it and whose strategy — being deterministic — decides exactly
// what the other one decided.
//
// This is that case with the guard deliberately given nothing to work with:
// the second evaluator warms up on history that stops short of the bar. Only
// the unique key is left, and one alert is what the owner must get.
func TestTheConstraintStopsASecondSignalWhenTheGuardCannot(t *testing.T) {
	history := series(warmBars())
	live := evalBar(len(history), 28000)

	signals := &recordingSignals{}

	first, _ := buildEvaluatorOn(t, signals, history, &alwaysLong{}, nil)
	if err := first.Warmup(context.Background()); err != nil {
		t.Fatalf("Warmup() returned error: %v", err)
	}
	if _, ok, err := first.OnClosedCandle(context.Background(), live); err != nil || !ok {
		t.Fatalf("the first evaluator recorded nothing: %v, %v", ok, err)
	}

	// Warmed on the same history, so the bar is new to it and its guard has
	// nothing to say.
	second, _ := buildEvaluatorOn(t, signals, history, &alwaysLong{}, nil)
	if err := second.Warmup(context.Background()); err != nil {
		t.Fatalf("the second Warmup() returned error: %v", err)
	}

	recorded, ok, err := second.OnClosedCandle(context.Background(), live)
	if err != nil {
		t.Fatalf("the duplicate was reported as an error: %v", err)
	}
	if ok {
		t.Errorf("a second signal was recorded for one bar: %+v", recorded)
	}
	if got := signals.stored(); len(got) != 1 {
		t.Errorf("one bar produced %d signals, want 1", len(got))
	}
}

func (r *recordingSignals) ListSignals(
	context.Context, signal.ListParams,
) ([]models.Signal, int64, error) {
	return nil, 0, errors.New("not used")
}
