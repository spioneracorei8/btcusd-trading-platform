package report_test

import (
	"bytes"
	"math"
	"strings"
	"testing"

	"github.com/spioneracorei8/btcusd-trading-platform/server/services/backtest/report"
)

// neighbourhoodOf renders a table and returns it.
func neighbourhoodOf(t *testing.T, columns []string, rows []report.NeighbourResult) string {
	t.Helper()

	var buf bytes.Buffer
	if err := report.WriteNeighbourhood(&buf, columns, rows); err != nil {
		t.Fatalf("WriteNeighbourhood() returned error: %v", err)
	}
	return buf.String()
}

// TestTheTableShowsEveryVariedParameterOnEveryRow.
//
// A row that named only the parameter it changed would leave a reader deriving
// the rest, which is exactly the kind of arithmetic that gets done wrong when
// two parameters were varied together.
func TestTheTableShowsEveryVariedParameterOnEveryRow(t *testing.T) {
	text := neighbourhoodOf(t, []string{"fast", "slow"}, []report.NeighbourResult{
		{Label: "base", Values: []string{"9", "21"}, NetReturn: 0.0722, ProfitFactor: 1.13, TradeCount: 210},
		{Label: "fast-1", Values: []string{"8", "21"}, NetReturn: 0.0680, ProfitFactor: 1.11, TradeCount: 214},
		{Label: "fast+1", Values: []string{"10", "21"}, NetReturn: 0.0751, ProfitFactor: 1.14, TradeCount: 205},
	})

	for _, want := range []string{"fast", "slow", "base", "fast-1", "fast+1", "PF", "net return", "trades"} {
		if !strings.Contains(text, want) {
			t.Errorf("the table does not show %q:\n%s", want, text)
		}
	}
	// The unvaried column still carries its value on every row.
	if strings.Count(text, "21") < 3 {
		t.Errorf("slow=21 is not repeated on each row:\n%s", text)
	}
}

// TestTheReadingIsPrintedEveryTime.
//
// It used to appear only when the chosen value stood alone — the one case a
// reader would probably have noticed unaided. The reading that gets forgotten
// is the other one: a base row that looks good, neighbours that look broadly
// similar, and nobody stopping to ask which shape they are looking at.
func TestTheReadingIsPrintedEveryTime(t *testing.T) {
	for name, rows := range map[string][]report.NeighbourResult{
		"a plateau": {
			{Label: "base", Values: []string{"9"}, NetReturn: 0.07, ProfitFactor: 1.13, TradeCount: 210},
			{Label: "fast-1", Values: []string{"8"}, NetReturn: 0.068, ProfitFactor: 1.11, TradeCount: 214},
			{Label: "fast+1", Values: []string{"10"}, NetReturn: 0.075, ProfitFactor: 1.14, TradeCount: 205},
		},
		"a spike": {
			{Label: "base", Values: []string{"9"}, NetReturn: 0.07, ProfitFactor: 1.13, TradeCount: 210},
			{Label: "fast-1", Values: []string{"8"}, NetReturn: -0.04, ProfitFactor: 0.9, TradeCount: 214},
			{Label: "fast+1", Values: []string{"10"}, NetReturn: -0.02, ProfitFactor: 0.95, TradeCount: 205},
		},
	} {
		text := neighbourhoodOf(t, []string{"fast"}, rows)
		for _, want := range []string{"plateau", "spike"} {
			if !strings.Contains(text, want) {
				t.Errorf("%s: the reading does not mention %q:\n%s", name, want, text)
			}
		}
	}
}

// TestASpikeIsCalledOut, in addition to the general reading, because a chosen
// value that is profitable while not one neighbour is has a specific finding
// attached: stop believing it.
func TestASpikeIsCalledOut(t *testing.T) {
	spike := neighbourhoodOf(t, []string{"fast"}, []report.NeighbourResult{
		{Label: "base", Values: []string{"9"}, NetReturn: 0.07, ProfitFactor: 1.13, TradeCount: 210},
		{Label: "fast-1", Values: []string{"8"}, NetReturn: -0.04, ProfitFactor: 0.9, TradeCount: 214},
		{Label: "fast+1", Values: []string{"10"}, NetReturn: -0.02, ProfitFactor: 0.95, TradeCount: 205},
	})
	if !strings.Contains(spike, "***") {
		t.Errorf("an isolated value is not called out:\n%s", spike)
	}

	plateau := neighbourhoodOf(t, []string{"fast"}, []report.NeighbourResult{
		{Label: "base", Values: []string{"9"}, NetReturn: 0.07, ProfitFactor: 1.13, TradeCount: 210},
		{Label: "fast-1", Values: []string{"8"}, NetReturn: 0.068, ProfitFactor: 1.11, TradeCount: 214},
	})
	if strings.Contains(plateau, "***") {
		t.Errorf("a plateau was called out as a spike:\n%s", plateau)
	}
}

// TestNothingIsRankedOrRecommended.
//
// Phase 06 puts automated optimisation out of scope, and a tool that picked
// the winner would industrialise the exact mistake the table exists to expose.
func TestNothingIsRankedOrRecommended(t *testing.T) {
	text := neighbourhoodOf(t, []string{"fast"}, []report.NeighbourResult{
		{Label: "base", Values: []string{"9"}, NetReturn: 0.07, ProfitFactor: 1.13, TradeCount: 210},
		{Label: "fast+1", Values: []string{"10"}, NetReturn: 0.42, ProfitFactor: 2.0, TradeCount: 205},
	})

	for _, forbidden := range []string{"best", "recommend", "optimal", "winner", "use "} {
		if strings.Contains(strings.ToLower(text), forbidden) {
			t.Errorf("the table suggests a choice with %q:\n%s", forbidden, text)
		}
	}
}

// TestANeighbourThatCouldNotRunIsShownAsSuch.
//
// A value one step away being invalid is itself information about how narrow
// the chosen one is, so the row stays in the table rather than vanishing —
// an absent row reads as a neighbour that behaved the same.
func TestANeighbourThatCouldNotRunIsShownAsSuch(t *testing.T) {
	text := neighbourhoodOf(t, []string{"resume_bars"}, []report.NeighbourResult{
		{Label: "base", Values: []string{"1"}, NetReturn: 0.07, ProfitFactor: 1.13, TradeCount: 210},
		{Label: "resume_bars-1", Values: []string{"0"}, Failed: "resume bars 0 is below 1"},
	})

	if !strings.Contains(text, "resume_bars-1") {
		t.Errorf("the failed neighbour is missing entirely:\n%s", text)
	}
	if !strings.Contains(text, "resume bars 0 is below 1") {
		t.Errorf("the reason it could not run is not shown:\n%s", text)
	}
}

// TestAnUndefinedProfitFactorIsNotPrintedAsANumber. A run with no losing trades
// has no ratio, and a large finite number invites comparison with one.
func TestAnUndefinedProfitFactorIsNotPrintedAsANumber(t *testing.T) {
	text := neighbourhoodOf(t, []string{"fast"}, []report.NeighbourResult{
		{Label: "base", Values: []string{"9"}, NetReturn: 0.07, ProfitFactor: math.NaN(), TradeCount: 0},
		{Label: "fast+1", Values: []string{"10"}, NetReturn: 0.07, ProfitFactor: math.Inf(1), TradeCount: 3},
	})

	if !strings.Contains(text, "n/a") {
		t.Errorf("an undefined profit factor is not shown as n/a:\n%s", text)
	}
	if !strings.Contains(text, "∞") {
		t.Errorf("an infinite profit factor is not shown as such:\n%s", text)
	}
}
