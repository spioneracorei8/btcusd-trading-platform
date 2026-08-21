package report_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spioneracorei8/btcusd-trading-platform/server/helper"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/backtest"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/backtest/report"
)

// TestNonDefaultParametersAppearInTheHeader.
//
// A run whose parameters are not in its own report is not reproducible. Both
// the value and the default it replaced are printed, because "fast=12" alone
// does not tell a reader six weeks later whether that was a change at all.
func TestNonDefaultParametersAppearInTheHeader(t *testing.T) {
	result := sampleResult()
	result.Params.StrategyParams = []helper.ParamChange{
		{Name: "fast", From: "9", To: "12"},
		{Name: "stop_atr_mult", From: "1.5", To: "2.5"},
	}

	var buf bytes.Buffer
	if err := report.WriteSummary(&buf, result, report.Compute(result)); err != nil {
		t.Fatalf("WriteSummary() returned error: %v", err)
	}
	text := buf.String()

	for _, want := range []string{"parameters", "fast=12", "default 9", "stop_atr_mult=2.5", "default 1.5"} {
		if !strings.Contains(text, want) {
			t.Errorf("the header does not show %q:\n%s", want, text)
		}
	}
}

// TestADefaultRunSaysSoRatherThanSayingNothing.
//
// Fifty-seven evaluations ran before any parameter could be varied, and every
// one was at defaults. A report that simply omitted the line would read the
// same whether the run used defaults or nobody recorded what it used.
func TestADefaultRunSaysSoRatherThanSayingNothing(t *testing.T) {
	result := sampleResult()
	result.Params.StrategyParams = nil

	var buf bytes.Buffer
	if err := report.WriteSummary(&buf, result, report.Compute(result)); err != nil {
		t.Fatalf("WriteSummary() returned error: %v", err)
	}
	if !strings.Contains(buf.String(), "defaults") {
		t.Errorf("a run at defaults does not say so:\n%s", buf.String())
	}
}

// TestTheExperimentLogRecordsTheParameters.
//
// The entry said the literal word "defaults" until parameters could be varied.
// The day that stopped being true it became a claim the log could not support,
// and the log's whole value is that it can be trusted months later.
func TestTheExperimentLogRecordsTheParameters(t *testing.T) {
	result := sampleResult()
	result.Params.StrategyParams = []helper.ParamChange{{Name: "fast", From: "9", To: "12"}}
	result.Params.FilterParams = []helper.ParamChange{{Name: "weight_1h", From: "0.5", To: "0.7"}}

	rendered := logEntryFor(t, result)
	for _, want := range []string{"fast=12", "weight_1h=0.7"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("the log entry does not record %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "**Parameters:** defaults") {
		t.Errorf("the log entry claims defaults while two parameters differ:\n%s", rendered)
	}
}

// TestTheExperimentLogStillSaysDefaultsWhenItMeansIt.
func TestTheExperimentLogStillSaysDefaultsWhenItMeansIt(t *testing.T) {
	rendered := logEntryFor(t, sampleResult())
	if !strings.Contains(rendered, "**Parameters:** defaults") {
		t.Errorf("a run at defaults is not recorded as such:\n%s", rendered)
	}
}

// logEntryFor appends one entry to a throwaway log and returns what was
// written.
func logEntryFor(t *testing.T, result backtest.Result) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "experiments.md")
	entry := report.ExperimentEntryFor(
		result, report.Compute(result), report.Analysis{}, report.Criteria{}, nil,
		"dev", time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC), nil)

	if _, err := report.AppendExperiment(path, entry); err != nil {
		t.Fatalf("AppendExperiment() returned error: %v", err)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the log: %v", err)
	}
	return string(content)
}
