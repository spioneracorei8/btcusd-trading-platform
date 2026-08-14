package usecase_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/backtest"
	_backtest_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/backtest/usecase"
	_indicator_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/indicator/usecase"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/strategy"
)

// testSetConfig uses the shortest periods the indicators accept, so a test
// series does not have to be thousands of bars long before anything is
// evaluated. The warm-up rule itself is phase 03's and is tested there.
func testSetConfig() _indicator_us.SetConfig {
	return _indicator_us.SetConfig{EMAPeriod: 2, RSIPeriod: 2, ATRPeriod: 2}
}

// warmupBars is the indicator set's warm-up.
//
// Note the inclusive semantics phase 03 chose: Ready() is count >= period, so
// the bar that first emits a value is itself the period-th one. Only
// period-1 bars are therefore skipped, and the first scored bar is at index
// period-1. Pinning that here is the point — the seam is easy to be off by
// one at, and being off by one would shift every fill in every run.
func warmupBars(t *testing.T) int {
	t.Helper()

	set, err := _indicator_us.NewSet(testSetConfig())
	if err != nil {
		t.Fatalf("build indicator set: %v", err)
	}
	return set.WarmupPeriod()
}

// firstScoredIndex is the index of the first bar the engine evaluates when the
// range starts at the beginning of the series.
func firstScoredIndex(t *testing.T) int { return warmupBars(t) - 1 }

func newEngine(candles *fakeCandles, gaps *fakeGaps) backtest.BacktestUsecase {
	if gaps == nil {
		gaps = &fakeGaps{}
	}
	return _backtest_us.NewBacktestUsecaseImpl(silentLogger(), candles, gaps, testSetConfig())
}

// scoredParams builds a run whose range starts after the warm-up, so every
// bar in the requested range is evaluated and the arithmetic below is exact.
func scoredParams(t *testing.T, series []models.Candle, strat strategy.Strategy) backtest.RunParams {
	t.Helper()

	first := firstScoredIndex(t)
	if len(series) <= first {
		t.Fatalf("series of %d bars is too short for a %d bar warm-up", len(series), warmupBars(t))
	}

	params := runParams(series, strat)
	params.From = series[first].OpenTime
	return params
}

// runEngine runs and fails the test on error.
func runEngine(t *testing.T, candles *fakeCandles, gaps *fakeGaps, params backtest.RunParams) backtest.Result {
	t.Helper()

	result, err := newEngine(candles, gaps).Run(context.Background(), params)
	if err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}
	return result
}

// ---------------------------------------------------------------------------
// The fixture strategies, whose answers are known in advance.
// ---------------------------------------------------------------------------

// TestAlwaysFlatTradesNothing catches phantom fills. A strategy that never
// asks for anything must produce no trades, no costs, and an equity curve
// that never moves off its starting value.
func TestAlwaysFlatTradesNothing(t *testing.T) {
	series := flatSeries(60, "100")
	candles := &fakeCandles{series: series}

	result := runEngine(t, candles, nil, scoredParams(t, series, alwaysFlat{}))

	if len(result.Trades) != 0 {
		t.Fatalf("a strategy that never trades produced %d trades", len(result.Trades))
	}
	for _, point := range result.Equity {
		if !point.Equity.Equal(initialEquity) {
			t.Fatalf("equity moved to %s at %s without a trade",
				point.Equity, point.OpenTime.Format(time.RFC3339))
		}
	}
	if int(result.BarsEvaluated) != len(result.Equity) {
		t.Errorf("evaluated %d bars but the curve has %d points",
			result.BarsEvaluated, len(result.Equity))
	}
}

// TestGuaranteedLossReturnsExactlyTheCosts is the strongest test of the cost
// model. On a flat series the price contributes nothing, so whatever the
// account lost is precisely what it paid to trade — no more, and crucially no
// less.
func TestGuaranteedLossReturnsExactlyTheCosts(t *testing.T) {
	series := flatSeries(40, "100")
	candles := &fakeCandles{series: series}

	result := runEngine(t, candles, nil, scoredParams(t, series, guaranteedLoss{}))

	if len(result.Trades) == 0 {
		t.Fatal("a strategy entering and exiting every bar produced no trades")
	}

	totalCosts := decimal.Zero
	for _, trade := range result.Trades {
		// Costs must be the whole cost of trading, fees and slippage both. If
		// slippage were left inside the price move instead, the account would
		// lose more than the report ever admitted to.
		if !trade.Costs.Equal(trade.Fees.Add(trade.Slippage)) {
			t.Errorf("trade costs %s do not equal fees %s plus slippage %s",
				trade.Costs, trade.Fees, trade.Slippage)
		}
		totalCosts = totalCosts.Add(trade.Costs)
		if !trade.NetPnL.IsNegative() {
			t.Errorf("trade at %s has net PnL %s, want a loss: it paid costs and the price never moved",
				trade.EntryTime.Format(time.RFC3339), trade.NetPnL)
		}
	}

	finalEquity := result.Equity[len(result.Equity)-1].Equity
	lost := initialEquity.Sub(finalEquity)

	// The account lost the costs and nothing besides. Comparing the two
	// independently accumulated figures is what makes this a test of the
	// accounting rather than a restatement of it.
	if !lost.Equal(totalCosts) {
		t.Errorf("account lost %s but trades recorded %s in costs; the difference is unexplained",
			lost, totalCosts)
	}
}

// TestBuyAndHoldMatchesAHandComputedFigure checks the arithmetic end to end
// against a number worked out independently of the engine.
//
// Note where this departs from the phase-04 spec, which describes the
// expected result as the close-to-close change minus one round trip. That
// cannot hold together with §4's rule that a signal fills at the *next* bar's
// open: the entry price is an open, not a close. §4 is the stronger rule —
// it is the one that keeps a backtest reproducible live — so the expected
// figure here is built from the fills the engine's own stated rules produce.
func TestBuyAndHoldMatchesAHandComputedFigure(t *testing.T) {
	// Prices rise by 1 a bar from 100. With open == close on every bar, the
	// price at bar i is exactly 100+i.
	const bars = 40
	series := risingSeries(bars, 100)
	candles := &fakeCandles{series: series}

	params := scoredParams(t, series, buyAndHold{})
	result := runEngine(t, candles, nil, params)

	if len(result.Trades) != 1 {
		t.Fatalf("buy and hold produced %d trades, want exactly 1", len(result.Trades))
	}
	trade := result.Trades[0]

	first := firstScoredIndex(t)
	costs := testCosts()
	slip := costs.SlippageAmount()
	hundred := decimal.NewFromInt(100)
	feeRate := costs.FeeTakerPct.Div(hundred)

	// The strategy sees its first bar at index `first` and asks to enter.
	// The fill lands on the next bar's open, index first+1, plus slippage.
	entryReference := decimal.NewFromInt(int64(100 + first + 1))
	wantEntry := entryReference.Add(slip)
	if !trade.EntryPrice.Equal(wantEntry) {
		t.Errorf("entry filled at %s, want %s (open of the bar after the signal, plus %s slippage)",
			trade.EntryPrice, wantEntry, slip)
	}

	// The position is still open when the range ends, so the engine
	// liquidates at the final bar's close, less slippage on the way out.
	exitReference := decimal.NewFromInt(int64(100 + bars - 1))
	wantExit := exitReference.Sub(slip)
	if !trade.ExitPrice.Equal(wantExit) {
		t.Errorf("exit filled at %s, want %s (last close, less %s slippage)",
			trade.ExitPrice, wantExit, slip)
	}
	if trade.ExitReason != backtest.ExitEndOfRun {
		t.Errorf("exit reason is %q, want %q", trade.ExitReason, backtest.ExitEndOfRun)
	}

	// Size is the whole account divided by the entry price and the fee it
	// must also cover, which is what "all-in" means when entering costs money.
	wantSize := initialEquity.Div(wantEntry.Mul(decimal.NewFromInt(1).Add(feeRate)))
	if !trade.Size.Equal(wantSize) {
		t.Errorf("size is %s, want %s", trade.Size, wantSize)
	}

	// One round trip: a fee each way, plus one tick of slippage each way.
	wantFees := wantEntry.Mul(wantSize).Mul(costs.FeeTakerPct).Div(hundred).
		Add(wantExit.Mul(wantSize).Mul(costs.FeeTakerPct).Div(hundred))
	wantSlippage := slip.Mul(wantSize).Mul(decimal.NewFromInt(2))
	wantCosts := wantFees.Add(wantSlippage)

	if !trade.Fees.Equal(wantFees) {
		t.Errorf("fees are %s, want %s (one each way)", trade.Fees, wantFees)
	}
	if !trade.Slippage.Equal(wantSlippage) {
		t.Errorf("slippage is %s, want %s (one tick each way)", trade.Slippage, wantSlippage)
	}
	if !trade.Costs.Equal(wantCosts) {
		t.Errorf("costs are %s, want %s (exactly one round trip)", trade.Costs, wantCosts)
	}

	// Gross is the move between the unslipped prices: what the market did,
	// with no cost of any kind folded into it.
	wantGross := exitReference.Sub(entryReference).Mul(wantSize)
	wantNet := wantGross.Sub(wantCosts)
	if !trade.GrossPnL.Equal(wantGross) {
		t.Errorf("gross PnL is %s, want %s", trade.GrossPnL, wantGross)
	}
	if !trade.NetPnL.Equal(wantNet) {
		t.Errorf("net PnL is %s, want %s", trade.NetPnL, wantNet)
	}

	finalEquity := result.Equity[len(result.Equity)-1].Equity
	if !finalEquity.Equal(initialEquity.Add(wantNet)) {
		t.Errorf("final equity is %s, want %s", finalEquity, initialEquity.Add(wantNet))
	}
}

// TestAlternatingPaysOneRoundTripPerTrade catches cost accounting that drifts
// with the number of trades — a fee charged once instead of on both sides, or
// slippage applied to the entry only.
func TestAlternatingPaysOneRoundTripPerTrade(t *testing.T) {
	series := flatSeries(60, "100")
	candles := &fakeCandles{series: series}

	strat := &alternating{everyN: 4}
	result := runEngine(t, candles, nil, scoredParams(t, series, strat))

	if len(result.Trades) == 0 {
		t.Fatal("the alternating strategy produced no trades")
	}

	costs := testCosts()
	hundred := decimal.NewFromInt(100)

	for _, trade := range result.Trades {
		entryFee := trade.EntryPrice.Mul(trade.Size).Mul(costs.FeeTakerPct).Div(hundred)
		exitFee := trade.ExitPrice.Mul(trade.Size).Mul(costs.FeeTakerPct).Div(hundred)
		want := entryFee.Add(exitFee)

		if !trade.Fees.Equal(want) {
			t.Errorf("trade entered at %s paid %s in fees, want %s (both sides)",
				trade.EntryTime.Format(time.RFC3339), trade.Fees, want)
		}
		if !trade.Costs.Equal(trade.Fees.Add(trade.Slippage)) {
			t.Errorf("costs %s do not equal fees %s plus slippage %s",
				trade.Costs, trade.Fees, trade.Slippage)
		}

		// On a flat series, slippage alone means the exit is two ticks below
		// the entry: one against on the way in, one against on the way out.
		wantSpread := costs.SlippageAmount().Mul(decimal.NewFromInt(2))
		if !trade.EntryPrice.Sub(trade.ExitPrice).Equal(wantSpread) {
			t.Errorf("entry %s and exit %s differ by %s, want %s: slippage must work against both sides",
				trade.EntryPrice, trade.ExitPrice, trade.EntryPrice.Sub(trade.ExitPrice), wantSpread)
		}
	}
}

// ---------------------------------------------------------------------------
// Execution rules.
// ---------------------------------------------------------------------------

// TestFillHappensAtTheNextBarsOpen is the rule §4 calls the most common source
// of backtests that cannot be reproduced live.
//
// The series is built so the two candidate prices are far apart and cannot be
// confused: the signal bar closes at 500 and the next bar opens at 900. A fill
// anywhere near 500 means the engine used a price it could not have known at
// the moment of the decision.
func TestFillHappensAtTheNextBarsOpen(t *testing.T) {
	first := firstScoredIndex(t)

	// The signal bar is the first one the strategy is shown. It closes at 500
	// and the bar after it opens at 900, so the two candidate fill prices are
	// far enough apart that no rounding could confuse them.
	series := flatSeries(first+1, "100")
	signalBar := seriesStart.Add(time.Duration(first) * time.Minute)
	series[first] = bar(signalBar, "100", "500", "100", "500")
	series = append(series,
		bar(signalBar.Add(time.Minute), "900", "950", "880", "920"),
		bar(signalBar.Add(2*time.Minute), "920", "930", "910", "915"),
	)

	candles := &fakeCandles{series: series}
	params := scoredParams(t, series, buyAndHold{})
	result := runEngine(t, candles, nil, params)

	if len(result.Trades) != 1 {
		t.Fatalf("produced %d trades, want 1", len(result.Trades))
	}

	wantEntry := decimal.RequireFromString("900").Add(testCosts().SlippageAmount())
	if !result.Trades[0].EntryPrice.Equal(wantEntry) {
		t.Fatalf("entry filled at %s, want %s: a signal on the close of bar t fills at the open of t+1, never on t itself",
			result.Trades[0].EntryPrice, wantEntry)
	}
}

// TestStopWinsWhenBothLevelsAreReachable pins the pessimistic assumption.
//
// The bar spans both levels, and a 1m candle says nothing about which was
// touched first. Taking the target would be the optimistic reading and is
// exactly how a backtest inflates itself on the bars where the data cannot
// argue back.
func TestStopWinsWhenBothLevelsAreReachable(t *testing.T) {
	warmup := warmupBars(t)

	series := flatSeries(warmup+2, "100")
	// The bar after the entry fills spans 80..120, reaching both levels.
	wideBar := seriesStart.Add(time.Duration(warmup+2) * time.Minute) //nolint:gocritic // index is a bar count
	series = append(series,
		bar(wideBar, "100", "120", "80", "100"),
		bar(wideBar.Add(time.Minute), "100", "100", "100", "100"),
	)

	candles := &fakeCandles{series: series}
	strat := &longWithLevels{
		stop:   decimal.RequireFromString("90"),
		target: decimal.RequireFromString("110"),
	}
	result := runEngine(t, candles, nil, scoredParams(t, series, strat))

	if len(result.Trades) != 1 {
		t.Fatalf("produced %d trades, want 1", len(result.Trades))
	}
	trade := result.Trades[0]

	if trade.ExitReason != backtest.ExitStop {
		t.Errorf("exit reason is %q, want %q: when both levels are reachable the stop is assumed to fill first",
			trade.ExitReason, backtest.ExitStop)
	}
	if !trade.StopAndTargetBothReachable {
		t.Error("the trade is not flagged ambiguous, so the report would not disclose the assumption")
	}
	if result.AmbiguousBars != 1 {
		t.Errorf("AmbiguousBars = %d, want 1", result.AmbiguousBars)
	}
}

// TestTargetIsTakenWhenOnlyItIsReachable is the other half: the pessimistic
// rule must apply only where the bar is genuinely ambiguous, or every winning
// trade would be turned into a loss.
func TestTargetIsTakenWhenOnlyItIsReachable(t *testing.T) {
	warmup := warmupBars(t)

	series := flatSeries(warmup+2, "100")
	upBar := seriesStart.Add(time.Duration(warmup+2) * time.Minute)
	series = append(series,
		bar(upBar, "100", "120", "99", "115"),
		bar(upBar.Add(time.Minute), "115", "115", "115", "115"),
	)

	candles := &fakeCandles{series: series}
	strat := &longWithLevels{
		stop:   decimal.RequireFromString("90"),
		target: decimal.RequireFromString("110"),
	}
	result := runEngine(t, candles, nil, scoredParams(t, series, strat))

	if len(result.Trades) != 1 {
		t.Fatalf("produced %d trades, want 1", len(result.Trades))
	}
	if result.Trades[0].ExitReason != backtest.ExitTarget {
		t.Errorf("exit reason is %q, want %q: only the target was reachable",
			result.Trades[0].ExitReason, backtest.ExitTarget)
	}
	if result.AmbiguousBars != 0 {
		t.Errorf("AmbiguousBars = %d, want 0: this bar was not ambiguous", result.AmbiguousBars)
	}
}

// TestShortIsRejectedOnSpot keeps a spot backtest from reporting fiction. A
// spot account cannot short, so this ends the run rather than warning or
// quietly dropping the intent — a strategy half of whose decisions were
// discarded has not been measured.
func TestShortIsRejectedOnSpot(t *testing.T) {
	series := flatSeries(40, "100")
	candles := &fakeCandles{series: series}

	params := scoredParams(t, series, &shortOnce{})
	_, err := newEngine(candles, nil).Run(context.Background(), params)

	if !errors.Is(err, constants.ErrShortOnSpot) {
		t.Fatalf("Run() returned %v, want ErrShortOnSpot", err)
	}
}

// TestShortIsAllowedOnFutures proves the rejection is about the market type
// rather than shorts being unimplemented.
func TestShortIsAllowedOnFutures(t *testing.T) {
	series := flatSeries(40, "100")
	candles := &fakeCandles{series: series}

	params := scoredParams(t, series, &shortOnce{})
	params.MarketType = constants.MarketTypeFutures
	for i := range series {
		series[i].MarketType = constants.MarketTypeFutures
	}

	result, err := newEngine(candles, nil).Run(context.Background(), params)
	if err != nil {
		t.Fatalf("Run() returned error on a futures market: %v", err)
	}
	if len(result.Trades) != 1 {
		t.Fatalf("produced %d trades, want 1", len(result.Trades))
	}
	if result.Trades[0].Direction != constants.DirectionShort {
		t.Errorf("direction is %q, want %q", result.Trades[0].Direction, constants.DirectionShort)
	}
}

// TestEquityCurveHasOnePointPerEvaluatedBar is the invariant drawdown depends
// on. A curve shorter than the run would compute drawdown over a series that
// skipped exactly the bars where the account was moving.
func TestEquityCurveHasOnePointPerEvaluatedBar(t *testing.T) {
	series := risingSeries(80, 100)
	candles := &fakeCandles{series: series}

	strat := &alternating{everyN: 3}
	result := runEngine(t, candles, nil, scoredParams(t, series, strat))

	if int64(len(result.Equity)) != result.BarsEvaluated {
		t.Fatalf("curve has %d points but %d bars were evaluated",
			len(result.Equity), result.BarsEvaluated)
	}
	for i := 1; i < len(result.Equity); i++ {
		if !result.Equity[i].OpenTime.After(result.Equity[i-1].OpenTime) {
			t.Fatalf("curve is not in chronological order at index %d: %s follows %s",
				i, result.Equity[i].OpenTime, result.Equity[i-1].OpenTime)
		}
	}
}

// TestWarmupBarsAreCountedNotScored checks that a range starting before the
// indicators are ready loses those bars to warm-up and says so, rather than
// silently narrowing what was asked for.
func TestWarmupBarsAreCountedNotScored(t *testing.T) {
	series := flatSeries(60, "100")
	candles := &fakeCandles{series: series}

	// From is the very first stored bar, so no history precedes it and the
	// warm-up has to be consumed inside the range.
	params := runParams(series, alwaysFlat{})
	result := runEngine(t, candles, nil, params)

	skipped := int64(firstScoredIndex(t))
	if result.BarsSkippedWarmup != skipped {
		t.Errorf("BarsSkippedWarmup = %d, want %d", result.BarsSkippedWarmup, skipped)
	}
	if result.BarsEvaluated != int64(len(series))-skipped {
		t.Errorf("BarsEvaluated = %d, want %d", result.BarsEvaluated, int64(len(series))-skipped)
	}
	if result.BarsSkippedGap != 0 {
		t.Errorf("BarsSkippedGap = %d, want 0: nothing was missing", result.BarsSkippedGap)
	}
}

// TestStrategyNeverSeesAnUnreadyIndicator guards the seam between phase 03
// and this engine. A NaN reaching a strategy would propagate into whatever it
// computed and turn into a phantom signal.
func TestStrategyNeverSeesAnUnreadyIndicator(t *testing.T) {
	series := risingSeries(60, 100)
	candles := &fakeCandles{series: series}

	strat := &recordingStrategy{}
	runEngine(t, candles, nil, scoredParams(t, series, strat))

	if len(strat.bars) == 0 {
		t.Fatal("the strategy was never called")
	}
	for _, seen := range strat.bars {
		for name, value := range map[string]float64{
			"EMA":  seen.Indicators.EMA,
			"RSI":  seen.Indicators.RSI,
			"ATR":  seen.Indicators.ATR,
			"VWAP": seen.Indicators.VWAP,
		} {
			if value != value { //nolint:gocritic // NaN is only detectable this way
				t.Fatalf("%s was NaN at %s: an unready indicator reached the strategy",
					name, seen.Candle.OpenTime.Format(time.RFC3339))
			}
		}
		if !seen.Indicators.OpenTime.Equal(seen.Candle.OpenTime) {
			t.Fatalf("indicator snapshot is for %s but the bar is %s: the strategy was shown mismatched data",
				seen.Indicators.OpenTime, seen.Candle.OpenTime)
		}
	}
}

// TestRunIsDeterministic runs the same inputs twice and compares everything
// that ends up in a report.
func TestRunIsDeterministic(t *testing.T) {
	series := risingSeries(90, 100)

	first := runEngine(t, &fakeCandles{series: series}, nil,
		scoredParams(t, series, &alternating{everyN: 5}))
	second := runEngine(t, &fakeCandles{series: series}, nil,
		scoredParams(t, series, &alternating{everyN: 5}))

	if len(first.Trades) != len(second.Trades) {
		t.Fatalf("run one produced %d trades and run two %d", len(first.Trades), len(second.Trades))
	}
	for i := range first.Trades {
		a, b := first.Trades[i], second.Trades[i]
		if !a.EntryPrice.Equal(b.EntryPrice) || !a.ExitPrice.Equal(b.ExitPrice) || !a.NetPnL.Equal(b.NetPnL) {
			t.Fatalf("trade %d differs between identical runs: %+v vs %+v", i, a, b)
		}
	}
	if len(first.Equity) != len(second.Equity) {
		t.Fatalf("equity curves differ in length: %d vs %d", len(first.Equity), len(second.Equity))
	}
	for i := range first.Equity {
		if !first.Equity[i].Equity.Equal(second.Equity[i].Equity) {
			t.Fatalf("equity differs at index %d: %s vs %s",
				i, first.Equity[i].Equity, second.Equity[i].Equity)
		}
	}
}

// TestNoPyramiding checks the one-position-at-a-time rule. Averaging a second
// entry into an open position would make the reported entry price a number no
// order ever paid.
func TestNoPyramiding(t *testing.T) {
	series := risingSeries(50, 100)
	candles := &fakeCandles{series: series}

	// Asks to enter on every bar, whether or not something is already held.
	strat := &recordingStrategy{
		onEachBar: func(strategy.BarContext) []strategy.Intent {
			return []strategy.Intent{strategy.EnterLong(decimal.Zero, decimal.Zero, "again")}
		},
	}
	result := runEngine(t, candles, nil, scoredParams(t, series, strat))

	if len(result.Trades) != 1 {
		t.Fatalf("produced %d trades, want 1: repeated entries must not pyramid", len(result.Trades))
	}
	entries := 0
	for _, seen := range strat.bars {
		if seen.Position.IsOpen() {
			entries++
		}
	}
	if entries == 0 {
		t.Error("the strategy never saw an open position, so nothing was actually entered")
	}
}

// TestZeroCostRunStillLosesNothing is a control for the cost tests: with the
// costs configured to zero, the guaranteed-loss strategy breaks exactly even.
// If it did not, something other than the cost model would be eating equity.
func TestZeroCostRunStillLosesNothing(t *testing.T) {
	series := flatSeries(40, "100")
	candles := &fakeCandles{series: series}

	params := scoredParams(t, series, guaranteedLoss{})
	params.Costs = zeroCosts()
	result := runEngine(t, candles, nil, params)

	finalEquity := result.Equity[len(result.Equity)-1].Equity
	if !finalEquity.Equal(initialEquity) {
		t.Errorf("final equity is %s, want %s: with no costs and no price movement nothing should change",
			finalEquity, initialEquity)
	}
}

// enterWithStop asks to enter and attaches its levels in the same call, which
// is the natural way to express "enter with protection".
type enterWithStop struct {
	stop   decimal.Decimal
	target decimal.Decimal
	done   bool
}

func (s *enterWithStop) OnBar(bar strategy.BarContext) []strategy.Intent {
	if s.done || bar.Position.IsOpen() {
		return nil
	}
	s.done = true
	return []strategy.Intent{
		strategy.EnterLong(decimal.Zero, decimal.Zero, "in"),
		strategy.SetStop(s.stop, "protect"),
		strategy.SetTarget(s.target, "take"),
	}
}
func (s *enterWithStop) WarmupPeriod() int { return 0 }
func (s *enterWithStop) Name() string      { return "enter_with_stop" }
func (s *enterWithStop) Version() string   { return "v1" }

// TestLevelsSetAlongsideAnEntryAreArmedWhenItFills is a regression test for a
// silent loss of protection.
//
// A strategy returning EnterLong and SetStop together is asking for a
// protected position. The levels used to be applied only to an already-open
// position, so on the bar the entry was requested there was nothing to attach
// them to, and by the time the entry filled a bar later they had been dropped
// on the floor. The position ran with no stop at all while the strategy — and
// the report — showed nothing wrong.
//
// The series here falls straight through the stop, so an unarmed stop is
// visible as an exit for the wrong reason at the wrong price.
func TestLevelsSetAlongsideAnEntryAreArmedWhenItFills(t *testing.T) {
	first := firstScoredIndex(t)

	series := flatSeries(first+2, "100")
	crashBar := seriesStart.Add(time.Duration(first+2) * time.Minute)
	series = append(series,
		bar(crashBar, "100", "100", "80", "80"),
		bar(crashBar.Add(time.Minute), "80", "80", "80", "80"),
	)

	strat := &enterWithStop{
		stop:   decimal.RequireFromString("95"),
		target: decimal.RequireFromString("105"),
	}
	params := scoredParams(t, series, strat)
	params.To = series[len(series)-1].OpenTime
	result := runEngine(t, &fakeCandles{series: series}, nil, params)

	if len(result.Trades) != 1 {
		t.Fatalf("produced %d trades, want 1", len(result.Trades))
	}
	trade := result.Trades[0]

	if trade.ExitReason != backtest.ExitStop {
		t.Fatalf("exit reason is %q, want %q: the stop attached to the entry never armed",
			trade.ExitReason, backtest.ExitStop)
	}
	// Filled at the stop less slippage, not at the bar's low.
	wantExit := decimal.RequireFromString("95").Sub(testCosts().SlippageAmount())
	if !trade.ExitPrice.Equal(wantExit) {
		t.Errorf("exit filled at %s, want %s", trade.ExitPrice, wantExit)
	}
}

// TestLevelsAreVisibleToTheStrategyOnTheBarAfterTheFill checks the position
// view reflects what was attached, so a strategy can tell whether its own
// protection is in place rather than having to assume it.
func TestLevelsAreVisibleToTheStrategyOnTheBarAfterTheFill(t *testing.T) {
	first := firstScoredIndex(t)
	series := flatSeries(first+6, "100")

	strat := &enterWithStop{
		stop:   decimal.RequireFromString("90"),
		target: decimal.RequireFromString("110"),
	}
	// Wrap it so what the strategy was shown can be inspected afterwards.
	seen := &recordingStrategy{onEachBar: strat.OnBar}
	params := scoredParams(t, series, seen)
	runEngine(t, &fakeCandles{series: series}, nil, params)

	armed := false
	for _, bar := range seen.bars {
		if bar.Position.IsOpen() && !bar.Position.Stop.IsZero() {
			armed = true
			if !bar.Position.Stop.Equal(decimal.RequireFromString("90")) {
				t.Errorf("the strategy sees a stop of %s, want 90", bar.Position.Stop)
			}
			if !bar.Position.Target.Equal(decimal.RequireFromString("110")) {
				t.Errorf("the strategy sees a target of %s, want 110", bar.Position.Target)
			}
			break
		}
	}
	if !armed {
		t.Error("the strategy never saw its own stop attached to the open position")
	}
}

// TestNetPnLReconcilesWithTheEquityCurve is the accounting invariant the whole
// report rests on: every trade's net, summed, must equal what the account
// actually gained or lost.
//
// The two are computed by different paths — trades from gross less costs, the
// curve from the fills themselves — so agreement is evidence rather than
// restatement. A disagreement means the report is describing a different run
// from the one the engine simulated.
func TestNetPnLReconcilesWithTheEquityCurve(t *testing.T) {
	for _, series := range [][]models.Candle{
		flatSeries(80, "100"),
		risingSeries(80, 100),
	} {
		result := runEngine(t, &fakeCandles{series: series}, nil,
			scoredParams(t, series, &alternating{everyN: 4}))

		if len(result.Trades) == 0 {
			t.Fatal("no trades, so nothing was reconciled")
		}

		summed := decimal.Zero
		for _, trade := range result.Trades {
			summed = summed.Add(trade.NetPnL)

			// Each trade must also be internally consistent.
			if !trade.NetPnL.Equal(trade.GrossPnL.Sub(trade.Costs)) {
				t.Errorf("trade net %s does not equal gross %s less costs %s",
					trade.NetPnL, trade.GrossPnL, trade.Costs)
			}
			if !trade.Costs.Equal(trade.Fees.Add(trade.Slippage)) {
				t.Errorf("trade costs %s do not equal fees %s plus slippage %s",
					trade.Costs, trade.Fees, trade.Slippage)
			}
		}

		final := result.Equity[len(result.Equity)-1].Equity
		if !final.Sub(initialEquity).Equal(summed) {
			t.Errorf("the account moved by %s but the trades sum to %s",
				final.Sub(initialEquity), summed)
		}
	}
}

// TestAlternatingProducesExactlyThePredictedTradeCount is the phase-04 §7
// requirement that the count be predictable rather than merely plausible.
func TestAlternatingProducesExactlyThePredictedTradeCount(t *testing.T) {
	const everyN = 5
	series := flatSeries(103, "100")

	result := runEngine(t, &fakeCandles{series: series}, nil,
		scoredParams(t, series, &alternating{everyN: everyN}))

	// The strategy acts on every everyN-th bar it is shown, alternating enter
	// and exit. A trade is one of each, and a final open position is closed by
	// the engine at the end of the run — so the count is the number of entries.
	scored := int(result.BarsEvaluated)
	actions := scored / everyN
	wantTrades := (actions + 1) / 2

	if len(result.Trades) != wantTrades {
		t.Errorf("produced %d trades over %d scored bars acting every %d, want %d",
			len(result.Trades), scored, everyN, wantTrades)
	}
}
