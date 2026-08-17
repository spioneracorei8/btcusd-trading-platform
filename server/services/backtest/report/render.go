package report

import (
	"fmt"
	"io"
	"math"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
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
	line(&b, "total costs paid", fmt.Sprintf("%s %s  (%s)",
		stats.TotalCosts.StringFixed(2), quoteUnit(result), formatCostShare(stats.CostShareOfGross)))
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

	// Beside the count, not buried below it. Frequency is what decides whether
	// costs matter: the same entry rule at twelve trades a day and at one is
	// two different strategies once a round trip is priced in, and a strategy
	// that declared an expected frequency is checked against this line.
	line(&b, "frequency", fmt.Sprintf("%.2f trades per day over the evaluated range", stats.TradesPerDay))

	// A run that took no trades has no performance to describe, and every
	// ratio below would be computed from an empty set. Said here rather than
	// left to be inferred from a row of zeroes and n/a.
	if stats.TradeCount == 0 {
		b.WriteString("\n  *** THIS RUN TOOK NO TRADES ***\n")
		b.WriteString("  Nothing below describes a strategy. Check the filter and gap lines\n")
		b.WriteString("  above: a run that was fully vetoed or never warmed up reports the\n")
		b.WriteString("  same empty result as one whose rules never triggered.\n")
	}
	line(&b, "win rate", fmt.Sprintf("%.2f%%", stats.WinRate*100))
	line(&b, "profit factor", formatFloat(stats.ProfitFactor))
	line(&b, "average win / loss", fmt.Sprintf("%s / %s %s",
		stats.AverageWin.StringFixed(2), stats.AverageLoss.StringFixed(2), quoteUnit(result)))
	line(&b, "largest win / loss", fmt.Sprintf("%s / %s %s",
		stats.LargestWin.StringFixed(2), stats.LargestLoss.StringFixed(2), quoteUnit(result)))
	line(&b, "average holding time", stats.AverageHoldingTime.String())
	line(&b, "longest losing streak", fmt.Sprintf("%d", stats.LongestLosingStreak))
	line(&b, "entry fills", fmt.Sprintf("%d maker, %d taker",
		result.MakerEntries, result.TakerEntries))
	line(&b, "exit fills", fmt.Sprintf("%d maker, %d taker",
		result.MakerExits, result.TakerExits))
	line(&b, "effective cost", fmt.Sprintf("%.2f bps per round trip", stats.CostPerTripBps))
	line(&b, "average cost", fmt.Sprintf("%s %s per round trip",
		stats.AverageCostPerTrade.StringFixed(4), quoteUnit(result)))

	if result.EntriesBelowMinLot > 0 {
		line(&b, "skipped for size", fmt.Sprintf(
			"%d entries were below the %s lot minimum and were not taken",
			result.EntriesBelowMinLot, result.Params.Costs.MinLot))
		b.WriteString("  (the size the strategy asked for was smaller than the venue can\n")
		b.WriteString("   trade. Refused rather than rounded up: a larger position is a\n")
		b.WriteString("   different bet from the one that was tested)\n")

		// Which of the two causes it was. Without this line the report says a
		// strategy wanted tiny positions, when what happened is that the
		// account could not hold the positions it asked for — and only the
		// second is answered by changing a setting.
		if result.EntriesRefusedAfterCap > 0 {
			line(&b, "  capped first", fmt.Sprintf(
				"%d of those had already been cut to fit a %sx notional limit",
				result.EntriesRefusedAfterCap, result.Params.Sizing.Leverage()))
			b.WriteString("   (the risk setting never reached the position size: the account's\n")
			b.WriteString("    buying power decided it. Raising MAX_LEVERAGE to what the venue\n")
			b.WriteString("    actually offers is what makes --risk-pct take effect)\n")
		}
	}

	// The absolute figures. On a small balance a percentage hides the thing
	// that decides whether a drawdown is survivable: "-45%" and "-45 USD" on a
	// 100 USD account are the same number and read completely differently.
	//
	// The two rows are separate because the worst share and the worst absolute
	// risk need not be the same trade — on a compounding account the largest
	// position is usually the last one, while the most dangerous is whichever
	// consumed most of the balance available at the time.
	line(&b, "risk per trade", fmt.Sprintf("%s %s average, %s worst",
		stats.AverageRisk.StringFixed(2), quoteUnit(result), stats.WorstRisk.StringFixed(2)))
	line(&b, "  as a share of balance", fmt.Sprintf("%.2f%% average, %.2f%% worst (measured at each entry)",
		stats.AverageRiskPct*100, stats.WorstRiskPct*100))

	// The line to watch under a limit entry model, so it is printed where it
	// cannot be skipped rather than left to be derived.
	if result.EntriesRequested > 0 && result.LimitOrdersExpired > 0 {
		line(&b, "limit orders cancelled", fmt.Sprintf("%d of %d signals (%.2f%%) never filled",
			result.LimitOrdersExpired, result.EntriesRequested,
			percent(result.LimitOrdersExpired, result.EntriesRequested)))
		b.WriteString("  (these signals produced no trade at all; the statistics above\n")
		b.WriteString("   describe the subset that did fill, which is not the strategy\n")
		b.WriteString("   as written)\n")
	}

	b.WriteString("\nASSUMPTIONS\n")
	line(&b, "stop-before-target bars", fmt.Sprintf("%d", stats.AmbiguousBars))
	b.WriteString("  (bars where the stop and the target were both reachable and\n")
	b.WriteString("   the stop was assumed to fill first; a large count means the\n")
	b.WriteString("   result rests on that assumption rather than on the data)\n")

	// Stated beside the stop-before-target assumption because they are the
	// same class of thing: a simplification the result rests on, which the
	// reader has to be able to weigh.
	if usesLimitOrders(result) {
		b.WriteString("\n  limit fills assume touch means fill. Queue position is real and\n")
		b.WriteString("  being at the front of the book is not automatic, so the true fill\n")
		b.WriteString("  rate is at best this one. Intrabar path is also unknown: a bar\n")
		b.WriteString("  whose low reached the limit may have done so before or after\n")
		b.WriteString("  everything else that happened in it.\n")
	}

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
	writeCostModel(b, result)
	line(b, "gap policy", result.Params.GapPolicy.String())
	line(b, "sizing", describeSizing(result.Params.Sizing))

	// The filter is part of what produced the numbers, so it belongs in the
	// header beside the strategy rather than in a footnote.
	if result.TrendFilterName == "" {
		// "none" alone reads as a choice. When the filter was wanted and could
		// not be built, that is a limit of the collected data and a different
		// finding entirely.
		if result.TrendUnavailable != "" {
			line(b, "trend filter", fmt.Sprintf("none (%s)", result.TrendUnavailable))
		} else {
			line(b, "trend filter", "none (unfiltered)")
		}
	} else {
		line(b, "trend filter", fmt.Sprintf("%s %s", result.TrendFilterName, result.TrendFilterVersion))
		line(b, "  configuration", result.TrendFilterConfig)
		line(b, "  bars vetoed", fmt.Sprintf("%d of %d evaluated (%.2f%%)",
			result.BarsVetoed, result.BarsEvaluated,
			percent(result.BarsVetoed, result.BarsEvaluated)))
		line(b, "  bars not ready", fmt.Sprintf("%d of %d evaluated (%.2f%%)",
			result.BarsFilterNotReady, result.BarsEvaluated,
			percent(result.BarsFilterNotReady, result.BarsEvaluated)))

		// Both extremes are worth saying out loud, because neither is legible
		// from the returns: a filter that blocks nothing is not doing
		// anything, and one that blocks nearly everything leaves too few
		// trades for the statistics to mean much.
		switch {
		case result.BarsEvaluated == 0:
		case result.BarsVetoed == 0:
			b.WriteString("  (the filter vetoed nothing at all — it is not affecting this run)\n")
		case percent(result.BarsFilterNotReady, result.BarsEvaluated) > 90:
			b.WriteString("  (the filter was not ready for most of the run — the range is\n" +
				"   probably shorter than its warm-up; see the header above)\n")
		}
	}
	_ = stats
}

// percent renders a count as a share of a total, guarding the empty run.
func percent(part, total int64) float64 {
	if total == 0 {
		return 0
	}
	return float64(part) / float64(total) * 100
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

// formatCostShare renders costs as a share of gross profit.
//
// The undefined case is spelled out rather than shown as a number. A run that
// made no gross profit has no share for its costs to be, and "0.00%" there
// would read as the opposite of what happened.
func formatCostShare(v float64) string {
	if math.IsNaN(v) {
		return "the run made no gross profit for them to be a share of"
	}
	return fmt.Sprintf("%.2f%% of gross profit", v*100)
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

// usesLimitOrders reports whether any part of the run rested on the book.
func usesLimitOrders(result backtest.Result) bool {
	return result.Params.Execution.Entry() == constants.OrderTypeLimit ||
		result.Params.Execution.Exit() == constants.OrderTypeLimit
}

// writeCostModel states what a fill actually costs.
//
// A single "fee applied" line was enough while every fill was a taker fill.
// With two rates and two order types it would be ambiguous in the direction
// that flatters: a reader seeing 0.02% would have no way to tell whether the
// run charged it on both sides, on entries only, or on the exits that happened
// to leave through a target.
func writeCostModel(b *strings.Builder, result backtest.Result) {
	costs := result.Params.Costs
	execution := result.Params.Execution

	if costs.CostModel() == constants.CostModelSpread {
		unit := quoteUnit(result)
		line(b, "cost model", fmt.Sprintf("spread (%s %s typical, %d points, half each side)",
			costs.SpreadPrice().StringFixed(2), unit, costs.SpreadPoints))
		line(b, "commission", fmt.Sprintf("%s %s per lot per side",
			costs.CommissionPerLot.StringFixed(2), unit))
		line(b, "contract size", fmt.Sprintf("%s per lot, min %s, step %s",
			costs.ContractSize, costs.MinLot, costs.LotStep))
		line(b, "point value", fmt.Sprintf("%s %s of price per point", costs.PointValue, unit))
		line(b, "slippage applied", fmt.Sprintf("%d tick(s) of %s, market fills only, always against",
			costs.SlippageTicks, costs.TickSize))
		if execution.Entry() == constants.OrderTypeLimit || execution.Exit() == constants.OrderTypeLimit {
			line(b, "limit order timeout", fmt.Sprintf("%d bar(s)", execution.Timeout()))
		}

		// Printed on every spread run rather than left to be remembered. The
		// cost model can be made to match the venue; the price series cannot,
		// without collecting from it.
		b.WriteString("  note: prices are Binance BTCUSDT; costs model IUX BTCUSD CFD.\n")
		b.WriteString("        Spread behaviour and quotes on the trading venue will differ,\n")
		b.WriteString("        so treat the direction of this result as the finding and the\n")
		b.WriteString("        magnitude as an estimate.\n")
		return
	}

	rate := func(orderType constants.OrderType) string {
		if orderType == constants.OrderTypeLimit {
			return fmt.Sprintf("limit (maker %s%%)", costs.MakerFeePct())
		}
		return fmt.Sprintf("market (taker %s%%)", costs.FeeTakerPct)
	}

	line(b, "entry order type", rate(execution.Entry()))
	line(b, "exit order type", rate(execution.Exit()))

	// Always stated, and always market. It is the one exit that cannot be a
	// resting order, and a reader comparing a maker-rate result against a
	// taker-rate one needs to know the losses were priced the same in both.
	line(b, "stop exits", fmt.Sprintf("market (taker %s%%) — always", costs.FeeTakerPct))

	line(b, "slippage applied", fmt.Sprintf("%d tick(s) of %s, market fills only, always against",
		costs.SlippageTicks, costs.TickSize))

	if usesLimitOrders(result) {
		line(b, "limit order timeout", fmt.Sprintf("%d bar(s)", execution.Timeout()))
	}
}
