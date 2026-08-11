package usecase_test

import (
	"math"
	"testing"
	"time"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/trend"
	_trend_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/trend/usecase"
)

func defaultFilter(t *testing.T) trend.Filter {
	t.Helper()

	filter, err := _trend_us.NewFilterImpl(trend.DefaultConfig())
	if err != nil {
		t.Fatalf("NewFilterImpl() returned error: %v", err)
	}
	return filter
}

// view builds a ready contribution whose score is set by how far price sits
// above or below its EMA and VWAP, measured in ATR.
//
// strength is in ATR units: +1 means price one ATR above both, which is a
// clear but not extreme reading.
func view(timeframe constants.Timeframe, strength float64) trend.TimeframeView {
	const price = 27000.0
	const atr = 100.0

	offset := strength * atr
	return trend.TimeframeView{
		Timeframe: timeframe,
		Candle: models.Candle{
			Timeframe: timeframe,
			OpenTime:  alignDay(),
			CloseTime: alignDay().Add(timeframe.Duration()),
			Close:     decimalOf(int64(price)),
		},
		Indicators: models.IndicatorSnapshot{
			OpenTime: alignDay(),
			EMA:      price - offset,
			VWAP:     price - offset,
			ATR:      atr,
			RSI:      50 + strength*10,
		},
		CloseTime: alignDay().Add(timeframe.Duration()),
		Ready:     true,
	}
}

func barWith(views ...trend.TimeframeView) trend.BarContext {
	return trend.BarContext{
		Candle:     models.Candle{OpenTime: alignDay(), CloseTime: alignDay().Add(time.Minute)},
		Indicators: models.IndicatorSnapshot{OpenTime: alignDay()},
		Higher:     views,
	}
}

// TestDeadZoneHoldsNeutralThroughChop is §4's requirement.
//
// A score wandering narrowly either side of zero is what chop looks like.
// Without a dead zone the bias alternates every few bars, and a strategy gated
// on it is permitted and forbidden in turn — which is worse than no filter,
// because it adds cost without adding information.
func TestDeadZoneHoldsNeutralThroughChop(t *testing.T) {
	filter := defaultFilter(t)

	// Twenty bars of a weak reading flipping sign. Each is far too small to
	// clear the dead zone, and the sign alternates on every one.
	flips := 0
	previous := constants.BiasNeutral

	for i := range 20 {
		strength := 0.05
		if i%2 == 1 {
			strength = -0.05
		}

		state := filter.OnBar(barWith(
			view(constants.Timeframe5m, strength),
			view(constants.Timeframe15m, strength),
			view(constants.Timeframe1h, strength),
		))

		if !state.Ready {
			t.Fatalf("bar %d is not ready: %s", i, state.NotReadyReason)
		}
		if state.Bias != constants.BiasNeutral {
			t.Errorf("bar %d reports %s on a reading of %.2f ATR; the dead zone should absorb it",
				i, state.Bias, strength)
		}
		if state.Confidence != 0 {
			t.Errorf("bar %d reports confidence %.4f inside the dead zone, want 0",
				i, state.Confidence)
		}
		if i > 0 && state.Bias != previous {
			flips++
		}
		previous = state.Bias
	}

	if flips != 0 {
		t.Errorf("the bias flipped %d times across noise, which is the oscillation the dead zone exists to stop", flips)
	}
}

// TestStrongAlignmentClearsTheDeadZone is the other half: the band must not be
// so wide that a genuine trend is also absorbed. A filter that never permits
// anything is as useless as one that permits everything.
func TestStrongAlignmentClearsTheDeadZone(t *testing.T) {
	filter := defaultFilter(t)

	up := filter.OnBar(barWith(
		view(constants.Timeframe5m, 1.5),
		view(constants.Timeframe15m, 1.5),
		view(constants.Timeframe1h, 1.5),
	))
	if up.Bias != constants.BiasBullish {
		t.Errorf("three timeframes 1.5 ATR above their EMAs report %s, want bullish", up.Bias)
	}
	if up.Confidence <= trend.DefaultConfig().DeadZone {
		t.Errorf("confidence %.4f does not exceed the dead zone", up.Confidence)
	}
	if !up.Permits(constants.DirectionLong) {
		t.Error("a bullish state does not permit a long")
	}
	if up.Permits(constants.DirectionShort) {
		t.Error("a bullish state permits a short")
	}

	down := filter.OnBar(barWith(
		view(constants.Timeframe5m, -1.5),
		view(constants.Timeframe15m, -1.5),
		view(constants.Timeframe1h, -1.5),
	))
	if down.Bias != constants.BiasBearish {
		t.Errorf("three timeframes 1.5 ATR below their EMAs report %s, want bearish", down.Bias)
	}
	if !down.Permits(constants.DirectionShort) {
		t.Error("a bearish state does not permit a short")
	}
}

// TestHourlyOutweighsFiveMinute is §8's weighting requirement.
//
// The 1h frame is the dominant trend and the strongest veto by design: a
// scalping entry against it is exactly the trade this filter exists to refuse.
// When the two disagree with equal conviction, the hourly must win.
func TestHourlyOutweighsFiveMinute(t *testing.T) {
	filter := defaultFilter(t)

	// 1h strongly down, 5m strongly up, 15m neutral so it casts no vote.
	state := filter.OnBar(barWith(
		view(constants.Timeframe5m, 2),
		view(constants.Timeframe15m, 0),
		view(constants.Timeframe1h, -2),
	))

	if !state.Ready {
		t.Fatalf("state is not ready: %s", state.NotReadyReason)
	}
	if state.Bias != constants.BiasBearish {
		t.Errorf("with 1h down and 5m up the bias is %s, want bearish: 1h carries the heavier weight", state.Bias)
	}

	// And the per-timeframe breakdown must explain it rather than only assert
	// it, so a surprising veto can be read out of a report.
	byTimeframe := map[constants.Timeframe]trend.TimeframeState{}
	for _, per := range state.PerTimeframe {
		byTimeframe[per.Timeframe] = per
	}
	if byTimeframe[constants.Timeframe1h].Score >= 0 {
		t.Errorf("the 1h score is %.4f, want negative", byTimeframe[constants.Timeframe1h].Score)
	}
	if byTimeframe[constants.Timeframe5m].Score <= 0 {
		t.Errorf("the 5m score is %.4f, want positive", byTimeframe[constants.Timeframe5m].Score)
	}
	if byTimeframe[constants.Timeframe1h].Weight <= byTimeframe[constants.Timeframe5m].Weight {
		t.Errorf("1h weight %.2f does not exceed 5m weight %.2f",
			byTimeframe[constants.Timeframe1h].Weight, byTimeframe[constants.Timeframe5m].Weight)
	}
}

// TestOneNotReadyTimeframeBlocksEverything. A partial aggregate would be a
// real number computed from an incomplete opinion, and nothing downstream
// could tell it apart from a complete one.
func TestOneNotReadyTimeframeBlocksEverything(t *testing.T) {
	filter := defaultFilter(t)

	warming := view(constants.Timeframe1h, 2)
	warming.Ready = false

	state := filter.OnBar(barWith(
		view(constants.Timeframe5m, 2),
		view(constants.Timeframe15m, 2),
		warming,
	))

	if state.Ready {
		t.Error("the filter reports ready while 1h is still warming up")
	}
	if state.Bias != constants.BiasNeutral {
		t.Errorf("bias is %s while not ready, want neutral", state.Bias)
	}
	if state.Confidence != 0 {
		t.Errorf("confidence is %.4f while not ready, want 0", state.Confidence)
	}
	if state.NotReadyReason == "" {
		t.Error("no reason given; an operator cannot tell warm-up from gap recovery")
	}
	// The conservative reading: not-ready permits nothing.
	if state.Permits(constants.DirectionLong) || state.Permits(constants.DirectionShort) {
		t.Error("a not-ready state permits an entry")
	}
}

// TestMissingTimeframeBlocksEverything covers the case before any candle of a
// contributor has closed at all, which is different from one that is warming.
func TestMissingTimeframeBlocksEverything(t *testing.T) {
	filter := defaultFilter(t)

	state := filter.OnBar(barWith(
		view(constants.Timeframe5m, 2),
		view(constants.Timeframe15m, 2),
	))

	if state.Ready {
		t.Error("the filter reports ready with no 1h contribution at all")
	}
	if state.NotReadyReason == "" {
		t.Error("no reason given for the missing timeframe")
	}
}

// TestFilterIsDeterministic. Identical inputs must produce identical states,
// or a backtest cannot be compared with itself.
func TestFilterIsDeterministic(t *testing.T) {
	filter := defaultFilter(t)

	bar := barWith(
		view(constants.Timeframe5m, 0.7),
		view(constants.Timeframe15m, -0.3),
		view(constants.Timeframe1h, 1.1),
	)

	first := filter.OnBar(bar)
	for i := range 50 {
		next := filter.OnBar(bar)

		if next.Bias != first.Bias || next.Ready != first.Ready {
			t.Fatalf("call %d differs: %s/%v vs %s/%v", i, next.Bias, next.Ready, first.Bias, first.Ready)
		}
		if math.Abs(next.Confidence-first.Confidence) > 0 {
			t.Fatalf("call %d confidence %.17g differs from %.17g", i, next.Confidence, first.Confidence)
		}
		if len(next.PerTimeframe) != len(first.PerTimeframe) {
			t.Fatalf("call %d has %d per-timeframe entries, want %d",
				i, len(next.PerTimeframe), len(first.PerTimeframe))
		}
		for j := range next.PerTimeframe {
			if next.PerTimeframe[j] != first.PerTimeframe[j] {
				t.Fatalf("call %d entry %d differs: %+v vs %+v",
					i, j, next.PerTimeframe[j], first.PerTimeframe[j])
			}
		}
	}
}

// TestPerTimeframeOrderIsStable. The slice exists instead of the map the spec
// sketches precisely so this holds: a map here would reorder between runs and
// break the byte-identical report phase 04 requires.
func TestPerTimeframeOrderIsStable(t *testing.T) {
	filter := defaultFilter(t)
	want := trend.DefaultConfig().Timeframes()

	for range 50 {
		state := filter.OnBar(barWith(
			view(constants.Timeframe5m, 1),
			view(constants.Timeframe15m, 1),
			view(constants.Timeframe1h, 1),
		))

		if len(state.PerTimeframe) != len(want) {
			t.Fatalf("got %d entries, want %d", len(state.PerTimeframe), len(want))
		}
		for i, per := range state.PerTimeframe {
			if per.Timeframe != want[i] {
				t.Fatalf("entry %d is %s, want %s: the order is not stable",
					i, per.Timeframe, want[i])
			}
		}
	}
}

// TestEveryReportedValueTracesToAClosedBar is §8's traceability requirement.
// A score without a close time cannot be checked for look-ahead after the
// fact, which is when it usually needs checking.
func TestEveryReportedValueTracesToAClosedBar(t *testing.T) {
	filter := defaultFilter(t)
	at := alignDay().Add(2 * time.Hour)

	state := filter.OnBar(barWith(
		view(constants.Timeframe5m, 1),
		view(constants.Timeframe15m, 1),
		view(constants.Timeframe1h, 1),
	))

	for _, per := range state.PerTimeframe {
		if per.CloseTime.IsZero() {
			t.Errorf("the %s reading has no close time, so it cannot be traced to a bar", per.Timeframe)
			continue
		}
		if per.CloseTime.After(at) {
			t.Errorf("the %s reading traces to a bar closing at %s, after %s",
				per.Timeframe, per.CloseTime, at)
		}
	}
}

// TestConfigRejectsNonsense keeps a misconfigured filter from running with a
// score nobody could interpret.
func TestConfigRejectsNonsense(t *testing.T) {
	for name, config := range map[string]trend.Config{
		"no timeframes":    {DeadZone: 0.1},
		"duplicate":        {Weights: []trend.Weight{{Timeframe: constants.Timeframe1h, Weight: 1}, {Timeframe: constants.Timeframe1h, Weight: 1}}},
		"zero weight":      {Weights: []trend.Weight{{Timeframe: constants.Timeframe1h, Weight: 0}}},
		"negative weight":  {Weights: []trend.Weight{{Timeframe: constants.Timeframe1h, Weight: -1}}},
		"dead zone of one": {Weights: []trend.Weight{{Timeframe: constants.Timeframe1h, Weight: 1}}, DeadZone: 1},
		"unknown frame":    {Weights: []trend.Weight{{Timeframe: constants.Timeframe("7m"), Weight: 1}}},
	} {
		if _, err := _trend_us.NewFilterImpl(config); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}

	if err := trend.DefaultConfig().Validate(); err != nil {
		t.Errorf("the shipped default is invalid: %v", err)
	}
}

// TestDefaultsAreTheDocumentedOnes pins the shipped numbers.
//
// §4 forbids tuning them in this phase: fitting weights against the same data
// used to evaluate the result is how a filter is made to look good on the past
// and nowhere else. Pinning them here means a change has to be deliberate and
// arrives with a version bump.
func TestDefaultsAreTheDocumentedOnes(t *testing.T) {
	config := trend.DefaultConfig()

	want := []trend.Weight{
		{Timeframe: constants.Timeframe5m, Weight: 0.2},
		{Timeframe: constants.Timeframe15m, Weight: 0.3},
		{Timeframe: constants.Timeframe1h, Weight: 0.5},
	}
	if len(config.Weights) != len(want) {
		t.Fatalf("got %d weights, want %d", len(config.Weights), len(want))
	}
	for i, weight := range config.Weights {
		if weight != want[i] {
			t.Errorf("weight %d is %+v, want %+v", i, weight, want[i])
		}
	}
	if config.DeadZone != 0.15 {
		t.Errorf("dead zone is %v, want 0.15", config.DeadZone)
	}
	if total := config.TotalWeight(); math.Abs(total-1.0) > 1e-12 {
		t.Errorf("weights total %v, want 1.0", total)
	}

	// The hourly must be able to outvote the other two together, which is what
	// makes it the dominant veto rather than merely the largest single vote.
	if config.Weights[2].Weight < config.Weights[0].Weight+config.Weights[1].Weight {
		t.Error("1h cannot outweigh 5m and 15m combined; it is not the dominant frame")
	}
}

// TestFilterPermitsNothingItWasNotAsked is the phase-05 boundary.
//
// A filter is a veto: its whole output is a bias, a confidence and a
// breakdown. Permits answers a question and never proposes one, and "flat" is
// not a thing to be permitted into — a strategy does not need consent to stay
// out of the market.
func TestFilterPermitsNothingItWasNotAsked(t *testing.T) {
	filter := defaultFilter(t)

	state := filter.OnBar(barWith(
		view(constants.Timeframe5m, 3),
		view(constants.Timeframe15m, 3),
		view(constants.Timeframe1h, 3),
	))
	if !state.Ready {
		t.Fatalf("state is not ready: %s", state.NotReadyReason)
	}

	if state.Permits(constants.DirectionFlat) {
		t.Error("Permits(flat) is true; staying out of the market needs no permission")
	}
	if state.Permits(constants.Direction("sideways")) {
		t.Error("an unknown direction was permitted")
	}
}
