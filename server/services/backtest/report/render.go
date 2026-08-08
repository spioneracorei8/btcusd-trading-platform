package report

import (
	"fmt"
	"io"
	"math"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/spioneracorei8/btcusd-trading-platform/server/services/backtest"
)

// DataIncompleteStamp marks a run that was allowed to proceed over holes in
// the candle series. It appears in the human summary and in the JSON, so it
// cannot be lost by whichever one is read.
const DataIncompleteStamp = "DATA_INCOMPLETE"

// WriteSummary renders the human-readable report.
//
// It leads with net return after costs. Gross return appears beside it and
// never alone: at scalping frequency the difference between the two is often
// the entire result, and a reader who sees the flattering number first has
// already formed an opinion by the time they reach the other.
func WriteSummary(w io.Writer, result backtest.Result, stats Statistics) error {
	var b strings.Builder

	writeHeader(&b, result, stats)

	b.WriteString("\nPERFORMANCE\n")
	line(&b, "net return after costs", fmt.Sprintf("%+.4f%%  (%s %s)",
		stats.NetReturn*100, signed(stats.TotalNetPnL), quoteUnit(result)))
	line(&b, "total costs paid", fmt.Sprintf("%s %s", stats.TotalCosts.StringFixed(2), quoteUnit(result)))
	line(&b, "gross return before costs", fmt.Sprintf("%+.4f%%", stats.GrossReturn*100))
	line(&b, "equity", fmt.Sprintf("%s → %s %s",
		stats.InitialEquity.StringFixed(2), stats.FinalEquity.StringFixed(2), quoteUnit(result)))

	b.WriteString("\nRISK\n")
	if stats.MaxDrawdown.Percent == 0 {
		line(&b, "max drawdown", "none")
	} else {
		line(&b, "max drawdown", fmt.Sprintf("%.4f%%  (%s %s)",
			stats.MaxDrawdown.Percent*100, stats.MaxDrawdown.Absolute.StringFixed(2), quoteUnit(result)))
		line(&b, "  from peak", stats.MaxDrawdown.PeakAt.Format(time.RFC3339))
		line(&b, "  to trough", stats.MaxDrawdown.TroughAt.Format(time.RFC3339))
	}
	line(&b, "sharpe (annualised)", fmt.Sprintf("%s  (risk-free rate %.2f%%, %.0f bars/year)",
		formatFloat(stats.Sharpe), stats.RiskFreeRate*100, stats.AnnualisationBars))

	b.WriteString("\nTRADES\n")
	line(&b, "count", fmt.Sprintf("%d  (%d won, %d lost)", stats.TradeCount, stats.WinCount, stats.LossCount))
	line(&b, "win rate", fmt.Sprintf("%.2f%%", stats.WinRate*100))
	line(&b, "profit factor", formatFloat(stats.ProfitFactor))
	line(&b, "average win / loss", fmt.Sprintf("%s / %s %s",
		stats.AverageWin.StringFixed(2), stats.AverageLoss.StringFixed(2), quoteUnit(result)))
	line(&b, "largest win / loss", fmt.Sprintf("%s / %s %s",
		stats.LargestWin.StringFixed(2), stats.LargestLoss.StringFixed(2), quoteUnit(result)))
	line(&b, "average holding time", stats.AverageHoldingTime.String())
	line(&b, "longest losing streak", fmt.Sprintf("%d", stats.LongestLosingStreak))

	b.WriteString("\nASSUMPTIONS\n")
	line(&b, "stop-before-target bars", fmt.Sprintf("%d", stats.AmbiguousBars))
	b.WriteString("  (bars where the stop and the target were both reachable and\n")
	b.WriteString("   the stop was assumed to fill first; a large count means the\n")
	b.WriteString("   result rests on that assumption rather than on the data)\n")

	if len(result.UntradeableWindows) > 0 {
		b.WriteString("\nUNTRADEABLE WINDOWS (no order could have been placed)\n")
		for _, window := range result.UntradeableWindows {
			fmt.Fprintf(&b, "  %s .. %s  %s\n",
				window.Start.Format(time.RFC3339), window.End.Format(time.RFC3339), window.Reason)
		}
	}

	if len(result.UnfilledGaps) > 0 {
		fmt.Fprintf(&b, "\nUNFILLED GAPS (%d)\n", len(result.UnfilledGaps))
		for _, gap := range result.UnfilledGaps {
			fmt.Fprintf(&b, "  %s .. %s  (%d fill attempts)\n",
				gap.GapStart.Format(time.RFC3339), gap.GapEnd.Format(time.RFC3339), gap.FillAttempts)
		}
	}

	_, err := io.WriteString(w, b.String())
	if err != nil {
		return fmt.Errorf("write summary: %w", err)
	}
	return nil
}

// writeHeader renders the block that is never suppressible.
func writeHeader(b *strings.Builder, result backtest.Result, stats Statistics) {
	if result.DataIncomplete {
		// First, before anything a reader might quote out of context.
		fmt.Fprintf(b, "*** %s — this run proceeded over unfilled gaps ***\n\n", DataIncompleteStamp)
	}

	b.WriteString("BACKTEST\n")
	line(b, "strategy", fmt.Sprintf("%s %s", result.StrategyName, result.StrategyVersion))
	line(b, "instrument", fmt.Sprintf("%s %s %s",
		result.Params.Symbol, result.Params.MarketType, result.Params.Timeframe))
	line(b, "requested range", fmt.Sprintf("%s .. %s",
		result.Params.From.Format(time.RFC3339), result.Params.To.Format(time.RFC3339)))
	line(b, "evaluated range", fmt.Sprintf("%s .. %s",
		formatTime(result.FirstBar), formatTime(result.LastBar)))
	line(b, "bars evaluated", fmt.Sprintf("%d", result.BarsEvaluated))
	line(b, "bars skipped", fmt.Sprintf("%d warm-up, %d gap or halt",
		result.BarsSkippedWarmup, result.BarsSkippedGap))
	line(b, "fee applied", fmt.Sprintf("%s%% taker, each side", result.Params.Costs.FeeTakerPct))
	line(b, "slippage applied", fmt.Sprintf("%d tick(s) of %s, each side, always against",
		result.Params.Costs.SlippageTicks, result.Params.Costs.TickSize))
	line(b, "gap policy", result.Params.GapPolicy.String())
	_ = stats
}

// line writes one aligned label/value row.
func line(b *strings.Builder, label, value string) {
	fmt.Fprintf(b, "  %-26s %s\n", label+":", value)
}

// formatFloat renders a statistic that may legitimately not exist.
//
// NaN prints as "n/a" and infinity as "∞" rather than as numbers: a Sharpe
// ratio of NaN means the question did not apply, and printing "0.0000" would
// claim an answer was computed.
func formatFloat(v float64) string {
	switch {
	case math.IsNaN(v):
		return "n/a"
	case math.IsInf(v, 1):
		return "∞ (no losing trades)"
	case math.IsInf(v, -1):
		return "-∞"
	default:
		return fmt.Sprintf("%.4f", v)
	}
}

// formatTime renders a timestamp that may be unset.
func formatTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Format(time.RFC3339)
}

// signed renders a decimal with an explicit sign, so a positive result is
// never mistaken for an unsigned magnitude.
func signed(d decimal.Decimal) string {
	if d.IsNegative() {
		return d.StringFixed(2)
	}
	return "+" + d.StringFixed(2)
}

// quoteUnit names the currency the money figures are in. The quote asset is
// the tail of the symbol; when it cannot be told, the figures are labelled
// "quote" rather than guessed at.
func quoteUnit(result backtest.Result) string {
	for _, quote := range []string{"USDT", "USDC", "BUSD", "USD", "BTC", "ETH"} {
		if strings.HasSuffix(result.Params.Symbol, quote) {
			return quote
		}
	}
	return "quote"
}
