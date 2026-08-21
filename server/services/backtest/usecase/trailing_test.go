package usecase_test

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/backtest"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/strategy"
)

// holdLong opens one long with a fixed stop and no target, then holds. The
// engine's trail is then the only thing that can close it.
type holdLong struct {
	stop decimal.Decimal
	done bool
}

func (h *holdLong) OnBar(bar strategy.BarContext) []strategy.Intent {
	if h.done || bar.Position.IsOpen() {
		return nil
	}
	h.done = true
	return []strategy.Intent{strategy.EnterLong(h.stop, decimal.Zero, "hold")}
}
func (h *holdLong) WarmupPeriod() int { return 0 }
func (h *holdLong) Name() string      { return "hold_long" }
func (h *holdLong) Version() string   { return "v1" }

// climbThenFall rises for `up` bars and then falls away, which is the shape a
// trailing stop exists for: a winner that gives some back.
func climbThenFall(up, down int, start, step int64) []models.Candle {
	series := make([]models.Candle, 0, up+down)
	price := start

	for i := range up + down {
		if i < up {
			price += step
		} else {
			price -= step
		}
		at := seriesStart.Add(time.Duration(len(series)) * time.Minute)
		value := decimal.NewFromInt(price).String()
		series = append(series, bar(at, value, value, value, value))
	}
	return series
}

// watchStop opens a long and records the stop it is shown on every bar.
//
// The strategy sees the live stop through its position view, which is the only
// place the trail's movement is observable bar by bar — the trade record keeps
// just the final level.
type watchStop struct {
	stop  decimal.Decimal
	done  bool
	stops []decimal.Decimal
}

func (w *watchStop) OnBar(bar strategy.BarContext) []strategy.Intent {
	if bar.Position.IsOpen() {
		w.stops = append(w.stops, bar.Position.Stop)
		return nil
	}
	if w.done {
		return nil
	}
	w.done = true
	return []strategy.Intent{strategy.EnterLong(w.stop, decimal.Zero, "watch")}
}
func (w *watchStop) WarmupPeriod() int { return 0 }
func (w *watchStop) Name() string      { return "watch_stop" }
func (w *watchStop) Version() string   { return "v1" }

// TestATrailingStopNeverMovesAgainstThePosition is the invariant, checked on
// every bar rather than at the end.
//
// A stop that can loosen is not a stop, and the arithmetic that would loosen it
// — a new extreme that is worse than the last, a widened ATR — is exactly the
// arithmetic that appears when a trade starts going wrong. Asserting only the
// final level would miss a stop that moved out and back.
func TestATrailingStopNeverMovesAgainstThePosition(t *testing.T) {
	// Up, down, up again: the middle leg is where a trail that tracked the
	// current price rather than the running extreme would give ground.
	series := append(climbThenFall(40, 25, 27000, 20), climbThenFall(30, 20, 27300, 20)...)
	for i := range series {
		at := seriesStart.Add(time.Duration(i) * time.Minute)
		series[i].OpenTime, series[i].CloseTime = at, at.Add(time.Minute)
	}

	watcher := &watchStop{stop: decimal.NewFromInt(26000)}
	params := scoredParams(t, series, watcher)
	params.Costs = zeroCosts()
	params.Sizing = backtest.AllInSizing()
	params.Exits = backtest.Exits{TrailingATRMult: 1}

	result := runEngine(t, &fakeCandles{series: series}, nil, params)

	if len(watcher.stops) < 5 {
		t.Fatalf("the position was observed on %d bars; too few to measure", len(watcher.stops))
	}
	for i := 1; i < len(watcher.stops); i++ {
		if watcher.stops[i].LessThan(watcher.stops[i-1]) {
			t.Fatalf("the stop moved down from %s to %s at observation %d; a long's stop may only rise",
				watcher.stops[i-1], watcher.stops[i], i)
		}
	}

	// And it did move, or the invariant held vacuously.
	if watcher.stops[len(watcher.stops)-1].Equal(watcher.stops[0]) {
		t.Errorf("the stop never moved from %s; the trail did nothing here", watcher.stops[0])
	}

	if len(result.Trades) == 0 {
		t.Fatal("the trail never closed the position")
	}
	if got := result.Trades[0].ExitReason; got != backtest.ExitTrailingStop {
		t.Errorf("the exit is %q, want %q", got, backtest.ExitTrailingStop)
	}
}

// TestAShortsTrailOnlyEverFalls, the mirror of the same invariant.
func TestAShortsTrailOnlyEverFalls(t *testing.T) {
	series := climbThenFall(0, 60, 28000, 20)
	for i := range series {
		at := seriesStart.Add(time.Duration(i) * time.Minute)
		series[i].OpenTime, series[i].CloseTime = at, at.Add(time.Minute)
	}

	watcher := &watchShortStop{stop: decimal.NewFromInt(29000)}
	params := scoredParams(t, series, watcher)
	params.MarketType = constants.MarketTypeFutures
	params.Costs = zeroCosts()
	params.Sizing = backtest.AllInSizing()
	params.Exits = backtest.Exits{TrailingATRMult: 1}

	runEngine(t, &fakeCandles{series: series}, nil, params)

	if len(watcher.stops) < 5 {
		t.Fatalf("the short was observed on %d bars; too few to measure", len(watcher.stops))
	}
	for i := 1; i < len(watcher.stops); i++ {
		if watcher.stops[i].GreaterThan(watcher.stops[i-1]) {
			t.Fatalf("the stop moved up from %s to %s at observation %d; a short's stop may only fall",
				watcher.stops[i-1], watcher.stops[i], i)
		}
	}
	if watcher.stops[len(watcher.stops)-1].Equal(watcher.stops[0]) {
		t.Errorf("the short's stop never moved from %s", watcher.stops[0])
	}
}

// watchShortStop is watchStop for the other side.
type watchShortStop struct {
	stop  decimal.Decimal
	done  bool
	stops []decimal.Decimal
}

func (w *watchShortStop) OnBar(bar strategy.BarContext) []strategy.Intent {
	if bar.Position.IsOpen() {
		w.stops = append(w.stops, bar.Position.Stop)
		return nil
	}
	if w.done {
		return nil
	}
	w.done = true
	return []strategy.Intent{strategy.EnterShort(w.stop, decimal.Zero, "watch")}
}
func (w *watchShortStop) WarmupPeriod() int { return 0 }
func (w *watchShortStop) Name() string      { return "watch_short_stop" }
func (w *watchShortStop) Version() string   { return "v1" }

// TestTheTrailIsOffByDefault. Every evaluation before this had a fixed stop and
// a fixed target, so a run that mentions no trail must behave exactly as it did.
func TestTheTrailIsOffByDefault(t *testing.T) {
	series := climbThenFall(40, 40, 27000, 20)

	withoutTrail := scoredParams(t, series, &holdLong{stop: decimal.NewFromInt(26000)})
	withoutTrail.Costs = zeroCosts()
	withoutTrail.Sizing = backtest.AllInSizing()

	result := runEngine(t, &fakeCandles{series: series}, nil, withoutTrail)

	for _, trade := range result.Trades {
		if trade.ExitReason == backtest.ExitTrailingStop {
			t.Errorf("a trailing exit happened with no trail configured: %+v", trade)
		}
	}
	if !withoutTrail.Exits.Trailing() {
		return
	}
	t.Error("the zero-value exit configuration reports a trail")
}

// TestTheTrailWaitsForItsActivation. Before the position has moved far enough
// in its favour the fixed stop applies unchanged; arming immediately would
// convert every entry into a trailing exit at roughly the noise level.
func TestTheTrailWaitsForItsActivation(t *testing.T) {
	// Rises 5 bars then falls: never far enough to arm a 10-ATR activation.
	series := climbThenFall(5, 60, 27000, 20)

	params := scoredParams(t, series, &holdLong{stop: decimal.NewFromInt(26900)})
	params.Costs = zeroCosts()
	params.Sizing = backtest.AllInSizing()
	params.Exits = backtest.Exits{TrailingATRMult: 1, TrailingActivateATR: 10}

	result := runEngine(t, &fakeCandles{series: series}, nil, params)
	if len(result.Trades) == 0 {
		t.Fatal("the position never closed")
	}

	trade := result.Trades[0]
	if trade.ExitReason == backtest.ExitTrailingStop {
		t.Errorf("the trail armed and closed the trade before its activation distance was reached: %+v", trade)
	}
	if !trade.StopPrice.Equal(decimal.NewFromInt(26900)) {
		t.Errorf("the stop moved to %s before the trail armed, want the fixed 26900", trade.StopPrice)
	}
}

// TestAnUnarmedTrailStillReportsAFixedStop. The exit reason is what separates
// "the trail did this" from "the fixed stop did this", and the two are counted
// apart precisely so the mechanism can be judged.
func TestAnUnarmedTrailStillReportsAFixedStop(t *testing.T) {
	series := climbThenFall(2, 60, 27000, 20)

	params := scoredParams(t, series, &holdLong{stop: decimal.NewFromInt(26900)})
	params.Costs = zeroCosts()
	params.Sizing = backtest.AllInSizing()
	params.Exits = backtest.Exits{TrailingATRMult: 1, TrailingActivateATR: 50}

	result := runEngine(t, &fakeCandles{series: series}, nil, params)
	if len(result.Trades) == 0 {
		t.Fatal("the position never closed")
	}
	if got := result.Trades[0].ExitReason; got != backtest.ExitStop {
		t.Errorf("the exit is %q, want %q", got, backtest.ExitStop)
	}
}

// TestTrailingExitsPayTheFullCostOfAMarketOrder.
//
// A stop that could only fill at a limit price is a stop that does not fill
// when the market gaps through it — precisely the situation stops exist for.
// The rule is structural for the fixed stop and must be structural here too.
func TestTrailingExitsPayTheFullCostOfAMarketOrder(t *testing.T) {
	series := climbThenFall(40, 40, 27000, 20)

	params := scoredParams(t, series, &holdLong{stop: decimal.NewFromInt(26000)})
	params.Costs = testCosts()
	params.Sizing = backtest.AllInSizing()
	params.Exits = backtest.Exits{TrailingATRMult: 1}
	params.Execution = backtest.Execution{
		EntryOrderType: constants.OrderTypeLimit,
		ExitOrderType:  constants.OrderTypeLimit,
	}

	result := runEngine(t, &fakeCandles{series: series}, nil, params)
	for _, trade := range result.Trades {
		if trade.ExitReason != backtest.ExitTrailingStop {
			continue
		}
		if trade.ExitMaker {
			t.Errorf("a trailing exit filled as a maker under a limit exit model: %+v", trade)
		}
	}
}

// TestTheTrailIsMeasuredFromTheEntryATR, not the current bar's.
//
// A trail whose distance moved with volatility would widen in a volatile hour
// and loosen a stop that was already placed — the one thing a stop may never
// do — and it would do so exactly when the position is most at risk.
func TestTheTrailIsMeasuredFromTheEntryATR(t *testing.T) {
	calm := climbThenFall(40, 40, 27000, 20)

	params := scoredParams(t, calm, &holdLong{stop: decimal.NewFromInt(26000)})
	params.Costs = zeroCosts()
	params.Sizing = backtest.AllInSizing()
	params.Exits = backtest.Exits{TrailingATRMult: 1}

	result := runEngine(t, &fakeCandles{series: calm}, nil, params)
	if len(result.Trades) == 0 {
		t.Fatal("the trail never closed the position")
	}

	trade := result.Trades[0]
	if trade.EntryATR <= 0 {
		t.Fatal("the entry ATR was not recorded, so the trail distance cannot be checked")
	}

	// The stop sits one entry-ATR below the highest price reached, within the
	// tick the fill is measured to.
	peak := decimal.Zero
	for _, candle := range calm {
		if candle.High.GreaterThan(peak) {
			peak = candle.High
		}
	}
	want := peak.Sub(decimal.NewFromFloat(trade.EntryATR))
	if diff := trade.StopPrice.Sub(want).Abs(); diff.GreaterThan(decimal.NewFromInt(25)) {
		t.Errorf("the trail finished at %s, want about %s (peak %s less one ATR of %v)",
			trade.StopPrice, want, peak, trade.EntryATR)
	}
}

// TestAnActivationWithoutATrailIsRefused. A setting that reads as though it
// does something, and does not, is worse than no setting.
func TestAnActivationWithoutATrailIsRefused(t *testing.T) {
	exits := backtest.Exits{TrailingActivateATR: 2}
	if err := exits.Validate(); err == nil {
		t.Error("an activation distance was accepted with nothing to trail")
	}

	for name, bad := range map[string]backtest.Exits{
		"a negative distance":   {TrailingATRMult: -1},
		"a negative activation": {TrailingATRMult: 1, TrailingActivateATR: -1},
	} {
		if err := bad.Validate(); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

// rangedSeries builds bars with a fixed range, so ATR is stable and the trail
// distance is predictable.
func rangedSeries(prices []int64, halfRange int64) []models.Candle {
	series := make([]models.Candle, 0, len(prices))
	for i, price := range prices {
		at := seriesStart.Add(time.Duration(i) * time.Minute)
		series = append(series, bar(at,
			decimal.NewFromInt(price).String(),
			decimal.NewFromInt(price+halfRange).String(),
			decimal.NewFromInt(price-halfRange).String(),
			decimal.NewFromInt(price).String()))
	}
	return series
}

// setsOwnStop enters long and then tightens its own stop above where the trail
// would put it.
type setsOwnStop struct {
	entryStop decimal.Decimal
	tightened decimal.Decimal
	done      bool
	raised    bool
}

func (s *setsOwnStop) OnBar(bar strategy.BarContext) []strategy.Intent {
	if !bar.Position.IsOpen() {
		if s.done {
			return nil
		}
		s.done = true
		return []strategy.Intent{strategy.EnterLong(s.entryStop, decimal.Zero, "enter")}
	}
	if s.raised {
		return nil
	}
	s.raised = true
	return []strategy.Intent{strategy.SetStop(s.tightened, "tighten")}
}
func (s *setsOwnStop) WarmupPeriod() int { return 0 }
func (s *setsOwnStop) Name() string      { return "sets_own_stop" }
func (s *setsOwnStop) Version() string   { return "v1" }

// TestTheTrailNeverLoosensAStopTheStrategyTightened.
//
// This is where the direction check earns its place. While the running extreme
// only improves, the trail rises on its own and never needs to be told not to
// fall. The moment something *else* moves the stop — a strategy tightening it —
// the trail's own level is behind it, and applying that level would give ground
// the position had already taken.
func TestTheTrailNeverLoosensAStopTheStrategyTightened(t *testing.T) {
	prices := make([]int64, 0, 80)
	for i := range 80 {
		prices = append(prices, 27000+int64(i)*10)
	}
	series := rangedSeries(prices, 50)

	watcher := &setsOwnStop{
		entryStop: decimal.NewFromInt(26500),
		// Far above anything the trail will produce: the extreme peaks near
		// 27840 and the trail sits an ATR (about 100) below it.
		tightened: decimal.NewFromInt(27800),
	}

	params := scoredParams(t, series, watcher)
	params.Costs = zeroCosts()
	params.Sizing = backtest.AllInSizing()
	params.Exits = backtest.Exits{TrailingATRMult: 1}

	result := runEngine(t, &fakeCandles{series: series}, nil, params)
	if len(result.Trades) == 0 {
		t.Fatal("the position never closed")
	}

	trade := result.Trades[0]
	if trade.StopPrice.LessThan(decimal.NewFromInt(27800)) {
		t.Errorf("the stop finished at %s, below the %s the strategy tightened it to; "+
			"the trail gave ground the position had already taken",
			trade.StopPrice, decimal.NewFromInt(27800))
	}
}

// TestABarThatWouldBothExtendAndTriggerTheTrailTriggersIt.
//
// A bar records four prices and says nothing about the path between them. When
// its range would both extend the trail and reach the trail as it already
// stands, the two orderings give opposite answers, and only one of them
// flatters the result. The stop triggers, at its pre-extension level, and the
// bar is counted so a result resting largely on the assumption says so.
func TestABarThatWouldBothExtendAndTriggerTheTrailTriggersIt(t *testing.T) {
	prices := make([]int64, 0, 60)
	for i := range 50 {
		prices = append(prices, 27000+int64(i)*10)
	}
	series := rangedSeries(prices, 50)

	// One bar that reaches far higher than anything before it and also dips
	// well below the trail: both things, in an order OHLC cannot report.
	last := prices[len(prices)-1]
	at := seriesStart.Add(time.Duration(len(series)) * time.Minute)
	series = append(series, bar(at,
		decimal.NewFromInt(last).String(),
		decimal.NewFromInt(last+800).String(), // would extend the trail a long way
		decimal.NewFromInt(last-800).String(), // and would trigger it
		decimal.NewFromInt(last).String()))

	// And a quiet tail, so a run that did *not* exit on that bar would go on.
	for i := range 10 {
		at := seriesStart.Add(time.Duration(len(series)) * time.Minute)
		value := decimal.NewFromInt(last + 800).String()
		series = append(series, bar(at, value, value, value, value))
		_ = i
	}

	params := scoredParams(t, series, &holdLong{stop: decimal.NewFromInt(26000)})
	params.Costs = zeroCosts()
	params.Sizing = backtest.AllInSizing()
	params.Exits = backtest.Exits{TrailingATRMult: 1}

	result := runEngine(t, &fakeCandles{series: series}, nil, params)

	if result.TrailAmbiguousBars == 0 {
		t.Error("a bar that would both extend and trigger the trail was not counted; " +
			"the trail is being extended with knowledge of where the bar ended up")
	}
	if len(result.Trades) == 0 {
		t.Fatal("the position never closed")
	}

	trade := result.Trades[0]
	if trade.ExitReason != backtest.ExitTrailingStop {
		t.Fatalf("the exit is %q, want %q", trade.ExitReason, backtest.ExitTrailingStop)
	}

	// At the pre-extension level: the stop cannot have been dragged up by the
	// same bar that took it out.
	if trade.StopPrice.GreaterThan(decimal.NewFromInt(last)) {
		t.Errorf("the position exited at a stop of %s, above the %d the bar opened at; "+
			"the trail extended on the bar that triggered it",
			trade.StopPrice, last)
	}
}
