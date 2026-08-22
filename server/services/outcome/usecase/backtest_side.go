package usecase

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/shopspring/decimal"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/backtest"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/backtest/report"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/outcome"
	_strategy_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/strategy/usecase"
)

// EngineComparer produces the backtest side by re-running the engine.
//
// # Why it re-runs rather than reading a stored figure
//
// The comparison is between what the backtest predicted for this period and
// what live did in it. A number from an evaluation over the development set
// would answer a different question — and the divergence worth catching most,
// live producing fewer signals than the engine would have, is only visible
// when both are asked about the same bars.
type EngineComparer struct {
	Engine     backtest.BacktestUsecase
	Timeframe  constants.Timeframe
	Costs      backtest.Costs
	Equity     decimal.Decimal
	MarketType constants.MarketType
}

// Compare re-runs one group's strategy and parameters over the same window.
func (c EngineComparer) Compare(
	ctx context.Context, params outcome.ReconcileParams, group outcome.ReconciledGroup,
) (outcome.Side, error) {
	if c.Engine == nil {
		return outcome.Side{}, fmt.Errorf("no engine to run")
	}

	entry, err := _strategy_us.Lookup(group.Strategy)
	if err != nil {
		// A strategy this binary no longer ships. Said plainly, because the
		// alternative is a report that quietly omits the comparison.
		return outcome.Side{}, err
	}

	overrides := make(map[string]string, len(group.Params))
	for _, p := range group.Params {
		overrides[p.Name] = p.Value
	}

	// Built through the same registry the live path used, with the parameter
	// set recorded on the signals themselves. Anything else would compare a
	// strategy against a differently configured version of itself.
	strat, _, err := entry.BuildWith(
		overrides,
		roundTripCostPct(c.Costs),
		c.MarketType == constants.MarketTypeSpot,
	)
	if err != nil {
		return outcome.Side{}, fmt.Errorf(
			"rebuild %s with the parameters recorded on its signals: %w", group.Strategy, err)
	}

	if group.Version != strat.Version() {
		// The code has moved since these signals were produced. Comparing
		// against it is comparing against a different strategy, and doing so
		// silently would attribute the difference to the market.
		return outcome.Side{}, fmt.Errorf(
			"these signals came from %s %s and this binary ships %s; "+
				"the comparison would be against different code",
			group.Strategy, group.Version, strat.Version())
	}

	result, err := c.Engine.Run(ctx, backtest.RunParams{
		Symbol:        params.Symbol,
		MarketType:    params.MarketType,
		Timeframe:     c.Timeframe,
		From:          params.From,
		To:            params.To,
		InitialEquity: c.Equity,
		Costs:         c.Costs,
		GapPolicy:     backtest.GapHalt,
		Sizing:        backtest.DefaultSizing(),
		Strategy:      strat,
	})
	if err != nil {
		return outcome.Side{}, fmt.Errorf("replay %s over the same period: %w", group.Strategy, err)
	}

	return sideFrom(result), nil
}

// sideFrom turns an engine result into the comparison's own shape.
//
// # Why the engine's statistics rather than a second count
//
// report.Compute is what every evaluation in docs/experiments.md was scored
// with. Recounting the trades here would risk answering "what is the win
// rate" differently from every number the strategy was chosen on.
func sideFrom(result backtest.Result) outcome.Side {
	stats := report.Compute(result)

	side := outcome.Side{
		Signals:  stats.TradeCount,
		Resolved: stats.TradeCount,
		Wins:     stats.WinCount,
		Losses:   stats.LossCount,
		Noted:    int(stats.AmbiguousBars),
		WinRate:  math.NaN(),
	}
	if stats.TradeCount > 0 {
		side.WinRate = stats.WinRate
	}

	// The engine reports money; the live side reports percentages of the
	// entry. They are put on the same footing here rather than left for the
	// reader, who would otherwise be comparing dollars against percents.
	side.AverageWinPct = pctOfEntry(stats.AverageWin, result)
	side.AverageLossPct = pctOfEntry(stats.AverageLoss, result)
	side.AverageEntryPrice = averageEntry(result)
	side.AverageCostPct = result.Params.Costs.FeeTakerPct.Mul(decimal.NewFromInt(2))

	if len(result.Trades) > 0 {
		side.First = result.Trades[0].EntryTime.UTC()
		side.Last = result.Trades[len(result.Trades)-1].ExitTime.UTC()

		// The engine records times rather than a bar count, so the holding
		// time is converted at the timeframe it was run on — which is the
		// same unit the live side counts in.
		if span := result.Params.Timeframe.Duration(); span > 0 {
			var held time.Duration
			for _, t := range result.Trades {
				held += t.ExitTime.Sub(t.EntryTime)
			}
			side.AverageBarsHeld = decimal.NewFromFloat(
				held.Seconds() / span.Seconds() / float64(len(result.Trades)))
		}
	}
	return side
}

// averageEntry is the mean price the engine filled its entries at.
func averageEntry(result backtest.Result) decimal.Decimal {
	if len(result.Trades) == 0 {
		return decimal.Zero
	}

	total := decimal.Zero
	for _, t := range result.Trades {
		total = total.Add(t.EntryPrice)
	}
	return total.Div(decimal.NewFromInt(int64(len(result.Trades))))
}

// pctOfEntry restates a money figure as a percentage of the average notional
// committed, so the two sides of the report are in the same units.
func pctOfEntry(amount decimal.Decimal, result backtest.Result) decimal.Decimal {
	if amount.IsZero() || len(result.Trades) == 0 {
		return decimal.Zero
	}

	notional := decimal.Zero
	for _, t := range result.Trades {
		notional = notional.Add(t.EntryPrice.Mul(t.Size))
	}
	if !notional.IsPositive() {
		return decimal.Zero
	}

	average := notional.Div(decimal.NewFromInt(int64(len(result.Trades))))
	return amount.Div(average).Mul(decimal.NewFromInt(100))
}

// roundTripCostPct is what one entry and one exit cost, in percent.
func roundTripCostPct(costs backtest.Costs) float64 {
	taker, _ := costs.FeeTakerPct.Float64()
	return taker * 2
}

// ensure the comparer satisfies the interface the usecase depends on.
var _ BacktestComparer = EngineComparer{}
