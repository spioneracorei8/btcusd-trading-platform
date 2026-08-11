package report

import (
	"fmt"
	"io"
	"math"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/spioneracorei8/btcusd-trading-platform/server/services/backtest"
)

// Comparison is one strategy measured twice over the same range, once with a
// trend filter and once without.
//
// This is the only honest way to answer "does the filter help". A filtered run
// on its own says what happened; it cannot say what would have happened
// otherwise, and the difference is the entire claim being made for the filter.
type Comparison struct {
	Unfiltered backtest.Result
	Filtered   backtest.Result

	UnfilteredStats Statistics
	FilteredStats   Statistics
}

// NewComparison pairs two results and computes both statistics.
func NewComparison(unfiltered, filtered backtest.Result) Comparison {
	return Comparison{
		Unfiltered:      unfiltered,
		Filtered:        filtered,
		UnfilteredStats: Compute(unfiltered),
		FilteredStats:   Compute(filtered),
	}
}

// WriteComparison renders both result sets side by side with the deltas.
//
// The deltas are the point. Two columns of numbers invite the reader to find
// the one that improved; a difference column makes the trade-offs visible at
// once — a filter usually buys a smaller drawdown with fewer trades, and
// whether that is worth it is a judgement the numbers should inform rather
// than make.
func WriteComparison(w io.Writer, comparison Comparison) error {
	var b strings.Builder

	unfiltered, filtered := comparison.UnfilteredStats, comparison.FilteredStats

	b.WriteString("BACKTEST COMPARISON — same strategy, same range, filter on and off\n\n")
	line(&b, "strategy", fmt.Sprintf("%s %s",
		comparison.Filtered.StrategyName, comparison.Filtered.StrategyVersion))
	line(&b, "instrument", fmt.Sprintf("%s %s %s",
		comparison.Filtered.Params.Symbol,
		comparison.Filtered.Params.MarketType,
		comparison.Filtered.Params.Timeframe))
	line(&b, "range", fmt.Sprintf("%s .. %s",
		formatTime(comparison.Filtered.FirstBar), formatTime(comparison.Filtered.LastBar)))
	line(&b, "trend filter", fmt.Sprintf("%s %s",
		comparison.Filtered.TrendFilterName, comparison.Filtered.TrendFilterVersion))
	line(&b, "  configuration", comparison.Filtered.TrendFilterConfig)

	if comparison.Filtered.DataIncomplete || comparison.Unfiltered.DataIncomplete {
		fmt.Fprintf(&b, "\n*** %s — one or both runs proceeded over unfilled gaps ***\n", DataIncompleteStamp)
	}

	fmt.Fprintf(&b, "\n%-28s %16s %16s %16s\n", "", "unfiltered", "filtered", "delta")
	b.WriteString(strings.Repeat("-", 80) + "\n")

	// Net return leads, as it does in the single-run summary.
	percentRow(&b, "net return after costs", unfiltered.NetReturn, filtered.NetReturn)
	moneyRow(&b, "total costs paid", unfiltered.TotalCosts, filtered.TotalCosts)
	percentRow(&b, "gross return", unfiltered.GrossReturn, filtered.GrossReturn)
	moneyRow(&b, "final equity", unfiltered.FinalEquity, filtered.FinalEquity)

	b.WriteString("\n")
	percentRow(&b, "max drawdown", unfiltered.MaxDrawdown.Percent, filtered.MaxDrawdown.Percent)
	floatRow(&b, "sharpe (annualised)", unfiltered.Sharpe, filtered.Sharpe)
	floatRow(&b, "profit factor", unfiltered.ProfitFactor, filtered.ProfitFactor)

	b.WriteString("\n")
	countRow(&b, "trades", int64(unfiltered.TradeCount), int64(filtered.TradeCount))
	percentRow(&b, "win rate", unfiltered.WinRate, filtered.WinRate)
	countRow(&b, "longest losing streak",
		int64(unfiltered.LongestLosingStreak), int64(filtered.LongestLosingStreak))

	b.WriteString("\nFILTER ACTIVITY\n")
	line(&b, "bars evaluated", fmt.Sprintf("%d", comparison.Filtered.BarsEvaluated))
	line(&b, "bars vetoed", fmt.Sprintf("%d (%.2f%%)",
		comparison.Filtered.BarsVetoed,
		percent(comparison.Filtered.BarsVetoed, comparison.Filtered.BarsEvaluated)))
	line(&b, "bars filter not ready", fmt.Sprintf("%d (%.2f%%)",
		comparison.Filtered.BarsFilterNotReady,
		percent(comparison.Filtered.BarsFilterNotReady, comparison.Filtered.BarsEvaluated)))

	b.WriteString("\n" + verdict(comparison))

	if _, err := io.WriteString(w, b.String()); err != nil {
		return fmt.Errorf("write comparison: %w", err)
	}
	return nil
}

// verdict states what the comparison does and does not support.
//
// It deliberately refuses to say the filter "works". One run over one range is
// a single observation; the honest output is what changed and what would make
// the change believable, not a conclusion the sample cannot carry.
func verdict(comparison Comparison) string {
	filtered, unfiltered := comparison.FilteredStats, comparison.UnfilteredStats

	switch {
	case comparison.Filtered.BarsVetoed == 0:
		return "The filter vetoed nothing. The two runs are the same run, and this\n" +
			"comparison says nothing about the filter either way.\n"

	case filtered.TradeCount < 30:
		return fmt.Sprintf(
			"The filtered run has %d trades. That is too few for win rate, profit\n"+
				"factor or Sharpe to mean much — the difference below is mostly sample\n"+
				"noise. Widen the range before reading anything into it.\n",
			filtered.TradeCount)

	case filtered.NetReturn > unfiltered.NetReturn && filtered.MaxDrawdown.Percent > unfiltered.MaxDrawdown.Percent:
		return fmt.Sprintf(
			"On this range the filter improved both net return (%+.4f%% -> %+.4f%%)\n"+
				"and max drawdown (%.4f%% -> %.4f%%). That is one observation over one\n"+
				"range, not evidence: check it on a range this filter has never seen.\n",
			unfiltered.NetReturn*100, filtered.NetReturn*100,
			unfiltered.MaxDrawdown.Percent*100, filtered.MaxDrawdown.Percent*100)

	default:
		return "On this range the filter did not improve both net return and drawdown.\n" +
			"Read the rows above for the trade-off it actually made.\n"
	}
}

// percentRow renders a fractional statistic as a percentage with its delta.
func percentRow(b *strings.Builder, label string, unfiltered, filtered float64) {
	fmt.Fprintf(b, "%-28s %16s %16s %16s\n", label,
		formatPercent(unfiltered), formatPercent(filtered),
		formatPercentDelta(filtered-unfiltered))
}

// moneyRow renders a decimal statistic with its delta.
func moneyRow(b *strings.Builder, label string, unfiltered, filtered decimal.Decimal) {
	delta := filtered.Sub(unfiltered)
	fmt.Fprintf(b, "%-28s %16s %16s %16s\n", label,
		unfiltered.StringFixed(2), filtered.StringFixed(2), signed(delta))
}

// floatRow renders a statistic that may not exist.
func floatRow(b *strings.Builder, label string, unfiltered, filtered float64) {
	delta := "n/a"
	if !math.IsNaN(unfiltered) && !math.IsNaN(filtered) &&
		!math.IsInf(unfiltered, 0) && !math.IsInf(filtered, 0) {
		delta = fmt.Sprintf("%+.4f", filtered-unfiltered)
	}
	fmt.Fprintf(b, "%-28s %16s %16s %16s\n", label,
		formatFloat(unfiltered), formatFloat(filtered), delta)
}

// countRow renders an integer statistic with its delta.
func countRow(b *strings.Builder, label string, unfiltered, filtered int64) {
	fmt.Fprintf(b, "%-28s %16d %16d %+16d\n", label, unfiltered, filtered, filtered-unfiltered)
}

func formatPercent(v float64) string {
	if math.IsNaN(v) {
		return "n/a"
	}
	return fmt.Sprintf("%.4f%%", v*100)
}

func formatPercentDelta(v float64) string {
	if math.IsNaN(v) {
		return "n/a"
	}
	return fmt.Sprintf("%+.4f%%", v*100)
}
