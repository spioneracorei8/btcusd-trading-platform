package trend_test

import (
	"math"
	"strings"
	"testing"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/trend"
)

// TestEachBaseResolvesToItsDocumentedContributors is the table from the fix,
// written out so a change to it has to be deliberate.
func TestEachBaseResolvesToItsDocumentedContributors(t *testing.T) {
	for _, testCase := range []struct {
		base constants.Timeframe
		want string
	}{
		{constants.Timeframe1m, "5m,15m,1h"},
		{constants.Timeframe5m, "15m,1h,4h"},
		{constants.Timeframe15m, "1h,4h,1d"},
		{constants.Timeframe1h, "4h,1d"},
		{constants.Timeframe4h, "1d"},
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

// TestADailyBaseHasNothingAboveIt. Still a hard error, and now a real case
// rather than a theoretical one: 1d is the slowest timeframe this system
// collects.
func TestADailyBaseHasNothingAboveIt(t *testing.T) {
	_, err := trend.DefaultConfigFor(constants.Timeframe1d)
	if err == nil {
		t.Fatal("a 1d base resolved to a contributor set")
	}
	if !strings.Contains(err.Error(), "1d") {
		t.Errorf("the error does not name the base: %v", err)
	}
}

// TestEveryContributorIsStrictlyAboveItsBase. The look-ahead rule from phase
// 05 §1 applies to the table itself: an entry that listed a contributor at or
// below its base would be a hazard shipped as a default.
func TestEveryContributorIsStrictlyAboveItsBase(t *testing.T) {
	for _, base := range []constants.Timeframe{
		constants.Timeframe1m, constants.Timeframe5m, constants.Timeframe15m,
		constants.Timeframe1h, constants.Timeframe4h,
	} {
		config, err := trend.DefaultConfigFor(base)
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
// top of the three-contributor one renormalised, so 1d:4h is the same 5:3 that
// 1h:15m is at a 1m base.
func TestTheProportionsAreTheSameWhateverTheBase(t *testing.T) {
	oneHour, err := trend.DefaultConfigFor(constants.Timeframe1h)
	if err != nil {
		t.Fatalf("DefaultConfigFor(1h) returned error: %v", err)
	}
	if len(oneHour.Weights) != 2 {
		t.Fatalf("the 1h base has %d contributors, want 2", len(oneHour.Weights))
	}

	ratio := oneHour.Weights[1].Weight / oneHour.Weights[0].Weight
	if math.Abs(ratio-5.0/3.0) > 1e-9 {
		t.Errorf("1d:4h is %v, want the 5:3 the shipped weights use", ratio)
	}
}

// TestASingleContributorTakesTheWholeWeight, so a 4h base is not a filter
// running at half strength without saying so.
func TestASingleContributorTakesTheWholeWeight(t *testing.T) {
	config, err := trend.DefaultConfigFor(constants.Timeframe4h)
	if err != nil {
		t.Fatalf("DefaultConfigFor(4h) returned error: %v", err)
	}
	if len(config.Weights) != 1 {
		t.Fatalf("the 4h base has %d contributors, want 1", len(config.Weights))
	}
	if math.Abs(config.Weights[0].Weight-1) > 1e-9 {
		t.Errorf("1d carries %v of a 4h filter, want all of it", config.Weights[0].Weight)
	}
}
