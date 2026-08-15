package usecase_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/backtest"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/strategy"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/trend"
)

// watchesHigher is a strategy that reads higher timeframes and records what it
// was shown, so the engine's promises about them can be checked from inside.
type watchesHigher struct {
	timeframes []constants.Timeframe

	seen      []*models.TrendSnapshot
	decidedAt []time.Time
}

func (w *watchesHigher) OnBar(bar strategy.BarContext) []strategy.Intent {
	w.seen = append(w.seen, bar.Higher)
	w.decidedAt = append(w.decidedAt, bar.Candle.CloseTime)
	return nil
}
func (w *watchesHigher) WarmupPeriod() int { return 0 }
func (w *watchesHigher) Name() string      { return "watches_higher" }
func (w *watchesHigher) Version() string   { return "v1" }
func (w *watchesHigher) RequiredTimeframes() []constants.Timeframe {
	return w.timeframes
}

// fakeAligner replays prepared readings and records the instants it was asked
// about, so ordering can be asserted without a database.
type fakeAligner struct {
	readings   []trend.TimeframeView
	timeframes []constants.Timeframe
	asked      []time.Time
	err        error
}

func (f *fakeAligner) Advance(_ context.Context, t time.Time) ([]trend.TimeframeView, error) {
	f.asked = append(f.asked, t)
	if f.err != nil {
		return nil, f.err
	}

	// Only readings that had closed by t, which is what the real aligner
	// guarantees and what this fake must not be looser about.
	var visible []trend.TimeframeView
	for _, reading := range f.readings {
		if !reading.CloseTime.After(t) {
			visible = append(visible, reading)
		}
	}
	return visible, nil
}
func (f *fakeAligner) WarmupBaseBars() int               { return 0 }
func (f *fakeAligner) Timeframes() []constants.Timeframe { return f.timeframes }

// TestASingleTimeframeStrategySeesNoHigherReadings is what keeps the addition
// to BarContext from being observable by anything that did not ask for it.
//
// The three phase-06 strategies must find nil there. A strategy that never
// declared RequiredTimeframes cannot read a higher timeframe by accident,
// which is what makes this field safe to have added at all.
func TestASingleTimeframeStrategySeesNoHigherReadings(t *testing.T) {
	series := flatSeries(40, "100")

	recorder := &recordingStrategy{}
	params := scoredParams(t, series, recorder)

	runEngine(t, &fakeCandles{series: series}, nil, params)

	if len(recorder.bars) == 0 {
		t.Fatal("the strategy was never called")
	}
	for i, bar := range recorder.bars {
		if bar.Higher != nil {
			t.Fatalf("bar %d carried a higher-timeframe snapshot to a strategy that asked for none", i)
		}
	}
}

// TestHigherReadingsNeverComeFromAnUnclosedBar is the phase-05 §1 rule,
// arriving through the strategy this time instead of through the filter.
//
// At 14:23 the 1m candle for 14:22 has closed and the 1h candle for 14:00 has
// not. Handing over the completed 14:00-15:00 bar would give the strategy the
// next 37 minutes of price action, and the backtest would look excellent and be
// impossible to reproduce live.
func TestHigherReadingsNeverComeFromAnUnclosedBar(t *testing.T) {
	series := flatSeries(40, "100")

	// One reading that closes in the middle of the series and one that closes
	// after the end of it. The second must never be visible.
	midway := series[len(series)/2].CloseTime
	afterTheEnd := series[len(series)-1].CloseTime.Add(time.Hour)

	aligner := &fakeAligner{
		timeframes: []constants.Timeframe{constants.Timeframe15m, constants.Timeframe1h},
		readings: []trend.TimeframeView{
			{Timeframe: constants.Timeframe15m, CloseTime: midway, Ready: true},
			{Timeframe: constants.Timeframe1h, CloseTime: afterTheEnd, Ready: true},
		},
	}

	watcher := &watchesHigher{
		timeframes: []constants.Timeframe{constants.Timeframe15m, constants.Timeframe1h},
	}
	params := scoredParams(t, series, watcher)
	params.StrategyAligner = aligner

	runEngine(t, &fakeCandles{series: series}, nil, params)

	if len(watcher.seen) == 0 {
		t.Fatal("the strategy was never called")
	}

	for i, snapshot := range watcher.seen {
		decidedAt := watcher.decidedAt[i]
		if snapshot == nil {
			t.Fatalf("bar %d: a multi-timeframe strategy was handed no snapshot at all", i)
		}
		for _, reading := range snapshot.Readings {
			if reading.CloseTime.After(decidedAt) {
				t.Fatalf("bar %d deciding at %s was shown a %s reading that closed at %s — "+
					"that bar had not closed yet",
					i, decidedAt.Format(time.RFC3339), reading.Timeframe,
					reading.CloseTime.Format(time.RFC3339))
			}
		}
	}

	// And the aligner was asked about the decision instant, not the bar's open
	// or anything later.
	for i, asked := range aligner.asked {
		if !asked.Equal(watcher.decidedAt[i]) {
			t.Fatalf("bar %d: the aligner was advanced to %s while the decision was made at %s",
				i, asked.Format(time.RFC3339), watcher.decidedAt[i].Format(time.RFC3339))
		}
	}
}

// TestTheAlignerIsAdvancedBeforeTheStrategyDecides.
//
// The trend filter's aligner is advanced *after* OnBar, because a veto applies
// to a decision already made. A strategy's own readings are its input, so they
// have to exist before it is asked anything — otherwise it would decide on the
// previous bar's alignment and the run would be off by one in the direction
// that is hardest to notice.
func TestTheAlignerIsAdvancedBeforeTheStrategyDecides(t *testing.T) {
	series := flatSeries(40, "100")

	aligner := &fakeAligner{timeframes: []constants.Timeframe{constants.Timeframe15m}}
	watcher := &watchesHigher{timeframes: []constants.Timeframe{constants.Timeframe15m}}

	params := scoredParams(t, series, watcher)
	params.StrategyAligner = aligner

	runEngine(t, &fakeCandles{series: series}, nil, params)

	if len(aligner.asked) != len(watcher.decidedAt) {
		t.Fatalf("the aligner was advanced %d times for %d decisions",
			len(aligner.asked), len(watcher.decidedAt))
	}
	if len(aligner.asked) == 0 {
		t.Fatal("the aligner was never advanced")
	}
}

// TestAnAlignerFailureStopsTheRun. A reading that could not be produced is not
// the same as a reading that says nothing, and continuing would silently
// evaluate the strategy on an empty snapshot.
func TestAnAlignerFailureStopsTheRun(t *testing.T) {
	series := flatSeries(40, "100")

	aligner := &fakeAligner{
		timeframes: []constants.Timeframe{constants.Timeframe15m},
		err:        errors.New("cursor exhausted"),
	}
	watcher := &watchesHigher{timeframes: []constants.Timeframe{constants.Timeframe15m}}

	params := scoredParams(t, series, watcher)
	params.StrategyAligner = aligner

	_, err := newEngine(&fakeCandles{series: series}, nil).Run(context.Background(), params)
	if err == nil {
		t.Fatal("the run completed despite the aligner failing")
	}
	if !strings.Contains(err.Error(), "cursor exhausted") {
		t.Errorf("the error does not carry the cause: %v", err)
	}
}

// TestAMultiTimeframeStrategyWithoutAnAlignerIsRefused.
//
// Left to run, it would find nil on every bar, correctly decline to trade on
// evidence it does not have, and report a clean zero-trade run — which reads
// exactly like a strategy whose rules never triggered. An error is better than
// a number that means something else.
func TestAMultiTimeframeStrategyWithoutAnAlignerIsRefused(t *testing.T) {
	series := flatSeries(40, "100")

	watcher := &watchesHigher{timeframes: []constants.Timeframe{constants.Timeframe15m}}
	params := scoredParams(t, series, watcher)
	params.StrategyAligner = nil

	_, err := newEngine(&fakeCandles{series: series}, nil).Run(context.Background(), params)
	if err == nil {
		t.Fatal("a multi-timeframe strategy ran with no aligner and reported a result")
	}
	if !strings.Contains(err.Error(), "aligner") {
		t.Errorf("the error does not say what is missing: %v", err)
	}
}

// TestAContributorAtOrBelowTheBaseIsRefused is the look-ahead hazard arriving
// through the strategy instead of the filter.
//
// A 1m contributor to a 1m run is a bar covering the same period the base bar
// covers; a 1m contributor to a 5m run is worse still. Neither can be
// permitted, and the aligner's own ordering rules would not catch it.
func TestAContributorAtOrBelowTheBaseIsRefused(t *testing.T) {
	series := flatSeries(40, "100")

	for _, timeframe := range []constants.Timeframe{constants.Timeframe1m, constants.Timeframe5m} {
		watcher := &watchesHigher{timeframes: []constants.Timeframe{timeframe}}
		params := scoredParams(t, series, watcher)
		params.Timeframe = constants.Timeframe5m
		params.StrategyAligner = &fakeAligner{timeframes: []constants.Timeframe{timeframe}}

		_, err := newEngine(&fakeCandles{series: series}, nil).Run(context.Background(), params)
		if err == nil {
			t.Errorf("a %s contributor was accepted on a 5m base", timeframe)
			continue
		}
		if !strings.Contains(err.Error(), "above") {
			t.Errorf("the %s error does not explain the ordering rule: %v", timeframe, err)
		}
	}
}

// TestTheSnapshotRefusesToReportReadinessItDoesNotHave covers the accessor
// every multi-timeframe strategy gates on.
//
// All, not any: a strategy requiring several timeframes to agree cannot act on
// a subset, because the missing one is the one that would have disagreed as
// often as not.
func TestTheSnapshotRefusesToReportReadinessItDoesNotHave(t *testing.T) {
	wanted := []constants.Timeframe{constants.Timeframe15m, constants.Timeframe1h}

	var nilSnapshot *models.TrendSnapshot
	if nilSnapshot.Ready(wanted...) {
		t.Error("a nil snapshot reported itself ready")
	}
	if _, ok := nilSnapshot.For(constants.Timeframe1h); ok {
		t.Error("a nil snapshot returned a reading")
	}

	partial := &models.TrendSnapshot{Readings: []models.TimeframeReading{
		{Timeframe: constants.Timeframe15m, Ready: true},
	}}
	if partial.Ready(wanted...) {
		t.Error("a snapshot missing 1h reported itself ready for both")
	}

	cold := &models.TrendSnapshot{Readings: []models.TimeframeReading{
		{Timeframe: constants.Timeframe15m, Ready: true},
		{Timeframe: constants.Timeframe1h, Ready: false},
	}}
	if cold.Ready(wanted...) {
		t.Error("a snapshot with a cold 1h reported itself ready; a warming timeframe is not consent")
	}

	warm := &models.TrendSnapshot{Readings: []models.TimeframeReading{
		{Timeframe: constants.Timeframe15m, Ready: true},
		{Timeframe: constants.Timeframe1h, Ready: true},
	}}
	if !warm.Ready(wanted...) {
		t.Error("a snapshot with both timeframes warm reported itself not ready")
	}

	// Asking about nothing is not readiness either. It is the shape a bug
	// takes when a strategy's timeframe list comes back empty.
	if warm.Ready() {
		t.Error("a snapshot reported ready for an empty list of timeframes")
	}
}

// TestRunParamsCarriesTheTwoAlignersSeparately.
//
// They are configured differently and advanced at different points in the bar.
// Collapsing them into one field would mean whichever was set first silently
// decided the other's inputs — and --no-trend-filter, which is how a
// multi-timeframe strategy is normally run, would leave the strategy blind.
func TestRunParamsCarriesTheTwoAlignersSeparately(t *testing.T) {
	var params backtest.RunParams

	params.TrendAligner = &fakeAligner{timeframes: []constants.Timeframe{constants.Timeframe1h}}
	params.StrategyAligner = &fakeAligner{timeframes: []constants.Timeframe{constants.Timeframe4h}}

	if params.TrendAligner.Timeframes()[0] == params.StrategyAligner.Timeframes()[0] {
		t.Fatal("the two aligners are the same object")
	}
	if params.Filtered() {
		t.Error("a strategy aligner alone made the run report itself as filtered")
	}
}
