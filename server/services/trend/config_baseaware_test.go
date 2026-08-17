package trend_test

import (
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/backtest"
	_indicator_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/indicator/usecase"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/trend"
)

// earliestCollected is the oldest candle this deployment has: the
// MARKET_BACKFILL_FROM in .env.example.
//
// It sits six months before the development set on purpose, and that gap is
// the entire warm-up budget the check below spends. Bring the two together and
// every higher-timeframe contributor is cold for the whole evaluation.
//
// This is a claim about the deployment, not only about a file. It holds
// because the collector honours the setting in both directions — it fills
// history older than the stored series as well as newer. Before that it did
// not, and a series collected under an earlier setting stayed short however
// the file was edited.
var earliestCollected = time.Date(2022, 7, 1, 0, 0, 0, 0, time.UTC)

// TestEachBaseResolvesToItsDocumentedContributors is the table from the fix,
// written out so a change to it has to be deliberate.
func TestEachBaseResolvesToItsDocumentedContributors(t *testing.T) {
	for _, testCase := range []struct {
		base constants.Timeframe
		want string
	}{
		{constants.Timeframe1m, "5m,15m,1h"},
		{constants.Timeframe5m, "15m,1h,4h"},
		{constants.Timeframe15m, "1h,4h"},
		{constants.Timeframe1h, "4h"},
	} {
		config, err := trend.DefaultConfigFor(testCase.base)
		if err != nil {
			t.Errorf("DefaultConfigFor(%s) returned error: %v", testCase.base, err)
			continue
		}
		if got := joined(timeframesOf(config)); got != testCase.want {
			t.Errorf("base %s resolves to %q, want %q", testCase.base, got, testCase.want)
		}
		if err := config.Validate(); err != nil {
			t.Errorf("base %s produced an invalid config: %v", testCase.base, err)
		}
		if total := config.TotalWeight(); math.Abs(total-1) > 1e-9 {
			t.Errorf("base %s weights sum to %v, want 1", testCase.base, total)
		}
	}
}

// TestTheOneMinuteBaseIsByteIdenticalToBefore.
//
// This is the reason the contributor set is per-base rather than one shared
// list. Three of the seven completed evaluation runs used a 1m base; if this
// resolution changed, those results would stop being comparable with anything
// run afterwards, and the log that records them would quietly become
// misleading.
func TestTheOneMinuteBaseIsByteIdenticalToBefore(t *testing.T) {
	config, err := trend.DefaultConfigFor(constants.Timeframe1m)
	if err != nil {
		t.Fatalf("DefaultConfigFor(1m) returned error: %v", err)
	}

	// The exact string the seven logged runs recorded in their headers.
	const want = "5m=0.20 15m=0.30 1h=0.50 deadzone=0.15"
	if got := config.Describe(); got != want {
		t.Errorf("the 1m filter now describes itself as:\n  %s\nthe completed runs recorded:\n  %s",
			got, want)
	}

	// And it must survive ForBase unchanged, since that is what the CLI applies.
	adapted, err := config.ForBase(constants.Timeframe1m)
	if err != nil {
		t.Fatalf("ForBase(1m) returned error: %v", err)
	}
	if got := adapted.Describe(); got != want {
		t.Errorf("after ForBase the 1m filter describes itself as:\n  %s\nwant:\n  %s", got, want)
	}
	if len(adapted.Dropped) != 0 {
		t.Errorf("ForBase dropped %v from the 1m set", adapted.Dropped)
	}
}

// TestABaseWithNoUsableContributorSaysSoRatherThanFailing.
//
// Round two made this a hard error, which was right when the alternative was a
// filter that silently gated on nothing. It is wrong when the alternative is
// not running the experiment: three strategies refused to run at 4h and left
// the one cell that could say whether the monotonic trend continues unmeasured.
//
// Both 4h and 1d resolve this way — 4h because 1d cannot warm up above it, 1d
// because nothing is above it at all.
func TestABaseWithNoUsableContributorSaysSoRatherThanFailing(t *testing.T) {
	for _, base := range []constants.Timeframe{
		constants.Timeframe4h, constants.Timeframe1d,
	} {
		_, err := trend.DefaultConfigFor(base)
		if !errors.Is(err, trend.ErrNoUsableContributor) {
			t.Errorf("DefaultConfigFor(%s) returned %v, want ErrNoUsableContributor", base, err)
		}
		if err != nil && !strings.Contains(err.Error(), base.String()) {
			t.Errorf("the %s message does not name the base: %v", base, err)
		}
	}
}

// TestEveryContributorIsStrictlyAboveItsBase. The look-ahead rule from phase
// 05 §1 applies to the table itself: an entry that listed a contributor at or
// below its base would be a hazard shipped as a default.
func TestEveryContributorIsStrictlyAboveItsBase(t *testing.T) {
	for _, base := range trend.DefaultBases() {
		config, err := trend.DefaultConfigFor(base)
		if errors.Is(err, trend.ErrNoUsableContributor) {
			continue
		}
		if err != nil {
			t.Fatalf("DefaultConfigFor(%s) returned error: %v", base, err)
		}
		for _, weight := range config.Weights {
			if weight.Timeframe.Duration() <= base.Duration() {
				t.Errorf("the %s set contains %s, which does not close less often",
					base, weight.Timeframe)
			}
		}
		// ForBase is the enforcement, so it must drop nothing from a correct
		// table. If it ever does, the table is wrong.
		adapted, err := config.ForBase(base)
		if err != nil {
			t.Fatalf("ForBase(%s) returned error: %v", base, err)
		}
		if len(adapted.Dropped) != 0 {
			t.Errorf("ForBase dropped %v from the %s set; the default table is wrong",
				adapted.Dropped, base)
		}
	}
}

// TestTheHighestContributorCarriesTheMostWeight, at every base. The dominant
// trend is the strongest veto by design; a set where the fastest contributor
// outvoted the slowest would be a different filter wearing the same name.
func TestTheHighestContributorCarriesTheMostWeight(t *testing.T) {
	for _, base := range []constants.Timeframe{
		constants.Timeframe1m, constants.Timeframe5m,
		constants.Timeframe15m, constants.Timeframe1h,
	} {
		config, err := trend.DefaultConfigFor(base)
		if err != nil {
			t.Fatalf("DefaultConfigFor(%s) returned error: %v", base, err)
		}
		// Weights are ordered shortest contributor first, so they must ascend.
		for i := 1; i < len(config.Weights); i++ {
			if config.Weights[i].Weight <= config.Weights[i-1].Weight {
				t.Errorf("at base %s, %s (%.3f) does not outweigh %s (%.3f)",
					base,
					config.Weights[i].Timeframe, config.Weights[i].Weight,
					config.Weights[i-1].Timeframe, config.Weights[i-1].Weight)
			}
		}
	}
}

// TestTheProportionsAreTheSameWhateverTheBase. A two-contributor set is the
// top of the three-contributor one renormalised, so 4h:1h at a 15m base is the
// same 5:3 that 1h:15m is at a 1m base.
func TestTheProportionsAreTheSameWhateverTheBase(t *testing.T) {
	fifteen, err := trend.DefaultConfigFor(constants.Timeframe15m)
	if err != nil {
		t.Fatalf("DefaultConfigFor(15m) returned error: %v", err)
	}
	if len(fifteen.Weights) != 2 {
		t.Fatalf("the 15m base has %d contributors, want 2", len(fifteen.Weights))
	}

	ratio := fifteen.Weights[1].Weight / fifteen.Weights[0].Weight
	if math.Abs(ratio-5.0/3.0) > 1e-9 {
		t.Errorf("4h:1h is %v, want the 5:3 the shipped weights use", ratio)
	}
}

// TestASingleContributorTakesTheWholeWeight, so a 4h base is not a filter
// running at half strength without saying so.
func TestASingleContributorTakesTheWholeWeight(t *testing.T) {
	for _, testCase := range []struct {
		base        constants.Timeframe
		contributor constants.Timeframe
	}{
		{constants.Timeframe1h, constants.Timeframe4h},
	} {
		config, err := trend.DefaultConfigFor(testCase.base)
		if err != nil {
			t.Fatalf("DefaultConfigFor(%s) returned error: %v", testCase.base, err)
		}
		if len(config.Weights) != 1 {
			t.Fatalf("the %s base has %d contributors, want 1", testCase.base, len(config.Weights))
		}
		if config.Weights[0].Timeframe != testCase.contributor {
			t.Errorf("the %s base uses %s, want %s",
				testCase.base, config.Weights[0].Timeframe, testCase.contributor)
		}
		if math.Abs(config.Weights[0].Weight-1) > 1e-9 {
			t.Errorf("%s carries %v of a %s filter, want all of it",
				testCase.contributor, config.Weights[0].Weight, testCase.base)
		}
	}
}

// TestEveryContributorCanWarmUpBeforeTheDevelopmentSet is the reason 1d is not
// in the 15m or 1h rows.
//
// The filter says nothing until every contributor has seen
// WarmupMultiplier × EMAPeriod closes, and the aligner starts its cursors that
// far before the range. A contributor whose warm-up reaches back further than
// the collected history can never become ready, and a filter that is never
// ready blocks every entry — which is not a conservative filter, it is a
// broken one that reports zero trades and no reason.
//
// MARKET_BACKFILL_FROM is the development-set start, so the warm-up has to fit
// between the earliest collectable candle and there. This asserts the table
// against that budget rather than against anyone remembering to check.
func TestEveryContributorCanWarmUpBeforeTheDevelopmentSet(t *testing.T) {
	warmupCloses := constants.WarmupMultiplier * _indicator_us.DefaultSetConfig().EMAPeriod

	// How far back candles actually exist. Collection starts at the
	// development-set boundary, so anything earlier than this is history the
	// system does not have.
	budget := backtest.DevFrom.Sub(earliestCollected)

	// Every base in the table, taken from the table.
	//
	// This originally listed four bases by hand. A fifth row was added and the
	// test went on covering four, so a 4h base shipped with a contributor that
	// could never warm up — 100% not-ready and zero trades across all three
	// strategies, in the one cell the evaluation most needed measured. A test
	// that enumerates what it checks will always lag the thing it checks.
	bases := trend.DefaultBases()
	if len(bases) == 0 {
		t.Fatal("the default table is empty; this test is checking nothing")
	}

	for _, base := range bases {
		config, err := trend.DefaultConfigFor(base)
		if errors.Is(err, trend.ErrNoUsableContributor) {
			// A base with no contributor is a legitimate answer: it runs
			// unfiltered. What it must not be is a contributor that cannot
			// warm up, which is what the loop below checks.
			continue
		}
		if err != nil {
			t.Fatalf("DefaultConfigFor(%s) returned error: %v", base, err)
		}
		if len(config.Weights) == 0 {
			t.Errorf("base %s resolved to an empty filter without saying so", base)
		}

		for _, weight := range config.Weights {
			required := time.Duration(warmupCloses) * weight.Timeframe.Duration()
			if required > budget {
				t.Errorf(
					"the %s base uses %s, which needs %.0f days of warm-up before the "+
						"development set begins — only %.0f days of history exist.\n"+
						"A filter that cannot warm up blocks every entry and reports zero "+
						"trades. If the daily series is ever backfilled further, this test "+
						"is what says the contributor may come back.",
					base, weight.Timeframe, required.Hours()/24, budget.Hours()/24)
			}
		}
	}
}
