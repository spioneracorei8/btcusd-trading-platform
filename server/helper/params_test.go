package helper_test

import (
	"strings"
	"testing"

	"github.com/spioneracorei8/btcusd-trading-platform/server/helper"
)

// levels mirrors the shape a strategy's levels have: a nested struct whose
// fields are flattened into the parameter namespace.
type levels struct {
	StopATRMult   float64 `param:"stop_atr_mult,step=0.25"`
	TargetATRMult float64 `param:"target_atr_mult,step=0.25"`
}

type sample struct {
	Fast     int      `param:"fast,step=1"`
	Ratio    float64  `param:"ratio,step=0.1"`
	Enabled  bool     `param:"enabled"`
	Name     string   `param:"name"`
	Items    []string `param:"items"`
	Levels   levels   `param:",inline"`
	Derived  float64  `param:"-"`
	unexpose int
}

func newSample() *sample {
	return &sample{
		Fast: 9, Ratio: 1.5, Enabled: true, Name: "4h",
		Items:  []string{"15m", "1h"},
		Levels: levels{StopATRMult: 1.5, TargetATRMult: 3},
	}
}

// TestEveryParameterIsDescribedWithItsTypeAndDefault covers what
// --list-strategies prints.
func TestEveryParameterIsDescribedWithItsTypeAndDefault(t *testing.T) {
	specs, err := helper.DescribeParams(newSample())
	if err != nil {
		t.Fatalf("DescribeParams() returned error: %v", err)
	}

	want := []helper.ParamSpec{
		{Name: "fast", Kind: helper.ParamInt, Default: "9", Step: "1"},
		{Name: "ratio", Kind: helper.ParamFloat, Default: "1.5", Step: "0.1"},
		{Name: "enabled", Kind: helper.ParamBool, Default: "true"},
		{Name: "name", Kind: helper.ParamString, Default: "4h"},
		{Name: "items", Kind: helper.ParamList, Default: "15m,1h"},
		{Name: "stop_atr_mult", Kind: helper.ParamFloat, Default: "1.5", Step: "0.25"},
		{Name: "target_atr_mult", Kind: helper.ParamFloat, Default: "3", Step: "0.25"},
	}
	if len(specs) != len(want) {
		t.Fatalf("described %d parameters, want %d: %+v", len(specs), len(want), specs)
	}
	for i, spec := range specs {
		if spec.Name != want[i].Name || spec.Kind != want[i].Kind ||
			spec.Default != want[i].Default || spec.Step != want[i].Step {
			t.Errorf("parameter %d is %+v, want %+v", i, spec, want[i])
		}
	}
}

// TestADerivedFieldIsNotSettable.
//
// RoundTripCostPct and LongOnly come from the cost model and the market type.
// A run that could set them could be configured to contradict the venue it
// claims to model — a spot run taking shorts, or a strategy validated against
// a fee it does not pay.
func TestADerivedFieldIsNotSettable(t *testing.T) {
	config := newSample()

	err := helper.ApplyParams(config, map[string]string{"derived": "5"})
	if err == nil {
		t.Fatal("a field marked `param:\"-\"` was settable")
	}
	if !strings.Contains(err.Error(), "unknown parameter") {
		t.Errorf("the error does not say the key is unknown: %v", err)
	}
	for _, name := range helper.ParamNames(config) {
		if name == "derived" {
			t.Error("a derived field is listed as a valid parameter")
		}
	}
}

// TestAnUntaggedFieldIsRefused is the drift guard.
//
// A hand-written table of parameters goes stale the moment a field is added
// and nobody updates it — and an unreachable parameter looks exactly like one
// that was withheld on purpose. Refusing the whole struct turns that omission
// into a failure of the test that walks every configuration.
func TestAnUntaggedFieldIsRefused(t *testing.T) {
	type untagged struct {
		Fast  int `param:"fast,step=1"`
		Added float64
	}

	_, err := helper.DescribeParams(&untagged{})
	if err == nil {
		t.Fatal("a struct with an untagged exported field was accepted")
	}
	if !strings.Contains(err.Error(), "Added") {
		t.Errorf("the error does not name the offending field: %v", err)
	}
}

// TestAnUnknownKeyNamesTheValidOnes. A silently ignored typo means running the
// default while believing otherwise, which no report produced afterwards could
// reveal.
func TestAnUnknownKeyNamesTheValidOnes(t *testing.T) {
	err := helper.ApplyParams(newSample(), map[string]string{"fastt": "12"})
	if err == nil {
		t.Fatal("an unknown parameter was accepted")
	}
	for _, name := range []string{"fast", "ratio", "stop_atr_mult", "target_atr_mult"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("the error does not offer %q as a valid key: %v", name, err)
		}
	}
}

// TestEveryTypeRoundTrips through parse and back to its rendered form.
func TestEveryTypeRoundTrips(t *testing.T) {
	config := newSample()

	err := helper.ApplyParams(config, map[string]string{
		"fast":          "12",
		"ratio":         "2.25",
		"enabled":       "false",
		"name":          "1d",
		"items":         "5m, 15m ,1h",
		"stop_atr_mult": "2",
	})
	if err != nil {
		t.Fatalf("ApplyParams() returned error: %v", err)
	}

	if config.Fast != 12 || config.Ratio != 2.25 || config.Enabled || config.Name != "1d" {
		t.Errorf("scalars did not round trip: %+v", config)
	}
	if strings.Join(config.Items, ",") != "5m,15m,1h" {
		t.Errorf("items are %v, want 5m,15m,1h with the spaces trimmed", config.Items)
	}
	if config.Levels.StopATRMult != 2 {
		t.Errorf("the inlined stop is %v, want 2", config.Levels.StopATRMult)
	}
	// Untouched parameters keep their defaults.
	if config.Levels.TargetATRMult != 3 {
		t.Errorf("an unmentioned parameter changed to %v", config.Levels.TargetATRMult)
	}
}

// TestAnUnparseableValueIsRejected, naming the parameter rather than only the
// value, so the message says which of several flags was wrong.
func TestAnUnparseableValueIsRejected(t *testing.T) {
	for name, override := range map[string]map[string]string{
		"a word where a number belongs": {"fast": "nine"},
		"a number that is not a float":  {"ratio": "half"},
		"a bool that is neither":        {"enabled": "yes please"},
	} {
		err := helper.ApplyParams(newSample(), override)
		if err == nil {
			t.Errorf("%s was accepted", name)
			continue
		}
		for key := range override {
			if !strings.Contains(err.Error(), key) {
				t.Errorf("%s: the error does not name %q: %v", name, key, err)
			}
		}
	}
}

// TestChangedParamsComparesValuesNotIntent.
//
// A parameter passed at its own default is not a change. The header answers
// "what is different about this run", not "what did somebody type", and the
// two differ exactly when someone spells out a default to be explicit.
func TestChangedParamsComparesValuesNotIntent(t *testing.T) {
	defaults := newSample()
	configured := newSample()

	if err := helper.ApplyParams(configured, map[string]string{
		"fast":  "9",  // the default, spelled out
		"ratio": "99", // a real change
	}); err != nil {
		t.Fatalf("ApplyParams() returned error: %v", err)
	}

	changes, err := helper.ChangedParams(defaults, configured)
	if err != nil {
		t.Fatalf("ChangedParams() returned error: %v", err)
	}
	if len(changes) != 1 {
		t.Fatalf("reported %d changes, want 1: %+v", len(changes), changes)
	}
	if changes[0].Name != "ratio" || changes[0].From != "1.5" || changes[0].To != "99" {
		t.Errorf("change is %+v, want ratio 1.5 -> 99", changes[0])
	}
}

// TestStepMovesAParameterByItsDeclaredStep, in its own units.
//
// A blanket rule — ten percent, say — asks a different question at different
// magnitudes: ten percent of an EMA period of 200 is twenty bars, and of a 0.5
// ATR multiple is 0.05. Neither is what a human means by "one step either
// side".
func TestStepMovesAParameterByItsDeclaredStep(t *testing.T) {
	for _, tc := range []struct {
		name      string
		direction int
		check     func(*sample) (got, want float64)
	}{
		{"fast", -1, func(s *sample) (float64, float64) { return float64(s.Fast), 8 }},
		{"fast", +1, func(s *sample) (float64, float64) { return float64(s.Fast), 10 }},
		{"ratio", +1, func(s *sample) (float64, float64) { return s.Ratio, 1.6 }},
		{"stop_atr_mult", -1, func(s *sample) (float64, float64) { return s.Levels.StopATRMult, 1.25 }},
	} {
		config := newSample()
		if err := helper.StepParam(config, tc.name, tc.direction); err != nil {
			t.Fatalf("%s%+d: %v", tc.name, tc.direction, err)
		}

		got, want := tc.check(config)
		if diff := got - want; diff > 1e-9 || diff < -1e-9 {
			t.Errorf("%s%+d gave %v, want %v", tc.name, tc.direction, got, want)
		}
	}
}

// TestAParameterWithNoStepCannotBeVaried.
//
// A neighbourhood table missing a row reads as a neighbour that was tested and
// behaved the same, so refusing loudly is the only honest answer for a
// parameter with no neighbouring value.
func TestAParameterWithNoStepCannotBeVaried(t *testing.T) {
	for _, name := range []string{"enabled", "name", "items"} {
		if err := helper.StepParam(newSample(), name, 1); err == nil {
			t.Errorf("%s was stepped, but it has no neighbouring value", name)
		}
	}
}

// TestOverridesDoNotLeakBetweenConfigurations. Each call gets its own struct,
// so a second run in the same process does not inherit the first one's values.
func TestOverridesDoNotLeakBetweenConfigurations(t *testing.T) {
	first := newSample()
	if err := helper.ApplyParams(first, map[string]string{"fast": "50"}); err != nil {
		t.Fatalf("ApplyParams() returned error: %v", err)
	}
	if second := newSample(); second.Fast != 9 {
		t.Errorf("a fresh configuration starts at fast=%d, want the default 9", second.Fast)
	}
}

// TestAValueThatLandsOnItsDefaultIsStillReported.
//
// This is the bug ParamValues exists for. resume_bars=1 stepped up is 2, which
// is the documented default — so it differs from nothing, and a caller that
// derived the run's parameters from ChangedParams would drop it and fall back
// to the value it started from. The neighbourhood table then showed a row
// labelled +1 holding the base's value and, of course, the base's numbers:
// a neighbour reported as behaving identically because it was never run.
func TestAValueThatLandsOnItsDefaultIsStillReported(t *testing.T) {
	config := newSample()
	if err := helper.ApplyParams(config, map[string]string{"fast": "8"}); err != nil {
		t.Fatalf("ApplyParams() returned error: %v", err)
	}
	if err := helper.StepParam(config, "fast", +1); err != nil {
		t.Fatalf("StepParam() returned error: %v", err)
	}

	// Back at the default, so nothing differs...
	changed, err := helper.ChangedParams(newSample(), config)
	if err != nil {
		t.Fatalf("ChangedParams() returned error: %v", err)
	}
	for _, change := range changed {
		if change.Name == "fast" {
			t.Fatalf("precondition: fast is back at its default and should differ from nothing, got %+v", change)
		}
	}

	// ...and the value is still readable.
	values, err := helper.ParamValues(config)
	if err != nil {
		t.Fatalf("ParamValues() returned error: %v", err)
	}
	if values["fast"] != "9" {
		t.Errorf("fast reads as %q after stepping 8 up by one, want \"9\"", values["fast"])
	}
}

// TestParamValuesCoversEveryParameter, so a caller can rebuild a whole
// configuration from it rather than only the interesting part.
func TestParamValuesCoversEveryParameter(t *testing.T) {
	values, err := helper.ParamValues(newSample())
	if err != nil {
		t.Fatalf("ParamValues() returned error: %v", err)
	}

	specs, err := helper.DescribeParams(newSample())
	if err != nil {
		t.Fatalf("DescribeParams() returned error: %v", err)
	}
	if len(values) != len(specs) {
		t.Fatalf("read %d values for %d parameters", len(values), len(specs))
	}
	for _, spec := range specs {
		if values[spec.Name] != spec.Default {
			t.Errorf("%s reads as %q, want %q", spec.Name, values[spec.Name], spec.Default)
		}
	}
}
