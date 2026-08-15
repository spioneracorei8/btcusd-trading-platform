package usecase_test

import (
	"math"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/strategy"
	_strategy_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/strategy/usecase"
)

var seriesStart = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// bar builds a decision point: a closed candle plus the indicator snapshot the
// engine would have computed for it.
func bar(index int, closePrice float64, rsi, atr float64, position strategy.Position) strategy.BarContext {
	return barAligned(index, closePrice, rsi, atr, position, nil)
}

// barAligned is the same decision point with higher-timeframe readings
// attached, as a multi-timeframe strategy would be given them.
func barAligned(
	index int,
	closePrice float64,
	rsi, atr float64,
	position strategy.Position,
	higher *models.TrendSnapshot,
) strategy.BarContext {
	at := seriesStart.Add(time.Duration(index) * time.Minute)
	price := decimal.NewFromFloat(closePrice)

	return strategy.BarContext{
		Candle: models.Candle{
			Symbol: "BTCUSDT", MarketType: constants.MarketTypeSpot,
			Timeframe: constants.Timeframe1m,
			OpenTime:  at, CloseTime: at.Add(time.Minute),
			Open: price, High: price, Low: price, Close: price,
			Volume: decimal.NewFromInt(10), IsClosed: true,
		},
		Indicators: models.IndicatorSnapshot{
			OpenTime: at, RSI: rsi, ATR: atr,
			EMA: closePrice, VWAP: closePrice,
		},
		Position: position,
		Higher:   higher,
	}
}

// alignedHigher synthesises the readings a multi-timeframe strategy would see
// when every higher timeframe points the same way.
//
// # Why it takes a function rather than a price
//
// A reading must be *frozen* between that timeframe's closes. The real aligner
// repeats one 4h candle across 240 one-minute bars — same close, same
// indicators, same close_time — and a fixture whose close drifted while its
// close_time stood still would be looser than reality in the direction that
// matters: a stale EMA compared against a live close eventually agrees with
// anything, and a timeframe set up to disagree would quietly stop disagreeing.
//
// So each reading is evaluated at its own last close, through trendAt.
//
// The higher-timeframe close is deliberately not the base bar's close. A 4h
// candle's close is not the 1m close inside it, and tying them together would
// make it impossible to build the case this strategy exists for: a retracement
// on the base timeframe that leaves the higher ones pointing the same way
// throughout.
func alignedHigher(index int, trendAt func(int) float64, rising bool) *models.TrendSnapshot {
	timeframes := []struct {
		timeframe constants.Timeframe
		minutes   int
	}{
		{constants.Timeframe15m, 15},
		{constants.Timeframe1h, 60},
		{constants.Timeframe4h, 240},
	}

	readings := make([]models.TimeframeReading, 0, len(timeframes))
	for _, entry := range timeframes {
		// The most recent close of this timeframe at or before now, and the
		// trend as it stood at that instant.
		closedIndex := index / entry.minutes * entry.minutes
		closedAt := seriesStart.Add(time.Duration(closedIndex) * time.Minute)
		closePrice := trendAt(closedIndex)

		// The EMA sits below the close in an uptrend and above it in a
		// downtrend, so price-versus-average and the average's slope agree —
		// which is what the direction rule requires of both.
		ema := closePrice - 100
		if !rising {
			ema = closePrice + 100
		}

		price := decimal.NewFromFloat(closePrice)
		readings = append(readings, models.TimeframeReading{
			Timeframe: entry.timeframe,
			Candle: models.Candle{
				Symbol: "BTCUSDT", MarketType: constants.MarketTypeSpot,
				Timeframe: entry.timeframe,
				OpenTime:  closedAt.Add(-time.Duration(entry.minutes) * time.Minute),
				CloseTime: closedAt,
				Open:      price, High: price, Low: price, Close: price,
				Volume: decimal.NewFromInt(10), IsClosed: true,
			},
			Indicators: models.IndicatorSnapshot{OpenTime: closedAt, EMA: ema, ATR: 100},
			CloseTime:  closedAt,
			Ready:      true,
		})
	}
	return &models.TrendSnapshot{Readings: readings}
}

func flat() strategy.Position {
	return strategy.Position{Direction: constants.DirectionFlat}
}

// allStrategies builds every registered strategy at its documented defaults.
func allStrategies(t *testing.T) map[string]strategy.Strategy {
	t.Helper()

	out := map[string]strategy.Strategy{}
	for _, entry := range _strategy_us.All() {
		built, err := entry.Build(0.1, false)
		if err != nil {
			t.Fatalf("build %s: %v", entry.Name, err)
		}
		out[entry.Name] = built
	}
	if len(out) != 4 {
		t.Fatalf("registry has %d strategies, want the four phase-06 requires", len(out))
	}
	return out
}

// TestEveryEntryCarriesItsOwnLevels is the §A1 requirement, and the reason the
// entry intent carries the levels at all.
//
// Phase 04 had a defect where a stop issued as a separate intent alongside an
// entry was silently dropped, and the position ran unprotected while nothing
// in the report said so. The bug is fixed; this is what stops a strategy
// reintroducing the *shape* of it.
//
// Every registered strategy is driven over a series designed to make it fire,
// and every entry it produces must carry both levels in the same intent.
func TestEveryEntryCarriesItsOwnLevels(t *testing.T) {
	for name, strat := range allStrategies(t) {
		entries := 0

		for _, intent := range driveOverProvokingSeries(strat) {
			switch intent.Kind {
			case strategy.IntentEnterLong, strategy.IntentEnterShort:
				entries++
				if !intent.Stop.IsPositive() {
					t.Errorf("%s produced an entry with no stop: %+v", name, intent)
				}
				if !intent.Target.IsPositive() {
					t.Errorf("%s produced an entry with no target: %+v", name, intent)
				}
				if !intent.HasLevels() {
					t.Errorf("%s produced an entry that reports no levels", name)
				}

			case strategy.IntentSetStop, strategy.IntentSetTarget:
				t.Errorf("%s issued %s as a separate intent. Levels belong on the entry: "+
					"a separately-issued stop is the shape of the phase-04 defect, where "+
					"the position opened and the protection was lost.", name, intent.Kind)
			}
		}

		if entries == 0 {
			t.Errorf("%s never entered over the provoking series, so nothing was checked", name)
		}
	}
}

// TestEveryEntryIsProtectedOnTheCorrectSide. A long's stop must sit below its
// entry and its target above; a stop on the wrong side is not protection, it
// is an instant exit.
func TestEveryEntryIsProtectedOnTheCorrectSide(t *testing.T) {
	for name, strat := range allStrategies(t) {
		for _, intent := range driveOverProvokingSeries(strat) {
			if intent.Stop.IsZero() || intent.Target.IsZero() {
				continue
			}

			switch intent.Kind {
			case strategy.IntentEnterLong:
				if !intent.Stop.LessThan(intent.Target) {
					t.Errorf("%s long has stop %s at or above target %s",
						name, intent.Stop, intent.Target)
				}
			case strategy.IntentEnterShort:
				if !intent.Stop.GreaterThan(intent.Target) {
					t.Errorf("%s short has stop %s at or below target %s",
						name, intent.Stop, intent.Target)
				}
			}
		}
	}
}

// TestStrategiesAreDeterministic. Two instances fed the same bars must produce
// the same intents, or nothing downstream can be compared with itself.
func TestStrategiesAreDeterministic(t *testing.T) {
	for _, entry := range _strategy_us.All() {
		first, err := entry.Build(0.1, false)
		if err != nil {
			t.Fatalf("build %s: %v", entry.Name, err)
		}
		second, err := entry.Build(0.1, false)
		if err != nil {
			t.Fatalf("build %s: %v", entry.Name, err)
		}

		a := driveOverProvokingSeries(first)
		b := driveOverProvokingSeries(second)

		if len(a) != len(b) {
			t.Fatalf("%s produced %d intents then %d over identical input",
				entry.Name, len(a), len(b))
		}
		for i := range a {
			if a[i].Kind != b[i].Kind || !a[i].Stop.Equal(b[i].Stop) ||
				!a[i].Target.Equal(b[i].Target) || a[i].Reason != b[i].Reason {
				t.Fatalf("%s intent %d differs between identical runs:\n %+v\n %+v",
					entry.Name, i, a[i], b[i])
			}
		}
	}
}

// TestNoStrategyEntersWhileAPositionIsOpen. The engine drops a second entry
// anyway, but asking for one it will refuse inflates the veto counts and makes
// the filter look busier than it is.
func TestNoStrategyEntersWhileAPositionIsOpen(t *testing.T) {
	held := strategy.Position{
		Direction:  constants.DirectionLong,
		EntryPrice: decimal.NewFromInt(27000),
		EntryTime:  seriesStart,
		Size:       decimal.NewFromInt(1),
		BarsHeld:   3,
	}

	for name, strat := range allStrategies(t) {
		// Warm it up flat, then keep feeding it bars while holding a position.
		for i := range 400 {
			strat.OnBar(barAligned(i, provokingTrend(i), 50, 100, flat(),
				alignedHigher(i, provokingTrend, true)))
		}
		for i := 400; i < 500; i++ {
			for _, intent := range strat.OnBar(barAligned(i, provokingTrend(i), 25+float64(i%60), 100, held,
				alignedHigher(i, provokingTrend, true))) {
				if intent.Kind == strategy.IntentEnterLong || intent.Kind == strategy.IntentEnterShort {
					t.Errorf("%s asked to enter while a position was already open", name)
				}
			}
		}
	}
}

// TestRewardBelowTheRoundTripIsRejectedAtConstruction is §A2.
//
// A strategy targeting less than it pays in fees cannot win, and it must fail
// loudly when it is built rather than quietly in the results — where it would
// look like a strategy with no edge rather than a configuration that could
// never have had one.
func TestRewardBelowTheRoundTripIsRejectedAtConstruction(t *testing.T) {
	// A target of 0.5 ATR is 0.05% of price at the reference volatility, which
	// is half the round trip. Every winner would pay more than it made.
	tiny := strategy.Levels{StopATRMult: 0.4, TargetATRMult: 0.5}
	if err := tiny.Validate(0.1); err == nil {
		t.Error("a target below the round-trip cost was accepted")
	}

	// Reward below risk: right most of the time merely to break even.
	inverted := strategy.Levels{StopATRMult: 3.0, TargetATRMult: 1.0}
	if err := inverted.Validate(0.1); err == nil {
		t.Error("a reward-to-risk below 1 was accepted")
	}

	for _, bad := range []strategy.Levels{
		{StopATRMult: 0, TargetATRMult: 2},
		{StopATRMult: 1, TargetATRMult: 0},
		{StopATRMult: -1, TargetATRMult: 2},
	} {
		if err := bad.Validate(0.1); err == nil {
			t.Errorf("%+v was accepted", bad)
		}
	}

	// And every shipped default must clear its own bar.
	for _, entry := range _strategy_us.All() {
		if _, err := entry.Build(0.1, false); err != nil {
			t.Errorf("the shipped default for %s does not clear the round trip: %v", entry.Name, err)
		}
	}
}

// TestAHigherFeeTierCanInvalidateAConfiguration. The cost is passed in rather
// than assumed precisely so this holds: a configuration that works at 0.1%
// need not work at 1%, and finding out at construction beats finding out from
// a year of flat results.
func TestAHigherFeeTierCanInvalidateAConfiguration(t *testing.T) {
	levels := strategy.Levels{StopATRMult: 1.0, TargetATRMult: 1.5}

	if err := levels.Validate(0.1); err != nil {
		t.Fatalf("levels rejected at the normal fee tier: %v", err)
	}
	// 0.15 ATR-percent of reward against a 0.2% round trip.
	if err := levels.Validate(0.2); err == nil {
		t.Error("the same levels were accepted at double the cost")
	}
}

// TestLevelsAreATRScaledNotPercentage. The same multiplier must produce a
// wider stop in a volatile market, which is the whole reason ATR is used
// rather than a percentage of price.
func TestLevelsAreATRScaledNotPercentage(t *testing.T) {
	levels := strategy.Levels{StopATRMult: 2, TargetATRMult: 4}
	price := decimal.NewFromInt(27000)

	quiet := levels.StopFor(price, 10, true)
	volatile := levels.StopFor(price, 100, true)

	if !volatile.LessThan(quiet) {
		t.Errorf("a volatile-market stop (%s) is not further from price than a quiet one (%s)",
			volatile, quiet)
	}

	// A degenerate ATR must produce no level rather than a nonsensical one.
	if got := levels.StopFor(price, 0, true); !got.IsZero() {
		t.Errorf("a zero ATR produced a stop at %s, want none", got)
	}
	if got := levels.StopFor(decimal.NewFromInt(5), 1000, true); !got.IsZero() {
		t.Errorf("a stop below zero was returned as %s, want none", got)
	}
}

// TestRegistryIsStableAndComplete.
func TestRegistryIsStableAndComplete(t *testing.T) {
	want := []string{"ema_crossover", "mtf_alignment", "rsi_reversion", "trend_pullback"}

	names := _strategy_us.Names()
	if len(names) != len(want) {
		t.Fatalf("registry has %v, want %v", names, want)
	}
	for i, name := range names {
		if name != want[i] {
			t.Errorf("name %d is %q, want %q", i, name, want[i])
		}
	}

	// Order must not vary between calls: the list is printed and iterated, and
	// a map here would reorder it between runs.
	for range 20 {
		for i, name := range _strategy_us.Names() {
			if name != want[i] {
				t.Fatalf("the registry order is not stable: got %v", _strategy_us.Names())
			}
		}
	}

	if _, err := _strategy_us.Lookup("no_such_strategy"); err == nil {
		t.Error("an unknown strategy resolved")
	}
	for _, name := range want {
		entry, err := _strategy_us.Lookup(name)
		if err != nil {
			t.Errorf("%s does not resolve: %v", name, err)
		}
		if entry.Describe() == "" {
			t.Errorf("%s describes itself as nothing; a report could not say what produced it", name)
		}
	}
}

// TestBadConfigurationsAreRejected covers each strategy's own parameters.
func TestBadConfigurationsAreRejected(t *testing.T) {
	fast := _strategy_us.DefaultEMACrossoverConfig()
	fast.SlowPeriod = fast.FastPeriod
	if _, err := _strategy_us.NewEMACrossoverImpl(fast); err == nil {
		t.Error("a crossover with equal periods was accepted; it can never cross")
	}

	rsi := _strategy_us.DefaultRSIReversionConfig()
	rsi.Oversold = 60
	if _, err := _strategy_us.NewRSIReversionImpl(rsi); err == nil {
		t.Error("an oversold band above 50 was accepted")
	}

	pullback := _strategy_us.DefaultTrendPullbackConfig()
	pullback.ResumeBars = 0
	if _, err := _strategy_us.NewTrendPullbackImpl(pullback); err == nil {
		t.Error("zero resume bars was accepted; every touch would be an entry")
	}
}

// provokingTrend is the slow trend underlying the provoking series, as a pure
// function of bar index: flat, then up, then down, then up.
//
// A function rather than an accumulated variable because the higher-timeframe
// readings have to be evaluated at *their* last close, which is behind the
// current bar. An accumulator only knows where it is now.
func provokingTrend(i int) float64 {
	const (
		warmup = 300
		leg    = 1200
		base   = 27000.0
		step   = 4.0
	)

	switch {
	case i < warmup:
		return base
	case i < warmup+leg:
		return base + step*float64(i-warmup)
	case i < warmup+2*leg:
		return base + step*float64(leg) - step*float64(i-warmup-leg)
	default:
		return base + step*float64(i-warmup-2*leg)
	}
}

// driveOverProvokingSeries feeds a strategy a series shaped to make each of
// the four fire: a long warm-up, then a trending leg, a reversal, and an RSI
// excursion in both directions.
//
// # Why the trend and the price are separate
//
// The first three strategies read one timeframe, so a monotonic ramp provokes
// them. mtf_alignment needs something a ramp cannot express: a retracement on
// the base timeframe *inside* a higher-timeframe trend that does not turn. So
// the series carries a slow trend, which is what the higher timeframes report,
// and a base price that oscillates around it — a rising market that pulls back
// and resumes, repeatedly, which is the condition this strategy waits for.
//
// The legs are long because a 4h contributor only advances once every 240 base
// bars and needs two of them before it has a slope at all. A shorter leg would
// leave the dominant timeframe reporting flat throughout, and a strategy that
// correctly declined every bar would look like one that was never driven.
//
// It returns every intent produced. The series is deliberately synthetic —
// this checks the shape of what a strategy emits, never whether it is
// profitable, which no fixture can answer.
func driveOverProvokingSeries(strat strategy.Strategy) []strategy.Intent {
	var intents []strategy.Intent

	const (
		warmup = 300
		leg    = 1200
	)

	for i := range warmup + 3*leg {
		trend := provokingTrend(i)

		price := trend
		rsi := 50.0
		rising := true

		switch {
		case i < warmup:
			// Flat: nothing has a direction yet, and the strategies' own EMAs
			// converge.
		case i < warmup+leg:
			price = trend + 400*math.Sin(2*math.Pi*float64(i-warmup)/80)
			rsi = 50 + float64((i-warmup)%50)
		case i < warmup+2*leg:
			price = trend + 400*math.Sin(2*math.Pi*float64(i-warmup-leg)/80)
			rising = false
			rsi = 70 - float64((i-warmup-leg)%70)
			if rsi < 5 {
				rsi = 5 + float64(i%40)
			}
		default:
			price = trend + 400*math.Sin(2*math.Pi*float64(i-warmup-2*leg)/80)
			rsi = 45 + float64(i%30)
		}

		// Higher-timeframe readings travel with every bar. Without them a
		// multi-timeframe strategy would decline every bar and the guarantees
		// these tests exist to check would pass by never being exercised.
		intents = append(intents, strat.OnBar(
			barAligned(i, price, rsi, 100, flat(), alignedHigher(i, provokingTrend, rising)))...)
	}
	return intents
}

// TestNoStrategyReadsANaNIndicator. A NaN reaching the arithmetic would
// propagate into a level and produce a stop at NaN, which is not a stop.
func TestNoStrategyReadsANaNIndicator(t *testing.T) {
	for name, strat := range allStrategies(t) {
		for i := range 400 {
			for _, intent := range strat.OnBar(bar(i, 27000, math.NaN(), math.NaN(), flat())) {
				stop, _ := intent.Stop.Float64()
				target, _ := intent.Target.Float64()
				if math.IsNaN(stop) || math.IsNaN(target) {
					t.Fatalf("%s produced a NaN level from NaN indicators", name)
				}
				if intent.Kind == strategy.IntentEnterLong || intent.Kind == strategy.IntentEnterShort {
					t.Errorf("%s entered on a bar whose indicators were not available", name)
				}
			}
		}
	}
}
