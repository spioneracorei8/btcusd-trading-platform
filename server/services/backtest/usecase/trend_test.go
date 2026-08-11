package usecase_test

import (
	"context"
	"testing"
	"time"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/backtest"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/trend"
)

// ---------------------------------------------------------------------------
// Stub filters and aligners. The real ones are tested in services/trend; these
// isolate what the *engine* does with a verdict.
// ---------------------------------------------------------------------------

// fixedFilter answers the same thing on every bar.
type fixedFilter struct {
	state trend.TrendState
	calls int
}

func (f *fixedFilter) OnBar(trend.BarContext) trend.TrendState {
	f.calls++
	return f.state
}
func (f *fixedFilter) WarmupPeriod() int { return 0 }
func (f *fixedFilter) Name() string      { return "fixed" }
func (f *fixedFilter) Version() string   { return "v1" }

// permitEverything is the inert filter: ready, and consenting to both sides.
// A run gated on it must be indistinguishable from an unfiltered one.
func permitEverything() *fixedFilter {
	return &fixedFilter{state: trend.TrendState{Bias: constants.BiasBullish, Confidence: 1, Ready: true}}
}

// stubAligner records the instants it was asked about, so the engine's
// promise — one advance per evaluated bar, at the bar's close — can be checked.
type stubAligner struct {
	instants []time.Time
	views    []trend.TimeframeView
	err      error
}

func (a *stubAligner) Advance(_ context.Context, t time.Time) ([]trend.TimeframeView, error) {
	if a.err != nil {
		return nil, a.err
	}
	a.instants = append(a.instants, t)
	return a.views, nil
}
func (a *stubAligner) WarmupBaseBars() int               { return 0 }
func (a *stubAligner) Timeframes() []constants.Timeframe { return nil }

// filteredParams attaches a filter to an otherwise ordinary run.
func filteredParams(base backtest.RunParams, filter trend.Filter, aligner trend.Aligner) backtest.RunParams {
	base.TrendFilter = filter
	base.TrendAligner = aligner
	base.TrendConfig = trend.DefaultConfig()
	return base
}

// TestAnInertFilterChangesNothing is the phase-05 §8 requirement.
//
// A filter that permits everything must leave the run byte-for-byte as it was.
// If it does not, the veto is not the only thing the filter does, and every
// comparison between a filtered and unfiltered run would be measuring that
// side effect as well as the filter.
func TestAnInertFilterChangesNothing(t *testing.T) {
	series := risingSeries(90, 100)

	unfiltered := runEngine(t, &fakeCandles{series: series}, nil,
		scoredParams(t, series, &alternating{everyN: 5}))

	filtered := runEngine(t, &fakeCandles{series: series}, nil,
		filteredParams(scoredParams(t, series, &alternating{everyN: 5}),
			permitEverything(), &stubAligner{}))

	if len(filtered.Trades) != len(unfiltered.Trades) {
		t.Fatalf("filtered run has %d trades, unfiltered %d; the inert filter changed the run",
			len(filtered.Trades), len(unfiltered.Trades))
	}
	for i := range unfiltered.Trades {
		a, b := unfiltered.Trades[i], filtered.Trades[i]
		if !a.EntryTime.Equal(b.EntryTime) || !a.EntryPrice.Equal(b.EntryPrice) ||
			!a.ExitPrice.Equal(b.ExitPrice) || !a.NetPnL.Equal(b.NetPnL) {
			t.Fatalf("trade %d differs:\n unfiltered %+v\n filtered   %+v", i, a, b)
		}
	}
	if len(filtered.Equity) != len(unfiltered.Equity) {
		t.Fatalf("equity curves differ in length: %d vs %d", len(filtered.Equity), len(unfiltered.Equity))
	}
	for i := range unfiltered.Equity {
		if !unfiltered.Equity[i].Equity.Equal(filtered.Equity[i].Equity) {
			t.Fatalf("equity differs at %d: %s vs %s",
				i, unfiltered.Equity[i].Equity, filtered.Equity[i].Equity)
		}
	}

	if filtered.BarsVetoed != 0 {
		t.Errorf("BarsVetoed = %d with a filter that permits everything", filtered.BarsVetoed)
	}
	if filtered.BarsFilterNotReady != 0 {
		t.Errorf("BarsFilterNotReady = %d with a filter that is always ready", filtered.BarsFilterNotReady)
	}
}

// TestAVetoingFilterBlocksEveryEntry is the other extreme, and it must be
// visible rather than merely effective: a run with no trades and no
// bars_vetoed would look like a strategy that never fired.
func TestAVetoingFilterBlocksEveryEntry(t *testing.T) {
	series := risingSeries(90, 100)

	// Ready, but bearish — so a long-only strategy is refused on every bar.
	bearish := &fixedFilter{state: trend.TrendState{
		Bias: constants.BiasBearish, Confidence: 1, Ready: true,
	}}

	result := runEngine(t, &fakeCandles{series: series}, nil,
		filteredParams(scoredParams(t, series, &alternating{everyN: 5}),
			bearish, &stubAligner{}))

	if len(result.Trades) != 0 {
		t.Errorf("a filter refusing every long produced %d trades", len(result.Trades))
	}
	if result.BarsVetoed == 0 {
		t.Error("BarsVetoed is zero; a run blocked to a standstill must say so, " +
			"or it is indistinguishable from a strategy that never fired")
	}
	t.Logf("vetoed %d of %d evaluated bars", result.BarsVetoed, result.BarsEvaluated)
}

// TestNotReadyFilterPermitsNothingAndSaysWhy. §2 requires not-ready to mean
// "no entries permitted", not "no opinion, proceed freely" — and the two must
// be countable apart, because a run that vetoed everything because it was
// still warming up was simply started too early.
func TestNotReadyFilterPermitsNothingAndSaysWhy(t *testing.T) {
	series := risingSeries(90, 100)

	warming := &fixedFilter{state: trend.TrendState{
		Bias: constants.BiasNeutral, Ready: false, NotReadyReason: "warming up",
	}}

	result := runEngine(t, &fakeCandles{series: series}, nil,
		filteredParams(scoredParams(t, series, &alternating{everyN: 5}),
			warming, &stubAligner{}))

	if len(result.Trades) != 0 {
		t.Errorf("a not-ready filter permitted %d trades", len(result.Trades))
	}
	if result.BarsFilterNotReady == 0 {
		t.Error("BarsFilterNotReady is zero while the filter was never ready")
	}
	if result.BarsFilterNotReady != result.BarsEvaluated {
		t.Errorf("BarsFilterNotReady = %d over %d evaluated bars, want every bar",
			result.BarsFilterNotReady, result.BarsEvaluated)
	}
}

// TestExitsAreNeverVetoed. A filter that could trap a position in the market
// would be making a trading decision. It is only allowed to refuse entries.
func TestExitsAreNeverVetoed(t *testing.T) {
	series := risingSeries(90, 100)

	// Bullish, so longs are permitted and the position opens; the exits that
	// follow must not be blocked by the same verdict.
	bullish := permitEverything()

	result := runEngine(t, &fakeCandles{series: series}, nil,
		filteredParams(scoredParams(t, series, &alternating{everyN: 5}),
			bullish, &stubAligner{}))

	if len(result.Trades) == 0 {
		t.Fatal("no trades at all; the setup did not exercise an exit")
	}
	for _, trade := range result.Trades {
		if trade.ExitReason == backtest.ExitEndOfRun {
			continue
		}
		if trade.ExitReason != backtest.ExitStrategy {
			t.Errorf("trade exited by %q; the strategy's own exits should have run",
				trade.ExitReason)
		}
	}
}

// TestTheFilterIsAskedAtTheBarsCloseTime pins the alignment contract at the
// engine boundary.
//
// The decision instant is the base bar's close_time — the moment the strategy
// decided. Asking at the bar's *open* would hand the filter a higher-timeframe
// reading one base bar too old, and asking later would be look-ahead.
func TestTheFilterIsAskedAtTheBarsCloseTime(t *testing.T) {
	series := risingSeries(40, 100)
	aligner := &stubAligner{}

	result := runEngine(t, &fakeCandles{series: series}, nil,
		filteredParams(scoredParams(t, series, alwaysFlat{}), permitEverything(), aligner))

	if int64(len(aligner.instants)) != result.BarsEvaluated {
		t.Fatalf("the aligner was advanced %d times over %d evaluated bars",
			len(aligner.instants), result.BarsEvaluated)
	}

	// Every instant must be a bar's close, in order, with no repeats.
	closes := map[time.Time]bool{}
	for _, c := range series {
		closes[c.CloseTime] = true
	}
	for i, at := range aligner.instants {
		if !closes[at] {
			t.Fatalf("advance %d used %s, which is not any bar's close time",
				i, at.Format(time.RFC3339))
		}
		if i > 0 && !at.After(aligner.instants[i-1]) {
			t.Fatalf("advance %d went to %s, not after %s",
				i, at.Format(time.RFC3339), aligner.instants[i-1].Format(time.RFC3339))
		}
	}
}

// TestAlignerFailureStopsTheRun. A filter that cannot read its higher
// timeframes has no verdict, and continuing would silently run unfiltered —
// producing a report that claims to be filtered and is not.
func TestAlignerFailureStopsTheRun(t *testing.T) {
	series := risingSeries(60, 100)
	broken := &stubAligner{err: context.DeadlineExceeded}

	params := filteredParams(scoredParams(t, series, &alternating{everyN: 3}),
		permitEverything(), broken)

	if _, err := newEngine(&fakeCandles{series: series}, nil).Run(context.Background(), params); err == nil {
		t.Fatal("Run() succeeded with an aligner that could not read anything")
	}
}

// TestFilterWithoutAlignerIsRejected. The two are useless apart, and a filter
// with nothing to read would report a not-ready verdict on every bar — which
// looks like a warm-up problem rather than the wiring mistake it is.
func TestFilterWithoutAlignerIsRejected(t *testing.T) {
	series := risingSeries(40, 100)

	params := scoredParams(t, series, alwaysFlat{})
	params.TrendFilter = permitEverything()
	params.TrendAligner = nil

	if _, err := newEngine(&fakeCandles{series: series}, nil).Run(context.Background(), params); err == nil {
		t.Fatal("Run() accepted a trend filter with no aligner")
	}
}

// TestFilterMetadataReachesTheResult, so a stored report says what filtered it
// rather than only that something did.
func TestFilterMetadataReachesTheResult(t *testing.T) {
	series := risingSeries(40, 100)

	result := runEngine(t, &fakeCandles{series: series}, nil,
		filteredParams(scoredParams(t, series, alwaysFlat{}), permitEverything(), &stubAligner{}))

	if result.TrendFilterName != "fixed" || result.TrendFilterVersion != "v1" {
		t.Errorf("filter recorded as %q %q, want \"fixed\" \"v1\"",
			result.TrendFilterName, result.TrendFilterVersion)
	}
	if result.TrendFilterConfig == "" {
		t.Error("the configuration is not recorded; two runs of one version under " +
			"different weights would look identical")
	}

	unfiltered := runEngine(t, &fakeCandles{series: series}, nil,
		scoredParams(t, series, alwaysFlat{}))
	if unfiltered.TrendFilterName != "" {
		t.Errorf("an unfiltered run records a filter name %q", unfiltered.TrendFilterName)
	}
}

// compile-time proof the stubs satisfy the contracts they stand in for.
var (
	_ trend.Filter  = (*fixedFilter)(nil)
	_ trend.Aligner = (*stubAligner)(nil)
)
