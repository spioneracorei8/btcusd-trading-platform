package report

import (
	"fmt"
	"io"
	"math"
	"sort"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/backtest"
)

// Analysis is the phase-06 §C4 reporting: the specific ways a result can look
// good and mean nothing.
//
// These are reported beside the headline numbers rather than instead of them.
// A strategy can clear every acceptance threshold and still be a few lucky
// bars wearing a costume, and the headline figures cannot tell the difference.
type Analysis struct {
	Concentration Concentration
	ByYear        []PeriodBreakdown
	ByVolatility  []PeriodBreakdown
}

// Concentration is how much of the profit came from a handful of trades.
type Concentration struct {
	// TopN is how many trades were counted as "the best few".
	TopN int

	// TopNProfit is their combined net PnL, and Share is that as a fraction
	// of total net profit across all winning trades.
	TopNProfit decimal.Decimal
	Share      float64

	TotalNet decimal.Decimal
}

// PeriodBreakdown is one slice of a run — a year, or a volatility regime.
type PeriodBreakdown struct {
	Label string

	Trades  int
	NetPnL  decimal.Decimal
	WinRate float64
}

// Analyse computes the failure-mode report for a finished run.
func Analyse(result backtest.Result, stats Statistics) Analysis {
	return Analysis{
		Concentration: concentration(result.Trades, stats, 5),
		ByYear:        byYear(result.Trades),
		ByVolatility:  byVolatility(result),
	}
}

// concentration measures how much of the profit the best few trades carry.
//
// If most of it comes from five trades out of two hundred, the other
// hundred and ninety-five paid fees for the privilege of being in the sample,
// and the strategy's claim rests on events too rare to have been measured.
func concentration(trades []backtest.Trade, stats Statistics, topN int) Concentration {
	result := Concentration{TopN: topN, TotalNet: stats.TotalNetPnL,
		TopNProfit: decimal.Zero}

	if len(trades) == 0 {
		return result
	}

	nets := make([]decimal.Decimal, 0, len(trades))
	grossProfit := decimal.Zero
	for _, trade := range trades {
		nets = append(nets, trade.NetPnL)
		if trade.NetPnL.IsPositive() {
			grossProfit = grossProfit.Add(trade.NetPnL)
		}
	}
	sort.Slice(nets, func(i, j int) bool { return nets[i].GreaterThan(nets[j]) })

	for i := 0; i < topN && i < len(nets); i++ {
		if nets[i].IsPositive() {
			result.TopNProfit = result.TopNProfit.Add(nets[i])
		}
	}

	// Measured against gross profit rather than net: the question is how
	// concentrated the *winning* was, and netting losses in would let a
	// strategy with many small losses look diversified.
	if grossProfit.IsPositive() {
		share, _ := result.TopNProfit.Div(grossProfit).Float64()
		result.Share = share
	}
	return result
}

// byYear breaks the trades down by calendar year.
//
// A strategy that only worked in 2023 has told you about 2023. Crypto regimes
// last months, so a single aggregate over two years can hide one good year
// paying for one bad one — which is not an edge, it is a coin that landed
// heads first.
func byYear(trades []backtest.Trade) []PeriodBreakdown {
	years := map[int]*PeriodBreakdown{}
	order := []int{}

	for _, trade := range trades {
		year := trade.EntryTime.UTC().Year()
		if _, seen := years[year]; !seen {
			years[year] = &PeriodBreakdown{Label: fmt.Sprintf("%d", year), NetPnL: decimal.Zero}
			order = append(order, year)
		}
		accumulate(years[year], trade)
	}

	sort.Ints(order)
	out := make([]PeriodBreakdown, 0, len(order))
	for _, year := range order {
		finish(years[year])
		out = append(out, *years[year])
	}
	return out
}

// volatilityBuckets is how many the trades are split into. Terciles rather
// than a median split: two buckets hide the extremes, and the extremes are
// where a strategy's behaviour usually differs.
const volatilityBuckets = 3

// byVolatility groups trades by the volatility they were entered into.
//
// # The defect this replaces
//
// The first version split on the entry-to-exit move — how far price travelled
// while the trade was open. That is the trade's own outcome. Every trade whose
// price barely moved lost to costs, so every such trade landed in the "low"
// bucket by construction, and the report faithfully announced that the low
// bucket had a 0.00% win rate across four thousand trades. Reproduced across
// three structurally different strategies, that is not a finding about
// markets; it is arithmetic. Sorting losses into a bucket and then reporting
// that the bucket lost is circular.
//
// # What replaces it
//
// ATR at the entry bar, which is knowable before the outcome exists and is the
// volatility the strategy actually conditioned on — the same value its stop
// was sized from.
//
// It is normalised by entry price. An absolute ATR split over a range where
// BTC moved between roughly $16k and $100k would mostly be sorting by calendar
// date: $200 of ATR is a violent day at $16k and a quiet one at $100k.
//
// The buckets are labelled with the ATR range they cover, because "high" is
// meaningless without knowing whether it means 0.3% or 3%.
func byVolatility(result backtest.Result) []PeriodBreakdown {
	type scored struct {
		trade  backtest.Trade
		atrPct float64
	}

	scoredTrades := make([]scored, 0, len(result.Trades))
	for _, trade := range result.Trades {
		if !trade.EntryPrice.IsPositive() || trade.EntryATR <= 0 {
			continue
		}
		entry, _ := trade.EntryPrice.Float64()
		if entry <= 0 {
			continue
		}
		scoredTrades = append(scoredTrades, scored{
			trade: trade, atrPct: trade.EntryATR / entry * 100,
		})
	}

	// Fewer trades than buckets cannot be split into them, and a bucket
	// holding one trade reports that trade's outcome as a regime.
	if len(scoredTrades) < volatilityBuckets*2 {
		return nil
	}

	sort.Slice(scoredTrades, func(i, j int) bool {
		if scoredTrades[i].atrPct != scoredTrades[j].atrPct {
			return scoredTrades[i].atrPct < scoredTrades[j].atrPct
		}
		// A stable tiebreak, so two runs over the same trades bucket them
		// identically. Report determinism is ADR 0012.
		return scoredTrades[i].trade.EntryTime.Before(scoredTrades[j].trade.EntryTime)
	})

	names := [volatilityBuckets]string{"low", "mid", "high"}
	out := make([]PeriodBreakdown, 0, volatilityBuckets)

	for bucket := range volatilityBuckets {
		start := bucket * len(scoredTrades) / volatilityBuckets
		end := (bucket + 1) * len(scoredTrades) / volatilityBuckets
		if start >= end {
			continue
		}

		period := &PeriodBreakdown{
			Label: fmt.Sprintf("%s vol (ATR %.2f–%.2f%%)",
				names[bucket], scoredTrades[start].atrPct, scoredTrades[end-1].atrPct),
			NetPnL: decimal.Zero,
		}
		for _, s := range scoredTrades[start:end] {
			accumulate(period, s.trade)
		}
		finish(period)
		out = append(out, *period)
	}
	return out
}

// accumulate adds one trade to a breakdown.
func accumulate(period *PeriodBreakdown, trade backtest.Trade) {
	period.Trades++
	period.NetPnL = period.NetPnL.Add(trade.NetPnL)
	if trade.NetPnL.IsPositive() {
		// WinRate holds the count until finish turns it into a rate.
		period.WinRate++
	}
}

// finish turns the accumulated win count into a rate.
func finish(period *PeriodBreakdown) {
	if period.Trades > 0 {
		period.WinRate /= float64(period.Trades)
	}
}

// WriteAnalysis renders the failure-mode report.
func WriteAnalysis(w io.Writer, analysis Analysis, unit string) error {
	var b strings.Builder

	b.WriteString("\nFAILURE MODES\n")

	c := analysis.Concentration
	line(&b, fmt.Sprintf("best %d trades", c.TopN),
		fmt.Sprintf("%s %s — %.2f%% of gross profit", c.TopNProfit.StringFixed(2), unit, c.Share*100))
	if c.Share > 0.5 {
		b.WriteString("  (most of the profit comes from a handful of trades; the rest of the\n" +
			"   sample paid fees to be there. Treat the result as a few lucky bars\n" +
			"   until it survives a range those bars are not in.)\n")
	}

	if len(analysis.ByYear) > 0 {
		b.WriteString("\n  by year\n")
		writeBreakdowns(&b, analysis.ByYear, unit)

		if len(analysis.ByYear) > 1 && onlyOneYearProfitable(analysis.ByYear) {
			b.WriteString("  (only one year is profitable. A strategy that worked in one regime\n" +
				"   has told you about that regime.)\n")
		}
	}

	if len(analysis.ByVolatility) > 0 {
		b.WriteString("\n  by volatility (terciles of ATR at the entry bar, as % of entry price)\n")
		writeBreakdowns(&b, analysis.ByVolatility, unit)
	}

	if _, err := io.WriteString(w, b.String()); err != nil {
		return fmt.Errorf("write analysis: %w", err)
	}
	return nil
}

func writeBreakdowns(b *strings.Builder, periods []PeriodBreakdown, unit string) {
	for _, period := range periods {
		fmt.Fprintf(b, "    %-18s %6d trades  %14s %s  win rate %6.2f%%\n",
			period.Label, period.Trades, period.NetPnL.StringFixed(2), unit, period.WinRate*100)
	}
}

// onlyOneYearProfitable reports whether exactly one year carried the result.
func onlyOneYearProfitable(periods []PeriodBreakdown) bool {
	profitable := 0
	for _, period := range periods {
		if period.NetPnL.IsPositive() {
			profitable++
		}
	}
	return profitable == 1
}

// CostSensitivity is one run repeated at a multiple of the assumed cost.
type CostSensitivity struct {
	Multiplier float64
	NetReturn  float64
	TradeCount int

	// ProfitFactor at this cost level. Net return alone hides the shape of the
	// decay: a run can stay positive while its profit factor collapses towards
	// 1, which is the point at which the edge stops being worth the risk.
	ProfitFactor float64
}

// WriteCostSensitivity renders the cost-sensitivity table.
//
// An edge that vanishes under modest slippage was never robust enough to
// trade. The assumed cost is a guess about a number that moves against retail
// accounts over time — fee tiers change, spreads widen in exactly the
// conditions a strategy trades most — so a result that only survives at
// exactly the assumed figure has no margin at all.
// CostSensitivityHeading names the cost model the sweep scaled, so a run at
// maker rates is not mistaken for one at taker rates. An edge surviving 1.5x
// at maker rates is a materially stronger result than the same multiple at
// taker rates, and the two are otherwise indistinguishable in the table.
func CostSensitivityHeading(params backtest.RunParams) string {
	costs := params.Costs

	// Under the spread model no fee rate priced anything, and quoting one here
	// would name a cost the run never paid — which is worse than saying
	// nothing, because it reads as a description of what was scaled.
	if costs.CostModel() == constants.CostModelSpread {
		heading := fmt.Sprintf("scaling a %s spread (%d points) on both sides",
			costs.SpreadPrice().StringFixed(2), costs.SpreadPoints)
		if costs.CommissionPerLot.IsPositive() {
			heading += fmt.Sprintf(" and %s per lot per side", costs.CommissionPerLot.StringFixed(2))
		}
		return heading
	}

	entry := params.Execution.Entry()
	exit := params.Execution.Exit()
	if entry == constants.OrderTypeMarket && exit == constants.OrderTypeMarket {
		return fmt.Sprintf("scaling taker %s%% on both sides", costs.FeeTakerPct)
	}
	return fmt.Sprintf("scaling maker %s%% and taker %s%% together (entry %s, exit %s)",
		costs.MakerFeePct(), costs.FeeTakerPct, entry, exit)
}

func WriteCostSensitivity(w io.Writer, runs []CostSensitivity, heading string) error {
	var b strings.Builder

	b.WriteString("\nCOST SENSITIVITY\n")
	if heading != "" {
		fmt.Fprintf(&b, "  %s\n", heading)
	}
	fmt.Fprintf(&b, "    %-12s %16s %10s %10s\n", "cost", "net return", "trades", "PF")

	baseline := math.NaN()
	for _, run := range runs {
		if run.Multiplier == 1 {
			baseline = run.NetReturn
		}
		fmt.Fprintf(&b, "    %-12s %16s %10d %10s\n",
			fmt.Sprintf("%.1fx", run.Multiplier), formatPercent(run.NetReturn),
			run.TradeCount, formatFloat(run.ProfitFactor))
	}

	for _, run := range runs {
		if run.Multiplier > 1 && !math.IsNaN(baseline) && baseline > 0 && run.NetReturn <= 0 {
			fmt.Fprintf(&b, "  (the edge disappears at %.1fx the assumed cost. Treat that as a\n"+
				"   failure regardless of the headline number: fee tiers and spreads move\n"+
				"   against a retail account, not for it.)\n", run.Multiplier)
			break
		}
	}

	if _, err := io.WriteString(w, b.String()); err != nil {
		return fmt.Errorf("write cost sensitivity: %w", err)
	}
	return nil
}

// NeighbourResult is one run at a neighbouring parameter value.
type NeighbourResult struct {
	// Label names the row: "base", or the parameter and the direction it was
	// moved in.
	Label string

	// Values is what every varied parameter held for this row, in the same
	// order as the table's columns. A row that only named the parameter it
	// changed would leave a reader deriving the rest.
	Values []string

	NetReturn    float64
	ProfitFactor float64
	TradeCount   int
	Failed       string
}

// WriteNeighbourhood renders the parameter-stability report.
//
// # This reports, it never selects
//
// Phase 06 puts automated parameter optimisation out of scope, and this is
// deliberately not that. It runs the neighbours and prints them; it does not
// rank them, does not recommend one, and does not report a "best". The
// question it answers is whether the chosen value sits on a plateau or on a
// spike — if EMA(21) is profitable and 20 and 22 are not, that is a fitted
// artefact, and the only useful response is to stop believing 21.
//
// A tool that picked the winner would industrialise the exact mistake.
//
// # Why the reading is printed every time
//
// It used to appear only when the chosen value stood alone, which is the one
// case a reader would probably have noticed unaided. The reading that gets
// forgotten is the other one: a base row that looks good, neighbours that look
// broadly similar, and nobody stopping to ask which of the two shapes they are
// looking at. So the rule is printed beside every table, good or bad.
func WriteNeighbourhood(w io.Writer, columns []string, rows []NeighbourResult) error {
	var b strings.Builder

	b.WriteString("\nPARAMETER NEIGHBOURHOOD\n")

	fmt.Fprintf(&b, "    %-14s", "")
	for _, column := range columns {
		fmt.Fprintf(&b, " %10s", column)
	}
	fmt.Fprintf(&b, " %14s %10s %8s\n", "net return", "trades", "PF")

	for _, row := range rows {
		fmt.Fprintf(&b, "    %-14s", row.Label)
		for i := range columns {
			value := "-"
			if i < len(row.Values) {
				value = row.Values[i]
			}
			fmt.Fprintf(&b, " %10s", value)
		}

		if row.Failed != "" {
			fmt.Fprintf(&b, " %14s %10s %8s  %s\n", "-", "-", "-", row.Failed)
			continue
		}
		fmt.Fprintf(&b, " %14s %10d %8s\n",
			formatPercent(row.NetReturn), row.TradeCount, formatFloat(row.ProfitFactor))
	}

	b.WriteString("\n  How to read this: a value whose neighbours behave broadly like it sits\n")
	b.WriteString("  on a plateau, and may be measuring something real. One that collapses a\n")
	b.WriteString("  single step away is a spike — the shape of a value fitted to the noise\n")
	b.WriteString("  in this particular history — and should be discarded however good the\n")
	b.WriteString("  base row looks.\n")

	if base, neighbours, ok := splitBase(rows); ok && isolated(base, neighbours) {
		b.WriteString("\n  *** This is a spike. The chosen values are profitable and not one\n")
		b.WriteString("      neighbour is. Nothing here selects a replacement; the finding is\n")
		b.WriteString("      that these values should not be believed. ***\n")
	}

	if _, err := io.WriteString(w, b.String()); err != nil {
		return fmt.Errorf("write neighbourhood: %w", err)
	}
	return nil
}

// splitBase separates the base row from its neighbours.
func splitBase(rows []NeighbourResult) (NeighbourResult, []NeighbourResult, bool) {
	for i, row := range rows {
		if row.Label == NeighbourhoodBaseLabel {
			return row, append(append([]NeighbourResult(nil), rows[:i]...), rows[i+1:]...), true
		}
	}
	return NeighbourResult{}, nil, false
}

// NeighbourhoodBaseLabel names the row holding the chosen configuration.
const NeighbourhoodBaseLabel = "base"

// isolated reports whether the chosen value stands alone.
func isolated(chosen NeighbourResult, neighbours []NeighbourResult) bool {
	if chosen.Failed != "" || chosen.NetReturn <= 0 || len(neighbours) == 0 {
		return false
	}
	for _, neighbour := range neighbours {
		if neighbour.Failed == "" && neighbour.NetReturn > 0 {
			return false
		}
	}
	return true
}
