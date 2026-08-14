package report_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spioneracorei8/btcusd-trading-platform/server/services/backtest"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/backtest/report"
)

func holdoutEntry(strategyName string, at time.Time) report.HoldoutEntry {
	return report.HoldoutEntry{
		At:              at,
		StrategyName:    strategyName,
		StrategyVersion: "v1",
		TrendFilter:     "ema_rsi_mtf",
		From:            backtest.HoldoutFrom,
		To:              backtest.HoldoutFrom.AddDate(1, 0, 0),
		NetReturn:       0.0421,
		TradeCount:      312,
	}
}

// TestFirstHoldoutUseIsRecorded is §C1's enforcement: the run happens, and the
// fact that it happened is written down where it will be seen later.
func TestFirstHoldoutUseIsRecorded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "docs", "holdout-log.md")

	if err := report.AppendHoldoutUse(path, holdoutEntry("ema_crossover", time.Now().UTC())); err != nil {
		t.Fatalf("AppendHoldoutUse() returned error: %v", err)
	}

	content := readFile(t, path)
	for _, want := range []string{
		"# Holdout log", "Use 1", "ema_crossover", "ema_rsi_mtf", "4.2100%", "312",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("the log does not mention %q:\n%s", want, content)
		}
	}
}

// TestASecondUseIsMarkedAsSpending is what the log is actually for.
//
// The rule cannot be enforced — anyone can pass explicit dates or run against a
// copy — and a lock would create the illusion of a guarantee it cannot give.
// What the log does is make the second use visible, including to the person
// making it, and say plainly what it cost.
func TestASecondUseIsMarkedAsSpending(t *testing.T) {
	path := filepath.Join(t.TempDir(), "holdout-log.md")
	at := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)

	if err := report.AppendHoldoutUse(path, holdoutEntry("ema_crossover", at)); err != nil {
		t.Fatalf("first append returned error: %v", err)
	}
	if err := report.AppendHoldoutUse(path, holdoutEntry("rsi_reversion", at.Add(time.Hour))); err != nil {
		t.Fatalf("second append returned error: %v", err)
	}

	content := readFile(t, path)

	if !strings.Contains(content, "Use 1") || !strings.Contains(content, "Use 2") {
		t.Fatalf("both uses are not numbered in the log:\n%s", content)
	}
	// The header must appear once, not once per entry.
	if strings.Count(content, "# Holdout log") != 1 {
		t.Errorf("the header appears %d times, want 1", strings.Count(content, "# Holdout log"))
	}
	// And the second entry must say what it means, not merely exist.
	if !strings.Contains(content, "already spent by use 1") {
		t.Errorf("the second use is recorded without saying the set was already spent:\n%s", content)
	}
	// Both strategy names present: the log is the record of what was tried.
	if !strings.Contains(content, "ema_crossover") || !strings.Contains(content, "rsi_reversion") {
		t.Error("the log does not name both strategies")
	}
}

// TestTheLogSurvivesAMissingDirectory, so a first holdout run cannot fail for
// want of a folder and quietly go unrecorded.
func TestTheLogSurvivesAMissingDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a", "b", "holdout-log.md")

	if err := report.AppendHoldoutUse(path, holdoutEntry("trend_pullback", time.Now().UTC())); err != nil {
		t.Fatalf("AppendHoldoutUse() returned error: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the log was not created: %v", err)
	}
}

// TestOnlyTheHoldoutIsSpent. Development runs are free; that is what a
// development set is for, and a tool that recorded every run would train
// people to ignore the file.
func TestOnlyTheHoldoutIsSpent(t *testing.T) {
	if !backtest.DatasetHoldout.Spent() {
		t.Error("the holdout is not marked as spent")
	}
	for _, dataset := range []backtest.Dataset{backtest.DatasetDev, backtest.DatasetCustom} {
		if dataset.Spent() {
			t.Errorf("%s is marked as spent; only the holdout is", dataset)
		}
	}
}

// TestTheSplitIsWhereItSaysItIs. These dates are part of the method: changing
// them changes what the holdout means and makes every earlier result
// incomparable, so they are pinned rather than merely written down.
func TestTheSplitIsWhereItSaysItIs(t *testing.T) {
	if backtest.DevFrom.Format("2006-01-02") != "2023-01-01" {
		t.Errorf("the development set starts %s, want 2023-01-01", backtest.DevFrom)
	}
	if backtest.DevTo.Format("2006-01-02") != "2024-12-31" {
		t.Errorf("the development set ends %s, want 2024-12-31", backtest.DevTo)
	}
	if backtest.HoldoutFrom.Format("2006-01-02") != "2025-01-01" {
		t.Errorf("the holdout starts %s, want 2025-01-01", backtest.HoldoutFrom)
	}

	// The two must not overlap, or a "holdout" run would include data already
	// iterated on and the split would mean nothing.
	if !backtest.HoldoutFrom.After(backtest.DevTo) {
		t.Error("the holdout begins before the development set ends; they overlap")
	}

	from, to, ok := backtest.DatasetDev.Range(time.Now())
	if !ok || !from.Equal(backtest.DevFrom) || !to.Equal(backtest.DevTo) {
		t.Errorf("the dev range resolves to %s..%s", from, to)
	}
	if _, _, ok := backtest.DatasetCustom.Range(time.Now()); ok {
		t.Error("a custom dataset claims a range of its own; the caller supplies the dates")
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(content)
}
