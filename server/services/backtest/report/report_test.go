package report_test

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/backtest"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/backtest/report"
)

var reportStart = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

// sampleResult is a run with wins, losses, a drawdown and a flagged ambiguous
// bar, so every statistic has something to compute from.
func sampleResult() backtest.Result {
	params := backtest.RunParams{
		Symbol:        "BTCUSDT",
		MarketType:    constants.MarketTypeSpot,
		Timeframe:     constants.Timeframe1m,
		From:          reportStart,
		To:            reportStart.Add(10 * time.Minute),
		InitialEquity: dec("10000"),
		Costs: backtest.Costs{
			FeeTakerPct:   dec("0.05"),
			SlippageTicks: 1,
			TickSize:      dec("0.01"),
		},
		GapPolicy: backtest.GapHalt,
	}

	trade := func(minute int, net string, reason backtest.ExitReason) backtest.Trade {
		return backtest.Trade{
			Direction:  constants.DirectionLong,
			EntryTime:  reportStart.Add(time.Duration(minute) * time.Minute),
			EntryPrice: dec("100"),
			ExitTime:   reportStart.Add(time.Duration(minute+1) * time.Minute),
			ExitPrice:  dec("101"),
			Size:       dec("1"),
			GrossPnL:   dec(net).Add(dec("2")),
			Costs:      dec("2"),
			Fees:       dec("1.5"),
			Slippage:   dec("0.5"),
			NetPnL:     dec(net),
			ExitReason: reason,
		}
	}

	return backtest.Result{
		Params:            params,
		StrategyName:      "fixture",
		StrategyVersion:   "v1",
		FirstBar:          reportStart,
		LastBar:           reportStart.Add(4 * time.Minute),
		BarsEvaluated:     5,
		BarsSkippedWarmup: 3,
		BarsSkippedGap:    1,
		AmbiguousBars:     2,
		Trades: []backtest.Trade{
			trade(0, "150", backtest.ExitTarget),
			trade(2, "-80", backtest.ExitStop),
			trade(4, "-40", backtest.ExitStop),
		},
		Equity: []backtest.EquityPoint{
			{OpenTime: reportStart, Equity: dec("10000")},
			{OpenTime: reportStart.Add(time.Minute), Equity: dec("10150")},
			{OpenTime: reportStart.Add(2 * time.Minute), Equity: dec("10070")},
			{OpenTime: reportStart.Add(3 * time.Minute), Equity: dec("10030")},
			{OpenTime: reportStart.Add(4 * time.Minute), Equity: dec("10030")},
		},
	}
}

// TestJSONIsByteIdenticalAcrossRuns is what makes "did my change alter the
// result" answerable with diff instead of with judgement.
//
// Rendering the same document repeatedly is not a formality: Go randomises map
// iteration, so a single map anywhere in the document types would reorder keys
// between renders and this would catch it.
func TestJSONIsByteIdenticalAcrossRuns(t *testing.T) {
	result := sampleResult()

	var first bytes.Buffer
	if err := report.WriteJSON(&first, report.BuildDocument(result, report.Compute(result))); err != nil {
		t.Fatalf("WriteJSON() returned error: %v", err)
	}

	// Rendered many times: one repeat could agree by luck, twenty will not if
	// anything in the document iterates a map.
	for i := range 20 {
		var next bytes.Buffer
		if err := report.WriteJSON(&next, report.BuildDocument(result, report.Compute(result))); err != nil {
			t.Fatalf("WriteJSON() returned error on render %d: %v", i, err)
		}
		if !bytes.Equal(first.Bytes(), next.Bytes()) {
			t.Fatalf("render %d differs from the first; the report is not deterministic", i)
		}
	}
}

// TestJSONCarriesNoRunTimestamp guards determinism against the field most
// likely to be added in good faith. A generated_at would differ on every run
// by definition and break byte-identity permanently.
func TestJSONCarriesNoRunTimestamp(t *testing.T) {
	result := sampleResult()

	var buf bytes.Buffer
	if err := report.WriteJSON(&buf, report.BuildDocument(result, report.Compute(result))); err != nil {
		t.Fatalf("WriteJSON() returned error: %v", err)
	}

	for _, forbidden := range []string{"generated_at", "created_at", "run_at", "timestamp"} {
		if strings.Contains(buf.String(), forbidden) {
			t.Errorf("the JSON contains %q; a wall-clock field makes two identical runs differ", forbidden)
		}
	}
}

// TestMoneyIsEmittedAsStrings keeps large or precise figures from being
// rounded by a JSON float on the way out.
func TestMoneyIsEmittedAsStrings(t *testing.T) {
	result := sampleResult()

	var buf bytes.Buffer
	if err := report.WriteJSON(&buf, report.BuildDocument(result, report.Compute(result))); err != nil {
		t.Fatalf("WriteJSON() returned error: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("the report is not valid JSON: %v", err)
	}

	performance, ok := decoded["performance"].(map[string]any)
	if !ok {
		t.Fatal("no performance block in the report")
	}
	for _, field := range []string{"net_pnl", "total_costs", "gross_pnl", "final_equity"} {
		if _, isString := performance[field].(string); !isString {
			t.Errorf("performance.%s is %T, want a string: money through a JSON float can round",
				field, performance[field])
		}
	}
}

// TestEmptyRunEmitsArraysNotNull keeps the shape stable for a consumer, so a
// run with no trades does not need a special case.
func TestEmptyRunEmitsArraysNotNull(t *testing.T) {
	result := backtest.Result{
		Params: backtest.RunParams{
			Symbol:        "BTCUSDT",
			MarketType:    constants.MarketTypeSpot,
			Timeframe:     constants.Timeframe1m,
			InitialEquity: dec("10000"),
			GapPolicy:     backtest.GapHalt,
			Costs:         backtest.Costs{FeeTakerPct: dec("0.05"), TickSize: dec("0.01")},
		},
		StrategyName: "empty",
	}

	var buf bytes.Buffer
	if err := report.WriteJSON(&buf, report.BuildDocument(result, report.Compute(result))); err != nil {
		t.Fatalf("WriteJSON() returned error: %v", err)
	}

	for _, field := range []string{"trades", "equity_curve", "unfilled_gaps", "untradeable_windows"} {
		if strings.Contains(buf.String(), `"`+field+`": null`) {
			t.Errorf("%s is null; an empty run should emit []", field)
		}
	}
}

// TestUndefinedStatisticsBecomeNull. A Sharpe ratio that could not be computed
// must not be reported as 0.0000, which reads as a neutral result rather than
// as no result.
func TestUndefinedStatisticsBecomeNull(t *testing.T) {
	result := sampleResult()
	// A flat curve has no variation, so there is no risk-adjusted return.
	for i := range result.Equity {
		result.Equity[i].Equity = dec("10000")
	}
	// No losing trades, so profit factor has no denominator.
	result.Trades = []backtest.Trade{{
		Direction: constants.DirectionLong,
		NetPnL:    dec("10"), GrossPnL: dec("12"), Costs: dec("2"),
		EntryTime: reportStart, ExitTime: reportStart.Add(time.Minute),
	}}

	stats := report.Compute(result)
	if !math.IsNaN(stats.Sharpe) {
		t.Errorf("Sharpe on a flat curve is %v, want NaN", stats.Sharpe)
	}
	if !math.IsInf(stats.ProfitFactor, 1) {
		t.Errorf("profit factor with no losses is %v, want +Inf", stats.ProfitFactor)
	}

	var buf bytes.Buffer
	if err := report.WriteJSON(&buf, report.BuildDocument(result, stats)); err != nil {
		t.Fatalf("WriteJSON() returned error: %v", err)
	}
	if !strings.Contains(buf.String(), `"sharpe": null`) {
		t.Error("an undefined Sharpe is not null in the JSON")
	}
	if !strings.Contains(buf.String(), `"profit_factor": null`) {
		t.Error("an undefined profit factor is not null in the JSON")
	}
}

// ---------------------------------------------------------------------------
// Statistics.
// ---------------------------------------------------------------------------

// TestDrawdownWalksTheEquityCurve. The deepest fall here happens between two
// trade endpoints, so a trade-based calculation would miss it entirely.
func TestDrawdownWalksTheEquityCurve(t *testing.T) {
	result := sampleResult()
	stats := report.Compute(result)

	// Peak 10150 at minute 1, trough 10030 at minute 3: a fall of 120, which
	// is 120/10150 of the peak.
	wantPercent, _ := dec("-120").Div(dec("10150")).Float64()
	if math.Abs(stats.MaxDrawdown.Percent-wantPercent) > 1e-12 {
		t.Errorf("max drawdown is %v, want %v", stats.MaxDrawdown.Percent, wantPercent)
	}
	if !stats.MaxDrawdown.Absolute.Equal(dec("-120")) {
		t.Errorf("max drawdown absolute is %s, want -120", stats.MaxDrawdown.Absolute)
	}
	if !stats.MaxDrawdown.PeakAt.Equal(reportStart.Add(time.Minute)) {
		t.Errorf("drawdown peak is at %s, want minute 1", stats.MaxDrawdown.PeakAt)
	}
	if !stats.MaxDrawdown.TroughAt.Equal(reportStart.Add(3 * time.Minute)) {
		t.Errorf("drawdown trough is at %s, want minute 3", stats.MaxDrawdown.TroughAt)
	}
}

// TestBreakEvenTradeCountsAsALoss. It paid costs and returned nothing;
// counting it as a win would make a strategy that never profits look half
// successful.
func TestBreakEvenTradeCountsAsALoss(t *testing.T) {
	result := sampleResult()
	result.Trades = []backtest.Trade{{
		Direction: constants.DirectionLong,
		NetPnL:    decimal.Zero, GrossPnL: dec("2"), Costs: dec("2"),
		EntryTime: reportStart, ExitTime: reportStart.Add(time.Minute),
	}}

	stats := report.Compute(result)
	if stats.WinCount != 0 || stats.LossCount != 1 {
		t.Errorf("a break-even trade counted as %d wins and %d losses, want 0 and 1",
			stats.WinCount, stats.LossCount)
	}
}

// TestLongestLosingStreakCountsConsecutiveLosses.
func TestLongestLosingStreakCountsConsecutiveLosses(t *testing.T) {
	result := sampleResult()
	stats := report.Compute(result)

	// The fixture is win, loss, loss.
	if stats.LongestLosingStreak != 2 {
		t.Errorf("longest losing streak is %d, want 2", stats.LongestLosingStreak)
	}
	if stats.TradeCount != 3 || stats.WinCount != 1 || stats.LossCount != 2 {
		t.Errorf("trade counts are %d/%d/%d, want 3 total with 1 win and 2 losses",
			stats.TradeCount, stats.WinCount, stats.LossCount)
	}
}

// TestSharpeAnnualisesFromTheTimeframe. Crypto trades continuously, so a year
// is 365 days: using the ~252 trading days of an equities convention would
// inflate every Sharpe ratio this system reports.
func TestSharpeAnnualisesFromTheTimeframe(t *testing.T) {
	minutely := sampleResult()
	stats := report.Compute(minutely)

	const minutesPerYear = 365 * 24 * 60
	if math.Abs(stats.AnnualisationBars-minutesPerYear) > 1e-6 {
		t.Errorf("1m annualisation uses %v bars/year, want %d", stats.AnnualisationBars, minutesPerYear)
	}

	hourly := sampleResult()
	hourly.Params.Timeframe = constants.Timeframe1h
	if got := report.Compute(hourly).AnnualisationBars; math.Abs(got-365*24) > 1e-6 {
		t.Errorf("1h annualisation uses %v bars/year, want %d", got, 365*24)
	}

	if report.RiskFreeRate != 0 {
		t.Errorf("RiskFreeRate is %v; the summary states it, so a change here must be deliberate",
			report.RiskFreeRate)
	}
}

// ---------------------------------------------------------------------------
// The human summary.
// ---------------------------------------------------------------------------

// TestSummaryLeadsWithNetReturn. At scalping frequency the difference between
// gross and net is often the entire result, and a reader who meets the
// flattering number first has formed an opinion before reaching the other.
func TestSummaryLeadsWithNetReturn(t *testing.T) {
	result := sampleResult()

	var buf bytes.Buffer
	if err := report.WriteSummary(&buf, result, report.Compute(result)); err != nil {
		t.Fatalf("WriteSummary() returned error: %v", err)
	}
	text := buf.String()

	net := strings.Index(text, "net return after costs")
	gross := strings.Index(text, "gross return")
	costs := strings.Index(text, "total costs paid")

	if net < 0 || gross < 0 || costs < 0 {
		t.Fatalf("the summary is missing a required line:\n%s", text)
	}
	if net > gross {
		t.Error("gross return appears before net return")
	}
	if costs > gross {
		t.Error("total costs appear after gross return; costs must be impossible to skip past")
	}
}

// TestSummaryDisclosesTheCostsItApplied. A report that did not say what fee it
// charged could not be compared with another run, or checked.
func TestSummaryDisclosesTheCostsItApplied(t *testing.T) {
	result := sampleResult()

	var buf bytes.Buffer
	if err := report.WriteSummary(&buf, result, report.Compute(result)); err != nil {
		t.Fatalf("WriteSummary() returned error: %v", err)
	}
	text := buf.String()

	for _, want := range []string{
		"0.05", // the fee that was applied
		"0.01", // the tick size behind the slippage
		"bars evaluated",
		"bars skipped",
		"stop-before-target bars",
		"risk-free rate",
		"fixture v1", // strategy name and version
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the summary does not mention %q:\n%s", want, text)
		}
	}
}

// TestIncompleteRunIsStampedInBothOutputs. Whichever one the reader opens, the
// stamp has to be there.
func TestIncompleteRunIsStampedInBothOutputs(t *testing.T) {
	result := sampleResult()
	result.DataIncomplete = true
	result.Params.GapPolicy = backtest.GapIgnore
	result.UnfilledGaps = []models.DataGap{{
		GapStart: reportStart.Add(time.Minute),
		GapEnd:   reportStart.Add(2 * time.Minute),
		Note:     "no klines returned",
	}}
	stats := report.Compute(result)

	var summary bytes.Buffer
	if err := report.WriteSummary(&summary, result, stats); err != nil {
		t.Fatalf("WriteSummary() returned error: %v", err)
	}
	if !strings.Contains(summary.String(), report.DataIncompleteStamp) {
		t.Errorf("the summary is not stamped:\n%s", summary.String())
	}
	// Before any performance figure, so it cannot be quoted out of context.
	if strings.Index(summary.String(), report.DataIncompleteStamp) >
		strings.Index(summary.String(), "net return") {
		t.Error("the stamp appears after the returns")
	}

	var jsonOut bytes.Buffer
	if err := report.WriteJSON(&jsonOut, report.BuildDocument(result, stats)); err != nil {
		t.Fatalf("WriteJSON() returned error: %v", err)
	}
	if !strings.Contains(jsonOut.String(), report.DataIncompleteStamp) {
		t.Error("the JSON is not stamped")
	}
	if !strings.Contains(jsonOut.String(), `"data_incomplete": true`) {
		t.Error("the JSON does not carry the data_incomplete flag")
	}
}

// TestSummaryShowsUntradeableWindows so a reader can see why part of a range
// produced nothing.
func TestSummaryShowsUntradeableWindows(t *testing.T) {
	result := sampleResult()
	result.UntradeableWindows = []backtest.UntradeableWindow{{
		Start:  time.Date(2023, 3, 24, 12, 40, 0, 0, time.UTC),
		End:    time.Date(2023, 3, 24, 14, 0, 0, 0, time.UTC),
		Reason: "binance spot matching engine incident",
	}}

	var buf bytes.Buffer
	if err := report.WriteSummary(&buf, result, report.Compute(result)); err != nil {
		t.Fatalf("WriteSummary() returned error: %v", err)
	}
	if !strings.Contains(buf.String(), "binance spot matching engine incident") {
		t.Errorf("the summary does not name the outage:\n%s", buf.String())
	}
}
