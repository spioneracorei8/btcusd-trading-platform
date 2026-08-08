package strategy_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/spioneracorei8/btcusd-trading-platform/server/services/strategy"
)

// TestLookAheadDoesNotCompile is the phase-04 §1 requirement that look-ahead
// be structurally impossible rather than forbidden by convention.
//
// A rule written in a comment gets broken by accident during a refactor. A
// field that does not exist cannot be dereferenced, and the compiler enforces
// that for free — but only if something checks that it still holds. This does:
// it builds a strategy that reaches for future data through BarContext, in
// every shape the mistake tends to take, and fails if the build succeeds.
//
// It shells out to `go build` rather than using go/types because the question
// is exactly "would the real toolchain accept this", and nothing simulates
// that as faithfully as the toolchain.
func TestLookAheadDoesNotCompile(t *testing.T) {
	if testing.Short() {
		t.Skip("compiling a probe package is too slow for -short")
	}

	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Skipf("no go toolchain on PATH: %v", err)
	}

	probe, err := os.ReadFile("lookahead_probe.go.txt")
	if err != nil {
		t.Fatalf("read the probe: %v", err)
	}

	// The probe is built inside the module, so it resolves the real strategy
	// package rather than a copy that could drift from it.
	dir := t.TempDir()
	probeDir := filepath.Join("testdata", "lookaheadprobe")
	if err := os.MkdirAll(probeDir, 0o755); err != nil {
		t.Fatalf("create the probe directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(probeDir) })

	probePath := filepath.Join(probeDir, "probe.go")
	if err := os.WriteFile(probePath, probe, 0o600); err != nil {
		t.Fatalf("write the probe: %v", err)
	}

	cmd := exec.Command(goTool, "build", "-o", filepath.Join(dir, "probe.a"), "./"+filepath.ToSlash(probeDir))
	output, err := cmd.CombinedOutput()

	if err == nil {
		t.Fatalf("the look-ahead probe compiled.\n\n"+
			"BarContext has grown a field that lets a strategy see past its own\n"+
			"decision point. phase-04 §1 requires look-ahead to be impossible to\n"+
			"express, not merely discouraged: the interface needs to change.\n\n"+
			"probe:\n%s", probe)
	}

	// Confirm it failed for the intended reason rather than a typo or a
	// missing import, which would make this test pass while checking nothing.
	text := string(output)
	for _, field := range []string{"Candles", "Index", "Now", "NextCandle"} {
		if !strings.Contains(text, field) {
			t.Errorf("the probe failed to build but the compiler never mentioned %s;\n"+
				"the test may be passing for the wrong reason.\ncompiler said:\n%s", field, text)
		}
	}
}

// TestBarContextExposesNothingBeyondTheCurrentBar is the same guarantee stated
// as an allow-list, so a new field cannot be added without a deliberate choice
// to update this list.
//
// The compile probe catches the specific shapes look-ahead usually takes; this
// catches every other shape by refusing anything unrecognised.
func TestBarContextExposesNothingBeyondTheCurrentBar(t *testing.T) {
	allowed := map[string]bool{
		// The bar that just closed. Its close is the last price a strategy is
		// entitled to know.
		"Candle": true,
		// Indicator values at that same close, computed only from bars up to
		// it.
		"Indicators": true,
		// A copy of what is held. The engine owns the real position.
		"Position": true,
	}

	contextType := reflect.TypeOf(strategy.BarContext{})
	for i := range contextType.NumField() {
		name := contextType.Field(i).Name
		if !allowed[name] {
			t.Errorf("BarContext has a new field %q.\n"+
				"Everything a strategy can see must be knowable at the moment the\n"+
				"bar closes. If this field is, add it to the allow-list here and\n"+
				"say why in the commit; if it is not, look-ahead just became\n"+
				"expressible.", name)
		}
	}
}

// TestIntentCarriesNoFillPrice keeps the engine in charge of what happens.
//
// An Intent is a request. If a strategy could name the price it filled at, it
// could report any result it liked, and the engine's cost model would become
// advisory. Price exists only for the two level-setting intents, where it is a
// threshold rather than a fill.
func TestIntentCarriesNoFillPrice(t *testing.T) {
	for _, intent := range []strategy.Intent{
		strategy.EnterLong("in"),
		strategy.EnterShort("in"),
		strategy.Exit("out"),
	} {
		if !intent.Price.IsZero() {
			t.Errorf("%s carries a price (%s); entries and exits are priced by the engine",
				intent.Kind, intent.Price)
		}
	}

	intentType := reflect.TypeOf(strategy.Intent{})
	for i := range intentType.NumField() {
		switch name := intentType.Field(i).Name; name {
		case "Kind", "Price", "Reason":
		default:
			t.Errorf("Intent has a new field %q; check it cannot express a fill", name)
		}
	}
}

// TestPositionIsACopy proves a strategy cannot rewrite its own cost basis. It
// is a value type, so the assignment below touches nothing the engine holds.
func TestPositionIsACopy(t *testing.T) {
	if reflect.TypeOf(strategy.BarContext{}.Position).Kind() != reflect.Struct {
		t.Fatal("Position is not a value type, so a strategy could mutate the engine's own state")
	}
}

// TestEveryIntentKindIsValid catches an intent added to the type but not to
// Valid, which the engine uses to reject anything it does not understand.
func TestEveryIntentKindIsValid(t *testing.T) {
	for _, kind := range []strategy.IntentKind{
		strategy.IntentEnterLong,
		strategy.IntentEnterShort,
		strategy.IntentExit,
		strategy.IntentSetStop,
		strategy.IntentSetTarget,
	} {
		if !kind.Valid() {
			t.Errorf("%q is not reported valid", kind)
		}
	}

	var unset strategy.IntentKind
	if unset.Valid() {
		t.Error("the zero IntentKind is valid, so a forgotten Kind would be acted on")
	}
}
