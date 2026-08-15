package report_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/backtest"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/backtest/report"
)

// criteriaFile writes a copy of the real acceptance criteria table.
func criteriaFile(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "acceptance-criteria.md")
	content := `# Acceptance criteria

| # | Criterion | Threshold |
|---|---|---|
| 1 | Net return after costs | **> 0** |
| 2 | Profit factor | **> 1.3** |
| 3 | Max drawdown | **< 20%** |
| 4 | Trades in the development period | **≥ 200** |
| 5 | Total costs as a share of gross profit | **< 50%** |
| 6 | Longest losing streak | **≤ 15** |
| 7 | Concentration: profit from the best 5 trades | **< 50% of total** |
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write criteria: %v", err)
	}
	return path
}

// passingRun is a result and statistics that clear every criterion, so a test
// can move one number at a time and see only that criterion fail.
func passingRun() (backtest.Result, report.Statistics, report.Analysis) {
	result := backtest.Result{
		StrategyName: "ema_crossover", StrategyVersion: "v1",
		Params: backtest.RunParams{
			Symbol: "BTCUSDT", Timeframe: constants.Timeframe1h,
			From:      time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
			To:        time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC),
			GapPolicy: backtest.GapSkip,
			Sizing:    backtest.Sizing{Mode: backtest.SizingFixedFractional, RiskPct: decimal.NewFromInt(1)},
		},
		BarsEvaluated: 1000,
	}
	stats := report.Statistics{
		NetReturn:           0.2560,
		ProfitFactor:        1.45,
		MaxDrawdown:         report.Drawdown{Percent: -0.1186},
		TradeCount:          324,
		LongestLosingStreak: 7,
		TotalCosts:          decimal.NewFromInt(3314),
		TotalGrossPnL:       decimal.NewFromInt(8873),
	}
	analysis := report.Analysis{Concentration: report.Concentration{TopN: 5, Share: 0.0529}}
	return result, stats, analysis
}

// TestASuccessfulRunAppendsExactlyOneEntry.
func TestASuccessfulRunAppendsExactlyOneEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "docs", "experiments.md")
	criteria, err := report.LoadCriteria(criteriaFile(t))
	if err != nil {
		t.Fatalf("LoadCriteria() returned error: %v", err)
	}

	result, stats, analysis := passingRun()
	entry := report.ExperimentEntryFor(result, stats, analysis, criteria, nil,
		"dev", time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC), nil)

	number, err := report.AppendExperiment(path, entry)
	if err != nil {
		t.Fatalf("AppendExperiment() returned error: %v", err)
	}
	if number != 1 {
		t.Errorf("the first entry is numbered %d, want 1", number)
	}

	content := readFile(t, path)
	if got := strings.Count(content, "\n### "); got != 1 {
		t.Errorf("the log has %d entries, want 1:\n%s", got, content)
	}
	for _, want := range []string{
		"### 1. 2026-08-15 — ema_crossover v1 (1h)",
		"- **Dataset:** dev (2023-01-01 .. 2024-12-31)",
		"- **Filter:** none (unfiltered)",
		"- **Sizing:** risk 1% of equity",
		"- **Net return after costs:** +25.6000%",
		"- **Profit factor / max drawdown / trades:** 1.45 / -11.86% / 324",
		"- **Concentration (best 5):** 5.29%",
		"- **Verdict:** pass",
		"- **Note:**",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("the entry is missing %q:\n%s", want, content)
		}
	}
}

// TestTheAppendedEntryHasTheSameShapeAsTheHandWrittenOnes. The seven existing
// entries are the reference; a formatter that drifted from them would make the
// file two formats pretending to be one.
func TestTheAppendedEntryHasTheSameShapeAsTheHandWrittenOnes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "experiments.md")
	criteria, _ := report.LoadCriteria(criteriaFile(t))

	result, stats, analysis := passingRun()
	if _, err := report.AppendExperiment(path, report.ExperimentEntryFor(
		result, stats, analysis, criteria, nil, "dev", time.Now().UTC(), nil)); err != nil {
		t.Fatalf("AppendExperiment() returned error: %v", err)
	}

	// Every bullet the hand-written entries carry, in the order they carry it.
	wantOrder := []string{
		"- **Dataset:**", "- **Parameters:**", "- **Filter:**", "- **Sizing:**",
		"- **Net return after costs:**", "- **Profit factor / max drawdown / trades:**",
		"- **Costs as share of gross profit:**", "- **Concentration (best 5):**",
		"- **Verdict:**", "- **Note:**",
	}
	content := readFile(t, path)
	position := 0
	for _, bullet := range wantOrder {
		index := strings.Index(content[position:], bullet)
		if index < 0 {
			t.Fatalf("%q is missing or out of order:\n%s", bullet, content)
		}
		position += index
	}
}

// TestEntryNumbersContinueFromWhatIsAlreadyThere, including the seven
// hand-written entries that carry no number of their own.
func TestEntryNumbersContinueFromWhatIsAlreadyThere(t *testing.T) {
	existing := `# Experiment log

## Format

` + "```" + `
### <date> — <strategy> <version>
` + "```" + `

## Entries

### 2026-08-15 — ema_crossover v1

- **Note:** one

### 2026-08-15 — rsi_reversion v1

- **Note:** two
`
	if got := report.NextExperimentNumber(existing); got != 3 {
		t.Errorf("next number is %d, want 3 — two unnumbered entries are present", got)
	}

	// The example inside the fenced block must not be counted as an entry.
	if strings.Count(existing, "### ") != 3 {
		t.Fatal("the fixture no longer contains the format example; the test is not testing it")
	}

	// Once entries carry explicit numbers, the highest wins.
	numbered := existing + "\n### 9. 2026-08-16 — trend_pullback v1\n\n- **Note:** nine\n"
	if got := report.NextExperimentNumber(numbered); got != 10 {
		t.Errorf("next number is %d, want 10", got)
	}
}

// TestNumbersDoNotCollideAcrossAppends.
func TestNumbersDoNotCollideAcrossAppends(t *testing.T) {
	path := filepath.Join(t.TempDir(), "experiments.md")
	criteria, _ := report.LoadCriteria(criteriaFile(t))
	result, stats, analysis := passingRun()

	seen := map[int]bool{}
	for i := range 5 {
		number, err := report.AppendExperiment(path, report.ExperimentEntryFor(
			result, stats, analysis, criteria, nil, "dev", time.Now().UTC(), nil))
		if err != nil {
			t.Fatalf("append %d returned error: %v", i, err)
		}
		if seen[number] {
			t.Fatalf("number %d was handed out twice", number)
		}
		seen[number] = true
		if number != i+1 {
			t.Errorf("append %d was numbered %d, want %d", i, number, i+1)
		}
	}
}

// TestACostSweepRecordsAllThreePassesOnOneLine.
//
// This line matters more than most. Costs are the dominant variable in this
// system — the same rule went from −99.97% to +25.60% on trade frequency alone
// — so how fast an edge decays as costs rise is closer to the real question
// than the headline number, and it belongs in the permanent record rather than
// in a terminal that gets closed.
func TestACostSweepRecordsAllThreePassesOnOneLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "experiments.md")
	criteria, _ := report.LoadCriteria(criteriaFile(t))
	result, stats, analysis := passingRun()

	sweep := []report.CostSensitivity{
		{Multiplier: 1, NetReturn: 0.2560, TradeCount: 324, ProfitFactor: 1.13},
		{Multiplier: 1.5, NetReturn: 0.0412, TradeCount: 324, ProfitFactor: 1.02},
		{Multiplier: 2, NetReturn: -0.1731, TradeCount: 324, ProfitFactor: 0.91},
	}
	if _, err := report.AppendExperiment(path, report.ExperimentEntryFor(
		result, stats, analysis, criteria, nil, "dev", time.Now().UTC(), sweep)); err != nil {
		t.Fatalf("AppendExperiment() returned error: %v", err)
	}

	content := readFile(t, path)
	var line string
	for _, candidate := range strings.Split(content, "\n") {
		if strings.HasPrefix(candidate, "- **Cost sweep:**") {
			line = candidate
		}
	}
	if line == "" {
		t.Fatalf("no cost sweep line:\n%s", content)
	}
	for _, want := range []string{
		"1.0x +25.60% (PF 1.13)", "1.5x +4.12% (PF 1.02)", "2.0x -17.31% (PF 0.91)",
	} {
		if !strings.Contains(line, want) {
			t.Errorf("the sweep line does not contain %q:\n%s", want, line)
		}
	}
}

// TestASuppressedRunStillSpendsItsNumber.
//
// --no-experiment-log exists for genuine one-offs, but a run that happened is
// a run the denominator has to include. Spending the number makes the omission
// visible instead of silent.
func TestASuppressedRunStillSpendsItsNumber(t *testing.T) {
	path := filepath.Join(t.TempDir(), "experiments.md")
	criteria, _ := report.LoadCriteria(criteriaFile(t))
	result, stats, analysis := passingRun()

	first := report.ExperimentEntryFor(result, stats, analysis, criteria, nil, "dev", time.Now().UTC(), nil)
	if _, err := report.AppendExperiment(path, first); err != nil {
		t.Fatalf("first append returned error: %v", err)
	}

	suppressed := report.ExperimentEntryFor(result, stats, analysis, criteria, nil, "dev", time.Now().UTC(), nil)
	suppressed.Suppressed = true
	number, err := report.AppendExperiment(path, suppressed)
	if err != nil {
		t.Fatalf("suppressed append returned error: %v", err)
	}
	if number != 2 {
		t.Errorf("the suppressed run took number %d, want 2", number)
	}

	third := report.ExperimentEntryFor(result, stats, analysis, criteria, nil, "dev", time.Now().UTC(), nil)
	thirdNumber, err := report.AppendExperiment(path, third)
	if err != nil {
		t.Fatalf("third append returned error: %v", err)
	}
	if thirdNumber != 3 {
		t.Errorf("the run after a suppressed one took number %d, want 3", thirdNumber)
	}

	content := readFile(t, path)
	if !strings.Contains(content, "no-experiment-log") {
		t.Error("the suppressed entry does not say why it is empty")
	}
	// The details really are withheld.
	if strings.Count(content, "- **Net return after costs:**") != 2 {
		t.Errorf("the suppressed entry recorded its numbers anyway:\n%s", content)
	}
}

// TestTheVerdictMatchesAHandComputedResult, one criterion at a time.
func TestTheVerdictMatchesAHandComputedResult(t *testing.T) {
	criteria, err := report.LoadCriteria(criteriaFile(t))
	if err != nil {
		t.Fatalf("LoadCriteria() returned error: %v", err)
	}

	_, passing, analysis := passingRun()
	if verdict := criteria.Judge(passing, analysis); !verdict.Evaluated || len(verdict.Failed) != 0 {
		t.Fatalf("the passing fixture failed: %s", verdict)
	}

	for _, testCase := range []struct {
		name   string
		mutate func(*report.Statistics, *report.Analysis)
		want   string
	}{
		{"net return", func(s *report.Statistics, _ *report.Analysis) { s.NetReturn = -0.01 }, "Net return"},
		{"profit factor", func(s *report.Statistics, _ *report.Analysis) { s.ProfitFactor = 1.29 }, "Profit factor"},
		{"drawdown", func(s *report.Statistics, _ *report.Analysis) {
			s.MaxDrawdown = report.Drawdown{Percent: -0.2001}
		}, "Max drawdown"},
		{"trade count", func(s *report.Statistics, _ *report.Analysis) { s.TradeCount = 199 }, "Trades"},
		{"cost share", func(s *report.Statistics, _ *report.Analysis) {
			s.TotalCosts = decimal.NewFromInt(5000)
			s.TotalGrossPnL = decimal.NewFromInt(9000)
		}, "costs"},
		{"losing streak", func(s *report.Statistics, _ *report.Analysis) { s.LongestLosingStreak = 16 }, "losing streak"},
		{"concentration", func(_ *report.Statistics, a *report.Analysis) {
			a.Concentration.Share = 0.51
		}, "Concentration"},
	} {
		stats, localAnalysis := passing, analysis
		testCase.mutate(&stats, &localAnalysis)

		verdict := criteria.Judge(stats, localAnalysis)
		if len(verdict.Failed) != 1 {
			t.Errorf("%s: %d criteria failed, want exactly 1: %s",
				testCase.name, len(verdict.Failed), verdict)
			continue
		}
		if !strings.Contains(verdict.Failed[0], testCase.want) {
			t.Errorf("%s: the failure is %q, want it to name %q",
				testCase.name, verdict.Failed[0], testCase.want)
		}
	}
}

// TestBoundariesAreExactlyWhereTheCriteriaFileSaysTheyAre. "Above 1.3" is not
// "1.3 or more", and a run that landed exactly on a threshold must not be
// rounded into a pass.
func TestBoundariesAreExactlyWhereTheCriteriaFileSaysTheyAre(t *testing.T) {
	criteria, _ := report.LoadCriteria(criteriaFile(t))
	_, stats, analysis := passingRun()

	stats.ProfitFactor = 1.3 // "> 1.3", so exactly 1.3 fails
	if verdict := criteria.Judge(stats, analysis); len(verdict.Failed) != 1 {
		t.Errorf("a profit factor of exactly 1.3 was accepted against '> 1.3': %s", verdict)
	}

	_, stats, analysis = passingRun()
	stats.TradeCount = 200 // "≥ 200", so exactly 200 passes
	if verdict := criteria.Judge(stats, analysis); len(verdict.Failed) != 0 {
		t.Errorf("exactly 200 trades was rejected against '≥ 200': %s", verdict)
	}

	_, stats, analysis = passingRun()
	stats.LongestLosingStreak = 15 // "≤ 15"
	if verdict := criteria.Judge(stats, analysis); len(verdict.Failed) != 0 {
		t.Errorf("a streak of exactly 15 was rejected against '≤ 15': %s", verdict)
	}
}

// TestAnUnreadableCriteriaFileProducesNoVerdict rather than a guess. A wrong
// pass in a permanent log is the one outcome this apparatus exists to prevent.
func TestAnUnreadableCriteriaFileProducesNoVerdict(t *testing.T) {
	if _, err := report.LoadCriteria(filepath.Join(t.TempDir(), "absent.md")); err == nil {
		t.Fatal("a missing criteria file parsed successfully")
	}

	// A file that exists but is missing criteria is worse than one that is
	// absent: it looks authoritative. It must not produce a partial verdict.
	partial := filepath.Join(t.TempDir(), "partial.md")
	if err := os.WriteFile(partial, []byte(
		"| # | Criterion | Threshold |\n|---|---|---|\n| 1 | Net return after costs | **> 0** |\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := report.LoadCriteria(partial); err == nil {
		t.Error("a criteria file with one of seven thresholds parsed successfully")
	}

	path := filepath.Join(t.TempDir(), "experiments.md")
	result, stats, analysis := passingRun()
	entry := report.ExperimentEntryFor(result, stats, analysis, nil,
		os.ErrNotExist, "dev", time.Now().UTC(), nil)
	if _, err := report.AppendExperiment(path, entry); err != nil {
		t.Fatalf("AppendExperiment() returned error: %v", err)
	}
	if content := readFile(t, path); !strings.Contains(content, "not evaluated") {
		t.Errorf("the entry claims a verdict without criteria:\n%s", content)
	}
}

// TestChangingAThresholdChangesTheVerdict. This is what makes the criteria
// file authoritative rather than decorative: if the numbers were hardcoded,
// editing the file would change what the log claims was tested without
// changing what was tested.
func TestChangingAThresholdChangesTheVerdict(t *testing.T) {
	path := filepath.Join(t.TempDir(), "criteria.md")
	write := func(profitFactor string) report.Criteria {
		t.Helper()
		content := `| # | Criterion | Threshold |
|---|---|---|
| 1 | Net return after costs | **> 0** |
| 2 | Profit factor | **> ` + profitFactor + `** |
| 3 | Max drawdown | **< 20%** |
| 4 | Trades in the development period | **≥ 200** |
| 5 | Total costs as a share of gross profit | **< 50%** |
| 6 | Longest losing streak | **≤ 15** |
| 7 | Concentration: profit from the best 5 trades | **< 50% of total** |
`
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write criteria: %v", err)
		}
		criteria, err := report.LoadCriteria(path)
		if err != nil {
			t.Fatalf("LoadCriteria() returned error: %v", err)
		}
		return criteria
	}

	_, stats, analysis := passingRun() // profit factor 1.45

	if verdict := write("1.3").Judge(stats, analysis); len(verdict.Failed) != 0 {
		t.Errorf("1.45 failed against a 1.3 bar: %s", verdict)
	}
	if verdict := write("1.5").Judge(stats, analysis); len(verdict.Failed) != 1 {
		t.Errorf("1.45 passed against a 1.5 bar: %s", verdict)
	}
}
