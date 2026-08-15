// Package report turns a run's raw result into the numbers a decision is
// made on, and renders them.
//
// It is separate from the engine so that changing how a statistic is
// presented can never change what the engine simulated.
package report

import (
	"math"
	"time"

	"github.com/shopspring/decimal"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/backtest"
)

// RiskFreeRate is the annual rate subtracted before computing Sharpe.
//
// It is zero, and that is a statement rather than an omission: the report
// prints it, because a Sharpe ratio quoted without the rate it assumed is not
// comparable with anybody else's. Making it a named constant is what stops it
// from being an unexamined zero buried in the arithmetic.
const RiskFreeRate = 0.0

// Drawdown is the worst peak-to-trough fall on the equity curve.
type Drawdown struct {
	// Percent is negative or zero, as a fraction (-0.25 means -25%).
	Percent float64
	// Absolute is the fall in quote currency, also negative or zero.
	Absolute decimal.Decimal

	// PeakAt and TroughAt bound the fall. A drawdown is only meaningful with
	// its dates: "-30%" says nothing about whether it took a week or a year.
	PeakAt   time.Time
	TroughAt time.Time
}

// Statistics is everything computed from one run.
//
// Returns are held as decimal where they are money and float where they are
// ratios: the arithmetic that produces a Sharpe ratio is inherently
// floating-point, and pretending otherwise would add precision that the input
// never had.
type Statistics struct {
	InitialEquity decimal.Decimal
	FinalEquity   decimal.Decimal

	// NetReturn is after every cost. GrossReturn exists only so the two can
	// be compared; nothing reports it alone.
	NetReturn   float64
	GrossReturn float64

	// TotalCosts is what the run paid in fees and slippage. It is reported as
	// its own line because a strategy whose costs exceed its gross profit
	// must be obvious rather than something the reader derives.
	TotalCosts    decimal.Decimal
	TotalGrossPnL decimal.Decimal
	TotalNetPnL   decimal.Decimal

	MaxDrawdown Drawdown

	// Sharpe is annualised from the per-bar equity returns. It is NaN when
	// there is no variation to measure, which is honest: a flat curve has no
	// risk-adjusted return, and reporting zero would imply one was computed.
	Sharpe            float64
	AnnualisationBars float64
	RiskFreeRate      float64

	ProfitFactor float64
	WinRate      float64

	TradeCount int
	WinCount   int
	LossCount  int

	AverageWin  decimal.Decimal
	AverageLoss decimal.Decimal
	LargestWin  decimal.Decimal
	LargestLoss decimal.Decimal

	AverageHoldingTime  time.Duration
	LongestLosingStreak int

	// TradesPerDay is how often the strategy actually traded, over the range
	// it was evaluated on.
	//
	// It is a headline number rather than something to derive, because
	// frequency is what decides whether costs matter: the same entry rule at
	// twelve trades a day and at one is two different strategies once a round
	// trip is priced in. A strategy that states an expected frequency has to
	// be checked against the one it produced, and that check should not
	// require dividing two other numbers by hand.
	TradesPerDay float64

	// AmbiguousBars counts bars where the stop and the target were both
	// reachable and the stop was assumed. A large number here means the
	// result rests on that assumption rather than on the data.
	AmbiguousBars int64

	// AverageCostPerTrade, AverageRisk and WorstRisk are the absolute figures,
	// in quote currency.
	//
	// A percentage hides what decides survivability on a small balance. "-45%"
	// and "-45 USD" on a 100 USD account are the same number and read
	// completely differently, and only the second one answers whether a 72
	// trade losing streak can be sat through.
	AverageCostPerTrade decimal.Decimal
	AverageRisk         decimal.Decimal
	WorstRisk           decimal.Decimal

	// AverageRiskPct and WorstRiskPct are the same risks as a share of the
	// balance each trade was opened on.
	//
	// The worst share is not necessarily the worst absolute risk — on a
	// compounding account the largest position is usually the last one, while
	// the most dangerous is whichever consumed most of the balance available
	// at the time. Reported separately for that reason.
	AverageRiskPct float64
	WorstRiskPct   float64

	// CostShareOfGross is total costs as a fraction of gross profit — how much
	// of what the strategy made never reached the account.
	//
	// NaN when the run made no gross profit. A share of nothing is not zero,
	// and printing 0% there would read as "costs were negligible" for a run
	// where they were the entire result.
	CostShareOfGross float64

	// CostPerTripBps is what a round trip actually cost, in basis points of
	// the notional traded.
	//
	// It exists because the configured rates no longer answer the question. A
	// run with a limit entry and a market exit pays two different fees and
	// slippage on one side only, and the headline rate would understate it.
	// This is measured from the trades rather than from the configuration.
	CostPerTripBps float64
}

// Compute derives the statistics of a finished run.
func Compute(result backtest.Result) Statistics {
	stats := Statistics{
		InitialEquity:     result.Params.InitialEquity,
		FinalEquity:       result.Params.InitialEquity,
		RiskFreeRate:      RiskFreeRate,
		AnnualisationBars: barsPerYear(result.Params.Timeframe),
		AmbiguousBars:     result.AmbiguousBars,
		TradeCount:        len(result.Trades),
		Sharpe:            math.NaN(),
		// NaN, not zero and not infinity. A profit factor is gross profit over
		// gross loss; with no trades there is neither, and `inf` in a log
		// invites being read months later as an extraordinary result rather
		// than as an empty set. Sharpe has been NaN for the same reason since
		// phase 04.
		ProfitFactor: math.NaN(),
	}

	if len(result.Equity) > 0 {
		stats.FinalEquity = result.Equity[len(result.Equity)-1].Equity
	}

	summariseTrades(&stats, result.Trades)

	stats.TotalNetPnL = stats.FinalEquity.Sub(stats.InitialEquity)
	stats.NetReturn = ratio(stats.TotalNetPnL, stats.InitialEquity)
	stats.GrossReturn = ratio(stats.TotalGrossPnL, stats.InitialEquity)

	stats.MaxDrawdown = maxDrawdown(result.Equity)
	stats.Sharpe = sharpe(result.Equity, stats.AnnualisationBars)
	stats.CostPerTripBps = costPerTripBps(result.Trades)
	stats.TradesPerDay = tradesPerDay(len(result.Trades), result.FirstBar, result.LastBar)
	summariseRisk(&stats, result.Trades)

	return stats
}

// tradesPerDay measures frequency over the range actually evaluated.
//
// The evaluated range, not the requested one: warm-up consumed inside the
// range is time the strategy could not have traded in, and counting it would
// understate the frequency of a run whose history was short.
func tradesPerDay(trades int, first, last time.Time) float64 {
	if trades == 0 || first.IsZero() || last.IsZero() {
		return 0
	}

	// A run of one bar has no span to divide by. Reporting the trade count
	// itself is the honest answer at that point — it happened in under a day.
	days := last.Sub(first).Hours() / 24
	if days <= 0 {
		return float64(trades)
	}
	return float64(trades) / days
}

// summariseTrades fills in everything derived from the trade list.
func summariseTrades(stats *Statistics, trades []backtest.Trade) {
	stats.TotalCosts = decimal.Zero
	stats.TotalGrossPnL = decimal.Zero
	stats.AverageWin = decimal.Zero
	stats.AverageLoss = decimal.Zero
	stats.LargestWin = decimal.Zero
	stats.LargestLoss = decimal.Zero

	var (
		grossProfit = decimal.Zero
		grossLoss   = decimal.Zero
		winTotal    = decimal.Zero
		lossTotal   = decimal.Zero
		holdTotal   time.Duration
		streak      int
	)

	for _, trade := range trades {
		stats.TotalCosts = stats.TotalCosts.Add(trade.Costs)
		stats.TotalGrossPnL = stats.TotalGrossPnL.Add(trade.GrossPnL)
		holdTotal += trade.ExitTime.Sub(trade.EntryTime)

		switch {
		case trade.NetPnL.IsPositive():
			stats.WinCount++
			winTotal = winTotal.Add(trade.NetPnL)
			grossProfit = grossProfit.Add(trade.NetPnL)
			if trade.NetPnL.GreaterThan(stats.LargestWin) {
				stats.LargestWin = trade.NetPnL
			}
			streak = 0

		default:
			// A break-even trade counts as a loss, not as neither. It paid
			// costs and returned nothing, and rounding it into the win column
			// would make a strategy that never profits look half successful.
			stats.LossCount++
			lossTotal = lossTotal.Add(trade.NetPnL)
			grossLoss = grossLoss.Add(trade.NetPnL.Abs())
			if trade.NetPnL.LessThan(stats.LargestLoss) {
				stats.LargestLoss = trade.NetPnL
			}
			streak++
			if streak > stats.LongestLosingStreak {
				stats.LongestLosingStreak = streak
			}
		}
	}

	if stats.WinCount > 0 {
		stats.AverageWin = winTotal.Div(decimal.NewFromInt(int64(stats.WinCount)))
	}
	if stats.LossCount > 0 {
		stats.AverageLoss = lossTotal.Div(decimal.NewFromInt(int64(stats.LossCount)))
	}
	if len(trades) > 0 {
		stats.WinRate = float64(stats.WinCount) / float64(len(trades))
		stats.AverageHoldingTime = holdTotal / time.Duration(len(trades))
	}

	// Profit factor is undefined without losses. Infinity is the honest
	// answer — a strategy that has never lost has no ratio, and printing a
	// large finite number would invite it to be compared with one.
	switch {
	case grossLoss.IsZero() && grossProfit.IsZero():
		stats.ProfitFactor = math.NaN()
	case grossLoss.IsZero():
		stats.ProfitFactor = math.Inf(1)
	default:
		floatLoss, _ := grossLoss.Float64()
		floatProfit, _ := grossProfit.Float64()
		stats.ProfitFactor = floatProfit / floatLoss
	}
}

// maxDrawdown is the deepest peak-to-trough fall on the curve.
//
// It walks the equity series rather than the trade list on purpose: a
// position that went far against the account before recovering did happen,
// and a trade-endpoint view would report as if it had not.
func maxDrawdown(curve []backtest.EquityPoint) Drawdown {
	var (
		worst   Drawdown
		peak    decimal.Decimal
		peakAt  time.Time
		started bool
	)

	for _, point := range curve {
		if !started || point.Equity.GreaterThan(peak) {
			peak = point.Equity
			peakAt = point.OpenTime
			started = true
			continue
		}
		if !peak.IsPositive() {
			continue
		}

		fall := point.Equity.Sub(peak)
		percent := ratio(fall, peak)
		if percent < worst.Percent {
			worst = Drawdown{
				Percent:  percent,
				Absolute: fall,
				PeakAt:   peakAt,
				TroughAt: point.OpenTime,
			}
		}
	}

	if worst.Absolute.IsZero() {
		worst.Absolute = decimal.Zero
	}
	return worst
}

// sharpe is the annualised risk-adjusted return of the equity curve.
//
// The per-bar returns are used rather than per-trade ones so that a strategy
// holding through a violent period is not flattered by having had few exits.
// The sample standard deviation (n-1) is used because the run is a sample of
// the strategy's behaviour, not the whole of it.
func sharpe(curve []backtest.EquityPoint, barsPerYear float64) float64 {
	if len(curve) < 3 || barsPerYear <= 0 {
		return math.NaN()
	}

	returns := make([]float64, 0, len(curve)-1)
	for i := 1; i < len(curve); i++ {
		previous := curve[i-1].Equity
		if !previous.IsPositive() {
			continue
		}
		returns = append(returns, ratio(curve[i].Equity.Sub(previous), previous))
	}
	if len(returns) < 2 {
		return math.NaN()
	}

	var sum float64
	for _, r := range returns {
		sum += r
	}
	mean := sum / float64(len(returns))

	var variance float64
	for _, r := range returns {
		variance += (r - mean) * (r - mean)
	}
	variance /= float64(len(returns) - 1)

	stdDev := math.Sqrt(variance)
	if stdDev == 0 {
		// A curve with no variation has no risk-adjusted return to report.
		// NaN says the statistic does not apply; zero would claim it was
		// computed and came out neutral.
		return math.NaN()
	}

	perBarRiskFree := RiskFreeRate / barsPerYear
	return (mean - perBarRiskFree) / stdDev * math.Sqrt(barsPerYear)
}

// barsPerYear is how many candles of a timeframe fit in a calendar year.
//
// Crypto trades continuously, so this is the full 365 days rather than the
// ~252 trading days an equities convention would use. Using the equity figure
// here would inflate every Sharpe ratio this system reports by about 20%.
func barsPerYear(timeframe constants.Timeframe) float64 {
	duration := timeframe.Duration()
	if duration <= 0 {
		return 0
	}
	const year = 365 * 24 * time.Hour
	return float64(year) / float64(duration)
}

// ratio divides two decimals into a float, returning 0 rather than dividing
// by zero.
func ratio(numerator, denominator decimal.Decimal) float64 {
	if denominator.IsZero() {
		return 0
	}
	value, _ := numerator.Div(denominator).Float64()
	return value
}

// costPerTripBps is the average cost of a round trip, in basis points of the
// notional it traded.
//
// Measured from the trades rather than from the configured rates, because the
// rates stopped being the answer once a run could pay maker on one side and
// taker on the other. A run with a limit entry and a market exit pays two
// different fees and slippage once; quoting either rate alone would be wrong,
// and quoting their sum would be wrong in the flattering direction.
//
// Notional is measured at entry, which is the size the position committed.
func costPerTripBps(trades []backtest.Trade) float64 {
	if len(trades) == 0 {
		return 0
	}

	notional := decimal.Zero
	costs := decimal.Zero
	for _, trade := range trades {
		notional = notional.Add(trade.EntryPrice.Mul(trade.Size))
		costs = costs.Add(trade.Costs)
	}
	if !notional.IsPositive() {
		return 0
	}

	// 10,000 basis points to the unit.
	bps, _ := costs.Div(notional).Mul(decimal.NewFromInt(10000)).Float64()
	return bps
}

// summariseRisk measures what each trade put at stake, in quote currency.
//
// Risk is the distance to the stop, not the loss that happened: a trade that
// closed at a target still risked the stop distance, and it is the stop
// distance that decides how much of the account a losing streak consumes.
//
// Trades with no stop contribute nothing. Averaging them in as zero would
// understate the risk of the trades that did carry one.
//
// # The percentages are measured against the balance at the time
//
// Each trade's share is taken against the equity it was opened on, and those
// shares are averaged — rather than dividing the average risk by the run's
// starting balance. The two agree on an account that does not compound and
// diverge wildly on one that does: a 1%-risk rule on an account that grew
// tenfold would otherwise report "1000% of balance", which is alarming,
// meaningless, and not what the sizing rule did.
func summariseRisk(stats *Statistics, trades []backtest.Trade) {
	stats.AverageRisk = decimal.Zero
	stats.WorstRisk = decimal.Zero

	if len(trades) > 0 {
		total := decimal.Zero
		for _, trade := range trades {
			total = total.Add(trade.Costs)
		}
		stats.AverageCostPerTrade = total.Div(decimal.NewFromInt(int64(len(trades))))
	}

	risked := decimal.Zero
	var shareTotal float64
	counted := 0

	for _, trade := range trades {
		if !trade.StopPrice.IsPositive() || !trade.EntryPrice.IsPositive() {
			continue
		}

		risk := trade.EntryPrice.Sub(trade.StopPrice).Abs().Mul(trade.Size)
		risked = risked.Add(risk)
		counted++
		if risk.GreaterThan(stats.WorstRisk) {
			stats.WorstRisk = risk
		}

		// The starting balance is the fallback for a trade with no recorded
		// equity, which is the best available answer rather than a correct
		// one — and it only arises for a hand-built fixture.
		balance := trade.EquityAtEntry
		if !balance.IsPositive() {
			balance = stats.InitialEquity
		}
		share := ratio(risk, balance)
		shareTotal += share
		if share > stats.WorstRiskPct {
			stats.WorstRiskPct = share
		}
	}

	if counted > 0 {
		stats.AverageRisk = risked.Div(decimal.NewFromInt(int64(counted)))
		stats.AverageRiskPct = shareTotal / float64(counted)
	}

	stats.CostShareOfGross = math.NaN()
	if stats.TotalGrossPnL.IsPositive() {
		stats.CostShareOfGross = ratio(stats.TotalCosts, stats.TotalGrossPnL)
	}
}
