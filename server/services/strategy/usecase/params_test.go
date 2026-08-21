package usecase_test

import (
	"strings"
	"testing"

	"github.com/spioneracorei8/btcusd-trading-platform/server/helper"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/strategy"
	_strategy_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/strategy/usecase"
)

// TestEveryConfigurationFieldDeclaresAParameter is the drift guard, applied to
// the real configurations rather than a fixture.
//
// DescribeParams refuses a struct with an untagged exported field, so this
// fails the moment somebody adds a field and does not decide whether it is a
// parameter. The alternative — a hand-maintained list — goes stale silently,
// and an unreachable parameter is indistinguishable from one withheld on
// purpose.
func TestEveryConfigurationFieldDeclaresAParameter(t *testing.T) {
	for _, entry := range _strategy_us.All() {
		specs, err := entry.Params()
		if err != nil {
			t.Errorf("%s: %v", entry.Name, err)
			continue
		}
		if len(specs) == 0 {
			t.Errorf("%s exposes no parameters at all", entry.Name)
		}

		for _, spec := range specs {
			if spec.Name == "" {
				t.Errorf("%s has a parameter with no name", entry.Name)
			}
			if spec.Kind == "" {
				t.Errorf("%s: %s has no type", entry.Name, spec.Name)
			}
		}
	}
}

// TestDerivedValuesAreNotParameters.
//
// RoundTripCostPct comes from the cost model and LongOnly from the market
// type. A run that could set either could be configured to contradict the
// venue it claims to model: a spot run taking shorts, or a strategy validated
// against a fee it does not pay.
func TestDerivedValuesAreNotParameters(t *testing.T) {
	for _, entry := range _strategy_us.All() {
		for _, name := range []string{"round_trip_cost_pct", "roundtripcostpct", "long_only", "longonly"} {
			_, _, err := entry.BuildWith(map[string]string{name: "1"}, 0.1, false)
			if err == nil {
				t.Errorf("%s accepted %q, which is derived and must not be settable", entry.Name, name)
			}
		}
	}
}

// TestAnUnknownParameterNamesTheValidOnes, per strategy: the valid set differs
// between them, and a message listing another strategy's keys would send the
// reader looking for a flag that does not exist here.
func TestAnUnknownParameterNamesTheValidOnes(t *testing.T) {
	for _, entry := range _strategy_us.All() {
		_, _, err := entry.BuildWith(map[string]string{"no_such_knob": "1"}, 0.1, false)
		if err == nil {
			t.Fatalf("%s accepted an unknown parameter", entry.Name)
		}
		if !strings.Contains(err.Error(), entry.Name) {
			t.Errorf("%s: the error does not say which strategy rejected it: %v", entry.Name, err)
		}

		specs, _ := entry.Params()
		if !strings.Contains(err.Error(), specs[0].Name) {
			t.Errorf("%s: the error does not list the valid keys: %v", entry.Name, err)
		}
	}
}

// TestTheConstructorStillValidates. --param parses values and nothing more, so
// there is one set of rules rather than two that can disagree.
func TestTheConstructorStillValidates(t *testing.T) {
	for name, override := range map[string]map[string]string{
		// A target below the stop: right most of the time merely to break even.
		"reward below risk": {"stop_atr_mult": "3", "target_atr_mult": "1"},
		// A target that does not clear the round trip at the reference ATR.
		"reward below cost": {"stop_atr_mult": "0.4", "target_atr_mult": "0.5"},
		// An EMA period that cannot form an average.
		"a period of one": {"fast": "1", "slow": "1"},
	} {
		entry, err := _strategy_us.Lookup("ema_crossover")
		if err != nil {
			t.Fatalf("Lookup: %v", err)
		}
		if _, _, err := entry.BuildWith(override, 0.1, false); err == nil {
			t.Errorf("%s was accepted; the constructor's own rules were bypassed", name)
		}
	}
}

// TestAParameterReachesTheStrategy asserts behaviour, not storage.
//
// A value that is parsed, recorded in the header and never read would pass
// every other test here and change nothing about the run — which is precisely
// the failure the leverage defect turned out to be. So this drives two
// differently-configured instances over the same bars and requires that they
// disagree.
func TestAParameterReachesTheStrategy(t *testing.T) {
	for _, tc := range []struct {
		strategy string
		override map[string]string
	}{
		{"ema_crossover", map[string]string{"fast": "3", "slow": "40"}},
		{"ema_crossover", map[string]string{"stop_atr_mult": "0.5", "target_atr_mult": "4"}},
		{"rsi_reversion", map[string]string{"oversold": "45", "overbought": "55"}},
		{"trend_pullback", map[string]string{"trend_period": "10", "pullback_atr": "2"}},
		{"mtf_alignment", map[string]string{"trigger_period": "10", "pullback_atr": "3"}},
	} {
		entry, err := _strategy_us.Lookup(tc.strategy)
		if err != nil {
			t.Fatalf("Lookup(%s): %v", tc.strategy, err)
		}

		base, err := entry.Build(0.1, false)
		if err != nil {
			t.Fatalf("%s at defaults: %v", tc.strategy, err)
		}
		tuned, _, err := entry.BuildWith(tc.override, 0.1, false)
		if err != nil {
			t.Fatalf("%s with %v: %v", tc.strategy, tc.override, err)
		}

		if intentsMatch(driveOverProvokingSeries(base), driveOverProvokingSeries(tuned)) {
			t.Errorf("%s produced identical intents at defaults and with %v; "+
				"the parameter is recorded but never read", tc.strategy, tc.override)
		}
	}
}

// intentsMatch reports whether two runs produced the same decisions.
func intentsMatch(a, b []strategy.Intent) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Kind != b[i].Kind || !a[i].Stop.Equal(b[i].Stop) ||
			!a[i].Target.Equal(b[i].Target) || a[i].Reason != b[i].Reason {
			return false
		}
	}
	return true
}

// TestDescribeIsDerivedFromTheParameters.
//
// It used to be a hand-written format string listing each default — a second
// copy of the same facts, which drifts from the first the moment a default
// changes. Deriving it means a wrong description is not expressible.
func TestDescribeIsDerivedFromTheParameters(t *testing.T) {
	for _, entry := range _strategy_us.All() {
		description := entry.Describe()
		if description == "" {
			t.Errorf("%s describes itself as nothing", entry.Name)
			continue
		}

		specs, err := entry.Params()
		if err != nil {
			t.Fatalf("%s: %v", entry.Name, err)
		}
		for _, spec := range specs {
			if !strings.Contains(description, spec.Name+"="+spec.Default) {
				t.Errorf("%s does not describe %s at its default %s: %s",
					entry.Name, spec.Name, spec.Default, description)
			}
		}
	}
}

// TestBuildWithNoOverridesEqualsBuild. A run that passes no --param must be the
// run that existed before --param did.
func TestBuildWithNoOverridesEqualsBuild(t *testing.T) {
	for _, entry := range _strategy_us.All() {
		plain, err := entry.Build(0.1, false)
		if err != nil {
			t.Fatalf("%s: %v", entry.Name, err)
		}
		viaParams, _, err := entry.BuildWith(nil, 0.1, false)
		if err != nil {
			t.Fatalf("%s: %v", entry.Name, err)
		}

		if !intentsMatch(driveOverProvokingSeries(plain), driveOverProvokingSeries(viaParams)) {
			t.Errorf("%s behaves differently when built through the parameter path", entry.Name)
		}

		changed, err := helper.ChangedParams(entry.Defaults(), entry.Defaults())
		if err != nil {
			t.Fatalf("%s: %v", entry.Name, err)
		}
		if len(changed) != 0 {
			t.Errorf("%s reports %d changed parameters with none set", entry.Name, len(changed))
		}
	}
}
