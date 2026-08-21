package trend_test

import (
	"strings"
	"testing"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/trend"
)

// TestFilterWeightsAreSettablePerTimeframe.
//
// The weights live in a slice rather than a field per timeframe — a map would
// randomise iteration and break the byte-identical report — so they are the
// one part of the configuration a generic mechanism cannot reach, and need
// their own test rather than inheriting one.
func TestFilterWeightsAreSettablePerTimeframe(t *testing.T) {
	base, err := trend.DefaultConfigFor(constants.Timeframe1m)
	if err != nil {
		t.Fatalf("DefaultConfigFor: %v", err)
	}

	tuned, err := base.WithParams(map[string]string{"weight_1h": "0.7"})
	if err != nil {
		t.Fatalf("WithParams: %v", err)
	}

	found := false
	for _, weight := range tuned.Weights {
		if weight.Timeframe != constants.Timeframe1h {
			continue
		}
		found = true
		if weight.Weight != 0.7 {
			t.Errorf("the 1h weight is %v, want 0.7", weight.Weight)
		}
	}
	if !found {
		t.Fatal("the 1h contributor disappeared")
	}

	// Every other contributor keeps its configured share.
	for _, before := range base.Weights {
		if before.Timeframe == constants.Timeframe1h {
			continue
		}
		for _, after := range tuned.Weights {
			if after.Timeframe == before.Timeframe && after.Weight != before.Weight {
				t.Errorf("%s moved from %v to %v without being asked",
					before.Timeframe, before.Weight, after.Weight)
			}
		}
	}
}

// TestTheDeadZoneIsSettable, through the same generic path everything else
// uses.
func TestTheDeadZoneIsSettable(t *testing.T) {
	base, err := trend.DefaultConfigFor(constants.Timeframe1m)
	if err != nil {
		t.Fatalf("DefaultConfigFor: %v", err)
	}

	tuned, err := base.WithParams(map[string]string{"deadzone": "0.25"})
	if err != nil {
		t.Fatalf("WithParams: %v", err)
	}
	if tuned.DeadZone != 0.25 {
		t.Errorf("the dead zone is %v, want 0.25", tuned.DeadZone)
	}
}

// TestAWeightForAnAbsentContributorIsRefused.
//
// The contributor set depends on the run's base timeframe (ADR 0018), so
// weight_5m is a valid key on a 1m run and a mistake on a 1h one. Accepting it
// silently would mean tuning a timeframe that took no part in the numbers.
func TestAWeightForAnAbsentContributorIsRefused(t *testing.T) {
	base, err := trend.DefaultConfigFor(constants.Timeframe1h)
	if err != nil {
		t.Fatalf("DefaultConfigFor: %v", err)
	}

	_, err = base.WithParams(map[string]string{"weight_5m": "0.5"})
	if err == nil {
		t.Fatal("a weight was accepted for a timeframe this run does not use")
	}
	for _, want := range []string{"weight_5m", "deadzone"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not mention %q: %v", want, err)
		}
	}
}

// TestAnUnknownFilterParameterIsRefused, listing what this run's filter takes.
func TestAnUnknownFilterParameterIsRefused(t *testing.T) {
	base, err := trend.DefaultConfigFor(constants.Timeframe1m)
	if err != nil {
		t.Fatalf("DefaultConfigFor: %v", err)
	}

	if _, err := base.WithParams(map[string]string{"dead_zone": "0.2"}); err == nil {
		t.Error("an unknown filter parameter was accepted")
	}
}

// TestTheConfigurationStillValidates. WithParams parses and nothing more, so
// there is one set of rules rather than two that can disagree.
func TestTheConfigurationStillValidates(t *testing.T) {
	base, err := trend.DefaultConfigFor(constants.Timeframe1m)
	if err != nil {
		t.Fatalf("DefaultConfigFor: %v", err)
	}

	for name, override := range map[string]map[string]string{
		"a weight of zero":         {"weight_1h": "0"},
		"a negative weight":        {"weight_1h": "-0.5"},
		"a dead zone of one":       {"deadzone": "1"},
		"a negative dead zone":     {"deadzone": "-0.1"},
		"a weight that is a word":  {"weight_1h": "high"},
		"a dead zone that is text": {"deadzone": "wide"},
	} {
		tuned, err := base.WithParams(override)
		if err != nil {
			continue // rejected at the parse, which is also correct
		}
		if err := tuned.Validate(); err == nil {
			t.Errorf("%s was accepted by both the parse and Validate", name)
		}
	}
}

// TestWithParamsDoesNotMutateTheCaller.
//
// The weights are a slice, so a copied Config shares its backing array — and
// the CLI rebuilds the configuration for every pass of --cost-sweep from the
// same base. Writing through that shared array would leave the second pass
// carrying the first one's overrides.
func TestWithParamsDoesNotMutateTheCaller(t *testing.T) {
	base, err := trend.DefaultConfigFor(constants.Timeframe1m)
	if err != nil {
		t.Fatalf("DefaultConfigFor: %v", err)
	}

	before := make([]float64, len(base.Weights))
	for i, weight := range base.Weights {
		before[i] = weight.Weight
	}

	if _, err := base.WithParams(map[string]string{"weight_1h": "0.9"}); err != nil {
		t.Fatalf("WithParams: %v", err)
	}

	for i, weight := range base.Weights {
		if weight.Weight != before[i] {
			t.Errorf("%s changed from %v to %v in the caller's own configuration",
				weight.Timeframe, before[i], weight.Weight)
		}
	}
}

// TestNoOverridesIsTheSameConfiguration. A run passing no --filter-param must
// be the run that existed before --filter-param did.
func TestNoOverridesIsTheSameConfiguration(t *testing.T) {
	base, err := trend.DefaultConfigFor(constants.Timeframe1m)
	if err != nil {
		t.Fatalf("DefaultConfigFor: %v", err)
	}

	same, err := base.WithParams(nil)
	if err != nil {
		t.Fatalf("WithParams: %v", err)
	}
	if same.Describe() != base.Describe() {
		t.Errorf("the configuration changed with no overrides:\n got %s\nwant %s",
			same.Describe(), base.Describe())
	}
}
