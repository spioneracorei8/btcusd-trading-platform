package usecase_test

import (
	"math"
	"testing"
	"time"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/backtest"
	_indicator_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/indicator/usecase"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/strategy"
	_strategy_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/strategy/usecase"
)

// earliestCollectedCandle is the oldest candle this deployment has, mirroring
// the value the trend package's warm-up test uses. It is MARKET_BACKFILL_FROM
// from .env.example, which is also where the development set begins.
var earliestCollectedCandle = time.Date(2022, 7, 1, 0, 0, 0, 0, time.UTC)

// newMTF builds the strategy at its documented defaults.
func newMTF(t *testing.T) strategy.MultiTimeframe {
	t.Helper()

	built, err := _strategy_us.NewMTFAlignmentImpl(_strategy_us.DefaultMTFAlignmentConfig())
	if err != nil {
		t.Fatalf("build mtf_alignment: %v", err)
	}
	return built
}

// disagreeing builds readings where one timeframe points the other way.
//
// Its EMA is moved to the far side of its own close, so both halves of the
// direction rule stop agreeing: price is no longer on the trending side of the
// average, and the average's slope no longer matches. That is what a timeframe
// which has not joined the move looks like, and the strategy must treat it as
// a refusal rather than a weaker yes.
func disagreeing(
	index int,
	trendAt func(int) float64,
	rising bool,
	dissenter constants.Timeframe,
) *models.TrendSnapshot {
	snapshot := alignedHigher(index, trendAt, rising)

	for i, reading := range snapshot.Readings {
		if reading.Timeframe != dissenter {
			continue
		}

		flipped := reading
		closePrice, _ := reading.Candle.Close.Float64()
		flipped.Indicators.EMA = closePrice + 100
		if !rising {
			flipped.Indicators.EMA = closePrice - 100
		}
		snapshot.Readings[i] = flipped
	}
	return snapshot
}

// steadyTrend is a trend moving at a constant rate from a flat warm-up, as a
// pure function of index so higher-timeframe readings can be evaluated at
// their own closes rather than at the current bar.
func steadyTrend(rising bool) func(int) float64 {
	step := 4.0
	if !rising {
		step = -4.0
	}
	return func(i int) float64 {
		if i < 300 {
			return 40000
		}
		return 40000 + step*float64(i-300)
	}
}

// driveAligned feeds the strategy a rising or falling series with the higher
// timeframes reported by build, and returns the entries it produced.
//
// The shape matches driveOverProvokingSeries: a slow trend that the higher
// timeframes report, and a base price oscillating around it so retracements
// happen without the trend turning.
func driveAligned(
	strat strategy.Strategy,
	rising bool,
	build func(index int, trendAt func(int) float64, rising bool) *models.TrendSnapshot,
) []strategy.Intent {
	var intents []strategy.Intent

	trendAt := steadyTrend(rising)

	for i := range 1900 {
		price := trendAt(i)
		if i >= 300 {
			// Oscillating around the trend, so the base timeframe retraces to
			// its own EMA repeatedly while the higher ones never turn.
			price += 400 * math.Sin(2*math.Pi*float64(i-300)/80)
		}

		intents = append(intents, strat.OnBar(
			barAligned(i, price, 50, 100, flat(), build(i, trendAt, rising)))...)
	}
	return intents
}

// driveAlignedFrom drives the same series but returns only the intents
// produced from `from` onward, so a mid-run change can be asserted on without
// the bars before it.
func driveAlignedFrom(
	strat strategy.Strategy,
	from int,
	rising bool,
	build func(index int, trendAt func(int) float64, rising bool) *models.TrendSnapshot,
) []strategy.Intent {
	var intents []strategy.Intent

	trendAt := steadyTrend(rising)

	for i := range 1900 {
		price := trendAt(i)
		if i >= 300 {
			price += 400 * math.Sin(2*math.Pi*float64(i-300)/80)
		}

		produced := strat.OnBar(barAligned(i, price, 50, 100, flat(), build(i, trendAt, rising)))
		if i >= from {
			intents = append(intents, produced...)
		}
	}
	return intents
}

func entriesOnly(intents []strategy.Intent) []strategy.Intent {
	var entries []strategy.Intent
	for _, intent := range intents {
		if intent.Kind == strategy.IntentEnterLong || intent.Kind == strategy.IntentEnterShort {
			entries = append(entries, intent)
		}
	}
	return entries
}

// TestAlignmentIsTheEntryCondition. With every timeframe agreeing, the strategy
// trades; that is the baseline the refusal tests below are measured against.
func TestAlignmentIsTheEntryCondition(t *testing.T) {
	entries := entriesOnly(driveAligned(newMTF(t), true, alignedHigher))
	if len(entries) == 0 {
		t.Fatal("no entry with every timeframe aligned; the refusal tests below would prove nothing")
	}

	for _, entry := range entries {
		if entry.Kind != strategy.IntentEnterLong {
			t.Errorf("a rising series produced %s", entry.Kind)
		}
		// Levels travel with the entry, never separately — the shape that made
		// the phase-04 unprotected-position defect expressible.
		if !entry.Stop.IsPositive() || !entry.Target.IsPositive() {
			t.Errorf("entry carries stop %s and target %s; both must be set", entry.Stop, entry.Target)
		}
		if !entry.Stop.LessThan(entry.Target) {
			t.Errorf("long stop %s is not below target %s", entry.Stop, entry.Target)
		}
	}
}

// TestNoEntryWhenTheDominantTimeframeDisagrees.
//
// 4h is the gate, not a vote. A 4h downtrend means no long exists to confirm,
// whatever the intermediate timeframes are doing — and a rule where enough
// agreement lower down could carry it would be a different strategy with a
// tuning knob attached.
func TestNoEntryWhenTheDominantTimeframeDisagrees(t *testing.T) {
	build := func(index int, trendAt func(int) float64, rising bool) *models.TrendSnapshot {
		return disagreeing(index, trendAt, rising, constants.Timeframe4h)
	}

	entries := entriesOnly(driveAligned(newMTF(t), true, build))
	if len(entries) != 0 {
		t.Errorf("%d entries taken against the dominant timeframe; the first was %+v",
			len(entries), entries[0])
	}
}

// TestNoEntryWhenAnIntermediateTimeframeContradicts.
//
// Every intermediate must agree. One disagreeing is a veto rather than a
// reduced score: the rule is an AND, and a majority that could outvote a
// dissenter is a weighting scheme nobody chose.
func TestNoEntryWhenAnIntermediateTimeframeContradicts(t *testing.T) {
	for _, dissenter := range []constants.Timeframe{constants.Timeframe15m, constants.Timeframe1h} {
		build := func(index int, trendAt func(int) float64, rising bool) *models.TrendSnapshot {
			return disagreeing(index, trendAt, rising, dissenter)
		}

		entries := entriesOnly(driveAligned(newMTF(t), true, build))
		if len(entries) != 0 {
			t.Errorf("%d entries taken while %s contradicted the dominant direction",
				len(entries), dissenter)
		}
	}
}

// TestLongAndShortAreSymmetric. The rule must not be easier to satisfy in one
// direction: an asymmetry here would show up as a long bias in every result and
// be indistinguishable from a market that happened to rise.
func TestLongAndShortAreSymmetric(t *testing.T) {
	longs := entriesOnly(driveAligned(newMTF(t), true, alignedHigher))
	shorts := entriesOnly(driveAligned(newMTF(t), false, alignedHigher))

	if len(longs) == 0 || len(shorts) == 0 {
		t.Fatalf("mirrored series produced %d longs and %d shorts; one side never fired",
			len(longs), len(shorts))
	}
	for _, entry := range shorts {
		if entry.Kind != strategy.IntentEnterShort {
			t.Errorf("a falling series produced %s", entry.Kind)
		}
		if !entry.Stop.GreaterThan(entry.Target) {
			t.Errorf("short stop %s is not above target %s", entry.Stop, entry.Target)
		}
	}

	// Counts need not match exactly — the sine phase differs against a falling
	// trend — but a large asymmetry means the rule is not mirrored.
	ratio := float64(len(longs)) / float64(len(shorts))
	if ratio < 0.5 || ratio > 2 {
		t.Errorf("%d longs against %d shorts on mirrored series; the rule is not symmetric",
			len(longs), len(shorts))
	}
}

// TestLongOnlySuppressesShorts, for a spot account where a short is fiction
// rather than an unsupported feature.
func TestLongOnlySuppressesShorts(t *testing.T) {
	config := _strategy_us.DefaultMTFAlignmentConfig()
	config.LongOnly = true

	built, err := _strategy_us.NewMTFAlignmentImpl(config)
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	if entries := entriesOnly(driveAligned(built, false, alignedHigher)); len(entries) != 0 {
		t.Errorf("%d shorts taken by a long-only configuration", len(entries))
	}
}

// TestARepeatedReadingIsNotANewObservation is the mistake this strategy is
// most likely to make quietly.
//
// The aligner repeats the same 4h reading across 240 one-minute bars. Treating
// each repeat as a new observation would set the previous EMA equal to the
// current one on every bar, every slope would read as flat, and the strategy
// would refuse to trade for a reason nothing in the report could explain.
//
// So: hold every reading completely still and confirm that no direction is
// ever derived from it, however many bars go by.
func TestARepeatedReadingIsNotANewObservation(t *testing.T) {
	strat := newMTF(t)

	frozen := alignedHigher(0, func(int) float64 { return 40000 }, true)
	var intents []strategy.Intent

	trendAt := steadyTrend(true)
	for i := range 2000 {
		price := trendAt(i) + 400*math.Sin(2*math.Pi*float64(i)/80)
		intents = append(intents, strat.OnBar(barAligned(i, price, 50, 100, flat(), frozen))...)
	}

	if entries := entriesOnly(intents); len(entries) != 0 {
		t.Errorf("%d entries taken from a higher-timeframe reading that never advanced; "+
			"a repeat is being scored as a new bar", len(entries))
	}
}

// TestNoEntryWhileATimeframeIsStillWarmingUp.
//
// All, not any. A strategy requiring several timeframes to agree cannot act on
// a subset: the one still warming up is the one that would have disagreed as
// often as not, and treating absence as consent is how a warm-up hole becomes
// a run that traded on less evidence than it claimed.
func TestNoEntryWhileATimeframeIsStillWarmingUp(t *testing.T) {
	for _, cold := range []constants.Timeframe{
		constants.Timeframe15m, constants.Timeframe1h, constants.Timeframe4h,
	} {
		build := func(index int, trendAt func(int) float64, rising bool) *models.TrendSnapshot {
			return chill(alignedHigher(index, trendAt, rising), cold)
		}

		if entries := entriesOnly(driveAligned(newMTF(t), true, build)); len(entries) != 0 {
			t.Errorf("%d entries taken while %s was still warming up", len(entries), cold)
		}
	}
}

// TestATimeframeGoingColdMidRunStopsTrading is the case that distinguishes a
// readiness check from an accident.
//
// A timeframe that was never ready is refused for several independent reasons:
// it has no recorded history, so it has no slope either. A timeframe that goes
// cold *after* building history — which is what gap recovery looks like — has
// all of that and is still not entitled to speak. Only an explicit readiness
// check refuses it.
//
// Without this, the readiness rule could be deleted entirely and every other
// test here would still pass.
func TestATimeframeGoingColdMidRunStopsTrading(t *testing.T) {
	const goesColdAt = 1000

	for _, cold := range []constants.Timeframe{
		constants.Timeframe15m, constants.Timeframe1h, constants.Timeframe4h,
	} {
		// First establish that this series trades at all after that point,
		// so the assertion below is about the gap and not about the fixture.
		warm := newMTF(t)
		if len(entriesOnly(driveAlignedFrom(warm, goesColdAt, true, alignedHigher))) == 0 {
			t.Fatalf("the aligned series produces no entries after bar %d; "+
				"the %s case would prove nothing", goesColdAt, cold)
		}

		build := func(index int, trendAt func(int) float64, rising bool) *models.TrendSnapshot {
			snapshot := alignedHigher(index, trendAt, rising)
			if index < goesColdAt {
				return snapshot
			}
			return chill(snapshot, cold)
		}

		entries := entriesOnly(driveAlignedFrom(newMTF(t), goesColdAt, true, build))
		if len(entries) != 0 {
			t.Errorf("%d entries taken after %s went cold mid-run, having warmed up earlier",
				len(entries), cold)
		}
	}
}

// chill marks one timeframe as not ready, leaving everything else about the
// reading intact.
func chill(snapshot *models.TrendSnapshot, cold constants.Timeframe) *models.TrendSnapshot {
	for i, reading := range snapshot.Readings {
		if reading.Timeframe == cold {
			reading.Ready = false
			snapshot.Readings[i] = reading
		}
	}
	return snapshot
}

// TestNoEntryWithoutAnySnapshotAtAll. A strategy run with no aligner finds nil
// here. The engine refuses that configuration outright, and this is the second
// half of the same guarantee: even if it were reached, nothing would trade.
func TestNoEntryWithoutAnySnapshotAtAll(t *testing.T) {
	strat := newMTF(t)

	var intents []strategy.Intent
	trendAt := steadyTrend(true)
	for i := range 2000 {
		price := trendAt(i) + 400*math.Sin(2*math.Pi*float64(i)/80)
		intents = append(intents, strat.OnBar(bar(i, price, 50, 100, flat()))...)
	}

	if entries := entriesOnly(intents); len(entries) != 0 {
		t.Errorf("%d entries taken with no higher-timeframe readings at all", len(entries))
	}
}

// TestTheStrategyDeclaresEveryTimeframeItReads, shortest first, which is the
// aligner's contract.
func TestTheStrategyDeclaresEveryTimeframeItReads(t *testing.T) {
	config := _strategy_us.DefaultMTFAlignmentConfig()
	required := newMTF(t).RequiredTimeframes()

	want := []constants.Timeframe{
		constants.Timeframe15m, constants.Timeframe1h, constants.Timeframe4h,
	}
	if len(required) != len(want) {
		t.Fatalf("declares %v, want %v", required, want)
	}
	for i, timeframe := range required {
		if timeframe != want[i] {
			t.Fatalf("declares %v, want %v (shortest first)", required, want)
		}
	}

	// Everything it actually reads must be in the list, or the aligner would
	// not fetch it and the strategy would silently never see it.
	declared := map[constants.Timeframe]bool{}
	for _, timeframe := range required {
		declared[timeframe] = true
	}
	if !declared[config.Dominant] {
		t.Errorf("the dominant timeframe %s is not declared", config.Dominant)
	}
	for _, timeframe := range config.Intermediate {
		if !declared[timeframe] {
			t.Errorf("intermediate %s is not declared", timeframe)
		}
	}
}

// TestTheDailyTimeframeIsAbsentAndWhy.
//
// 1d is what the design asked for as a second dominant timeframe, and it is
// not here. This states the reason as an assertion rather than as a comment
// somebody could contradict: EMA(200) at the 5x warm-up rule needs 1000 daily
// closes, and the collected history does not reach that far back.
//
// It is not a claim that a daily contributor is useless. If the daily series is
// ever backfilled to 2020 or earlier, this test is what says it may come back.
func TestTheDailyTimeframeIsAbsentAndWhy(t *testing.T) {
	for _, timeframe := range newMTF(t).RequiredTimeframes() {
		if timeframe == constants.Timeframe1d {
			t.Fatal("1d is a contributor again; check the warm-up test below still passes")
		}
	}

	warmupCloses := constants.WarmupMultiplier * _indicator_us.DefaultSetConfig().EMAPeriod
	budget := backtest.DevFrom.Sub(earliestCollectedCandle)
	required := time.Duration(warmupCloses) * constants.Timeframe1d.Duration()

	if required <= budget {
		t.Errorf("1d now needs only %.0f days of warm-up against %.0f days of history — "+
			"the reason it was dropped no longer holds, and it should be reinstated",
			required.Hours()/24, budget.Hours()/24)
	}
}

// TestEveryContributorOfThisStrategyCanWarmUp is the phase-06 §12.3
// requirement, and the check that would have caught the 4h base shipping with
// a contributor that could never become ready.
//
// A contributor whose warm-up reaches further back than the collected history
// never reports ready. The strategy then correctly declines every bar and the
// run reports zero trades with nothing to explain it — which is worse than an
// error, because it produces a number.
func TestEveryContributorOfThisStrategyCanWarmUp(t *testing.T) {
	warmupCloses := constants.WarmupMultiplier * _indicator_us.DefaultSetConfig().EMAPeriod
	budget := backtest.DevFrom.Sub(earliestCollectedCandle)

	// Taken from the strategy rather than listed here. A test that enumerates
	// what it checks will always lag the thing it checks — which is exactly
	// how the 4h base shipped unmeasured.
	contributors := newMTF(t).RequiredTimeframes()
	if len(contributors) == 0 {
		t.Fatal("the strategy declares no contributors; this test is checking nothing")
	}

	for _, timeframe := range contributors {
		required := time.Duration(warmupCloses) * timeframe.Duration()
		if required > budget {
			t.Errorf(
				"mtf_alignment reads %s, which needs %.0f days of warm-up before the "+
					"development set begins — only %.0f days of history exist.\n"+
					"It would never become ready, and the strategy would report a "+
					"zero-trade run rather than an error.",
				timeframe, required.Hours()/24, budget.Hours()/24)
		}
	}
}

// TestBadAlignmentConfigurationsAreRejected at construction, where a mistake is
// a message rather than a run that quietly measures something else.
func TestBadAlignmentConfigurationsAreRejected(t *testing.T) {
	cases := map[string]func(*_strategy_us.MTFAlignmentConfig){
		"no intermediate timeframes": func(c *_strategy_us.MTFAlignmentConfig) {
			c.Intermediate = nil
		},
		"an intermediate at the dominant timeframe": func(c *_strategy_us.MTFAlignmentConfig) {
			c.Intermediate = []constants.Timeframe{constants.Timeframe4h}
		},
		"an intermediate above the dominant timeframe": func(c *_strategy_us.MTFAlignmentConfig) {
			c.Dominant = constants.Timeframe1h
			c.Intermediate = []constants.Timeframe{constants.Timeframe4h}
		},
		"a repeated timeframe": func(c *_strategy_us.MTFAlignmentConfig) {
			c.Intermediate = []constants.Timeframe{constants.Timeframe1h, constants.Timeframe1h}
		},
		"no resume bars":          func(c *_strategy_us.MTFAlignmentConfig) { c.ResumeBars = 0 },
		"no pullback distance":    func(c *_strategy_us.MTFAlignmentConfig) { c.PullbackATR = 0 },
		"a trigger period of one": func(c *_strategy_us.MTFAlignmentConfig) { c.TriggerPeriod = 1 },
	}

	for name, break_ := range cases {
		config := _strategy_us.DefaultMTFAlignmentConfig()
		break_(&config)

		if _, err := _strategy_us.NewMTFAlignmentImpl(config); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}
