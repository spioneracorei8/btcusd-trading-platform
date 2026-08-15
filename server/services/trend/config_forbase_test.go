package trend_test

import (
	"math"
	"strings"
	"testing"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/trend"
)

// timeframesOf lists a config's surviving contributors.
func timeframesOf(config trend.Config) []string {
	out := make([]string, 0, len(config.Weights))
	for _, weight := range config.Weights {
		out = append(out, weight.Timeframe.String())
	}
	return out
}

func joined(values []string) string { return strings.Join(values, ",") }

func droppedOf(config trend.Config) []string {
	out := make([]string, 0, len(config.Dropped))
	for _, timeframe := range config.Dropped {
		out = append(out, timeframe.String())
	}
	return out
}

// TestContributorsAtOrBelowTheBaseAreDroppedNotFatal is the fix.
//
// The look-ahead rule is right that a 5m contributor cannot inform a 15m
// decision. It was wrong to treat that as a broken configuration: the sensible
// reading is that this contributor has nothing to say at this base and the
// others still do. Treating it as fatal made the filter unusable at every base
// except 1m — the one timeframe the evaluation says to move away from.
func TestContributorsAtOrBelowTheBaseAreDroppedNotFatal(t *testing.T) {
	adapted, err := trend.DefaultConfig().ForBase(constants.Timeframe15m)
	if err != nil {
		t.Fatalf("ForBase(15m) returned error: %v", err)
	}

	if got := joined(timeframesOf(adapted)); got != "1h" {
		t.Errorf("survivors are %q, want only 1h", got)
	}
	// 15m itself is dropped too: a contributor that closes exactly as often as
	// the base is the same hazard as one that closes more often.
	if got := joined(droppedOf(adapted)); got != "5m,15m" {
		t.Errorf("dropped %q, want 5m,15m", got)
	}
}

// TestDroppingAContributorDoesNotShrinkTheFilter. The filter divides by
// TotalWeight, so survivors normalised over the original total would leave the
// aggregate scaled down without anything saying so.
func TestDroppingAContributorDoesNotShrinkTheFilter(t *testing.T) {
	original := trend.DefaultConfig()

	for _, base := range []constants.Timeframe{
		constants.Timeframe1m, constants.Timeframe5m, constants.Timeframe15m,
	} {
		adapted, err := trend.DefaultConfig().ForBase(base)
		if err != nil {
			t.Fatalf("ForBase(%s) returned error: %v", base, err)
		}
		if math.Abs(adapted.TotalWeight()-original.TotalWeight()) > 1e-9 {
			t.Errorf("at base %s the total weight is %v, want the configured %v",
				base, adapted.TotalWeight(), original.TotalWeight())
		}
		if err := adapted.Validate(); err != nil {
			t.Errorf("at base %s the adapted config does not validate: %v", base, err)
		}
	}
}

// TestTheSurvivorsKeepTheirRelativeShares. Rescaling must not reorder the
// contributors' importance — 1h outweighing 15m is a design decision, not an
// artefact of which base happened to be chosen.
func TestTheSurvivorsKeepTheirRelativeShares(t *testing.T) {
	adapted, err := trend.DefaultConfig().ForBase(constants.Timeframe5m)
	if err != nil {
		t.Fatalf("ForBase(5m) returned error: %v", err)
	}
	if got := joined(timeframesOf(adapted)); got != "15m,1h" {
		t.Fatalf("survivors are %q, want 15m,1h", got)
	}

	// Configured 0.3 and 0.5 — a ratio of 5/3, which rescaling preserves.
	ratio := adapted.Weights[1].Weight / adapted.Weights[0].Weight
	if math.Abs(ratio-5.0/3.0) > 1e-9 {
		t.Errorf("1h:15m is %v, want the configured %v", ratio, 5.0/3.0)
	}
}

// TestABaseAboveEveryContributorIsAnError. A filter with nothing above the
// base cannot contribute anything, and pretending to filter is worse than not
// filtering: the run would report a filter in its header and gate on nothing.
func TestABaseAboveEveryContributorIsAnError(t *testing.T) {
	_, err := trend.DefaultConfig().ForBase(constants.Timeframe1h)
	if err == nil {
		t.Fatal("a 1h base against 5m/15m/1h contributors was accepted")
	}
	for _, want := range []string{"1h", "5m, 15m, 1h"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not mention %q: %v", want, err)
		}
	}
}

// TestTheOneMinuteBaseIsUnchanged, so this fix cannot have altered any result
// produced before it.
func TestTheOneMinuteBaseIsUnchanged(t *testing.T) {
	original := trend.DefaultConfig()
	adapted, err := original.ForBase(constants.Timeframe1m)
	if err != nil {
		t.Fatalf("ForBase(1m) returned error: %v", err)
	}

	if len(adapted.Dropped) != 0 {
		t.Errorf("1m dropped %v; every contributor is above a 1m base", adapted.Dropped)
	}
	if len(adapted.Weights) != len(original.Weights) {
		t.Fatalf("1m kept %d contributors, want %d", len(adapted.Weights), len(original.Weights))
	}
	for i, weight := range adapted.Weights {
		if weight.Timeframe != original.Weights[i].Timeframe {
			t.Errorf("contributor %d is %s, want %s", i, weight.Timeframe, original.Weights[i].Timeframe)
		}
		if math.Abs(weight.Weight-original.Weights[i].Weight) > 1e-9 {
			t.Errorf("%s weighs %v, want the configured %v",
				weight.Timeframe, weight.Weight, original.Weights[i].Weight)
		}
	}
	if adapted.Describe() != original.Describe() {
		t.Errorf("the description changed:\n got %s\nwant %s", adapted.Describe(), original.Describe())
	}
}

// TestADroppedContributorCannotReachTheFilter.
//
// The header saying a contributor was dropped is worth nothing if the filter
// still reads it. Weights is the only thing the filter iterates, so a dropped
// timeframe being absent from it is what makes the drop real.
func TestADroppedContributorCannotReachTheFilter(t *testing.T) {
	adapted, err := trend.DefaultConfig().ForBase(constants.Timeframe15m)
	if err != nil {
		t.Fatalf("ForBase(15m) returned error: %v", err)
	}

	for _, weight := range adapted.Weights {
		for _, dropped := range adapted.Dropped {
			if weight.Timeframe == dropped {
				t.Errorf("%s is both dropped and weighted", dropped)
			}
		}
	}
	// Timeframes() is what the aligner is built from, so a dropped contributor
	// appearing here would open a cursor for it and re-admit the hazard.
	if got := joined(timeframesOf(adapted)); strings.Contains(got, "5m") {
		t.Errorf("the aligner would still be given 5m: %q", got)
	}
}

// TestTheDropIsStatedInTheHeader. Silently discarding part of a configuration
// is its own defect: a reader must be able to see that the contributor they
// configured took no part in the numbers in front of them.
func TestTheDropIsStatedInTheHeader(t *testing.T) {
	adapted, err := trend.DefaultConfig().ForBase(constants.Timeframe15m)
	if err != nil {
		t.Fatalf("ForBase(15m) returned error: %v", err)
	}

	description := adapted.Describe()
	for _, want := range []string{"dropped", "5m", "15m", "1h=1.00", "rescaled"} {
		if !strings.Contains(description, want) {
			t.Errorf("the header text does not mention %q: %s", want, description)
		}
	}
}
