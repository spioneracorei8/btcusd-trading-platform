package usecase

import (
	"time"

	"github.com/shopspring/decimal"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/backtest"
)

// hundred is the divisor turning a percentage into a fraction.
var hundred = decimal.NewFromInt(100)

// fillPrice applies slippage to a reference price.
//
// The rule itself is backtest.FillPrice, at the service root, because phase
// 07 fills live entries by the same one. Two copies would drift, and the
// comparison between backtest prediction and live outcome would then be
// measuring the drift.
func fillPrice(reference decimal.Decimal, buying bool, costs backtest.Costs) decimal.Decimal {
	return backtest.FillPrice(reference, buying, costs)
}

// feeOn is the cost charged on one side of a round trip, in quote currency.
//
// maker is whether that side rested on the book rather than crossing the
// spread. It is a property of the fill, not of the configuration: an exit
// configured as a limit still pays taker when it leaves through a stop.
//
// Under the spread model the charge is in price points rather than a share of
// notional, so it does not scale with the price level — which is the entire
// reason that model exists. A resting order does not cross the spread and pays
// only commission.
func feeOn(notional, size decimal.Decimal, costs backtest.Costs, maker bool) decimal.Decimal {
	if costs.CostModel() == constants.CostModelSpread {
		return spreadCostOn(size, costs, maker)
	}

	rate := costs.FeeTakerPct
	if maker {
		rate = costs.MakerFeePct()
	}
	return notional.Mul(rate).Div(hundred)
}

// spreadCostOn is one side's share of the quoted spread, plus commission.
func spreadCostOn(size decimal.Decimal, costs backtest.Costs, maker bool) decimal.Decimal {
	cost := decimal.Zero

	// A maker fill rested on the book and was crossed *to*, so it gives up no
	// spread. That is the same reasoning that exempts it from slippage.
	if !maker {
		cost = costs.HalfSpread().Mul(size)
	}

	if costs.CommissionPerLot.IsPositive() && costs.ContractSize.IsPositive() {
		lots := size.Div(costs.ContractSize)
		cost = cost.Add(costs.CommissionPerLot.Mul(lots))
	}
	return cost
}

// openPosition is the engine's own view of what is held.
//
// It is a superset of the read-only copy handed to the strategy, which sees
// no fee and no cost basis beyond its entry price. A strategy able to read
// the engine's accounting could infer fills it was never told about.
type openPosition struct {
	direction constants.Direction

	entryTime  time.Time
	entryPrice decimal.Decimal
	size       decimal.Decimal

	// entryReference is the price before slippage was applied. Gross PnL is
	// measured from it, so the slippage shows up as a cost rather than
	// disappearing into the fill.
	entryReference decimal.Decimal

	// equityAtEntry is what the account was worth the instant before this
	// position opened. Everything the position is worth later is expressed
	// relative to it, which keeps long and short accounting identical.
	equityAtEntry decimal.Decimal

	// entryFee is the cost already paid to open. It is held rather than
	// deducted immediately so the round trip can report one cost figure.
	entryFee decimal.Decimal

	stop   decimal.Decimal
	target decimal.Decimal

	// trailing records that the stop has been moved by the trail, so the exit
	// can be reported apart from a fixed stop.
	trailing bool

	// extreme is the best price the position has seen since entry, which is
	// what the trail is measured back from.
	extreme decimal.Decimal

	barsHeld int

	// entryMaker records how this position was opened, because the exit
	// cannot infer it and the fee for each side is decided separately.
	entryMaker bool

	entryNote string

	// entryATR is the base-timeframe ATR at the close the entry was decided
	// on. Carried through the position so the trade can record the conditions
	// it was taken in rather than only how it turned out.
	entryATR float64
}

// isOpen reports whether a position is held.
func (p *openPosition) isOpen() bool {
	return p != nil && (p.direction == constants.DirectionLong || p.direction == constants.DirectionShort)
}

// grossPnL is what the market did between the two reference prices, before
// any cost at all.
func (p *openPosition) grossPnL(exitReference decimal.Decimal) decimal.Decimal {
	move := exitReference.Sub(p.entryReference)
	if p.direction == constants.DirectionShort {
		move = move.Neg()
	}
	return move.Mul(p.size)
}

// realisedPnL is the change in the account from the actual fills, which is
// what the equity curve has to follow. It differs from grossPnL by exactly
// the slippage on both sides.
func (p *openPosition) realisedPnL(exitFill decimal.Decimal) decimal.Decimal {
	move := exitFill.Sub(p.entryPrice)
	if p.direction == constants.DirectionShort {
		move = move.Neg()
	}
	return move.Mul(p.size)
}

// slippageCost is what the two slipped fills cost the round trip.
//
// One tick against on the way in and one on the way out, whichever direction
// the position was: a short sells lower and buys back higher, which is the
// same penalty as a long buying higher and selling lower.
// slippageCost is the slippage paid across the round trip.
//
// Counted per side rather than doubled: a resting order does not cross the
// spread, so a limit entry followed by a market exit pays slippage once. The
// old unconditional ×2 would have charged a cost that was not incurred, which
// is the safe direction to be wrong in but still wrong.
func (p *openPosition) slippageCost(costs backtest.Costs, exitMaker bool) decimal.Decimal {
	sides := int64(0)
	if !p.entryMaker {
		sides++
	}
	if !exitMaker {
		sides++
	}
	return costs.SlippageAmount().Mul(p.size).Mul(decimal.NewFromInt(sides))
}

// equityAt marks the position to market without closing it.
//
// The full cost of getting out is subtracted even though the position is
// still open: the fee, and the slippage the exit fill would suffer. The curve
// answers "what is the account worth if it stops here", and getting out is
// part of stopping. A curve that counted only the fee would overstate every
// point and understate every drawdown — the statistic that most needs to be
// pessimistic — and would disagree with the trade the engine actually books
// when the position does close at that price.
//
// The exit is always priced as a market order, whatever the run configured.
// An open position's exit is not known to be a target fill — it may be a stop,
// which is always a market order, or the end of the run. Assuming the cheaper
// exit would make the curve optimistic exactly where it must not be.
func (p *openPosition) equityAt(reference decimal.Decimal, costs backtest.Costs) decimal.Decimal {
	if !p.isOpen() {
		// Unreachable through markEquity, which checks first. Guarded anyway:
		// a nil receiver here would panic, and CLAUDE.md §4 rules panics out
		// of business logic.
		return decimal.Zero
	}

	fill := fillPrice(reference, p.direction == constants.DirectionShort, costs)
	exitFee := feeOn(fill.Mul(p.size), p.size, costs, false)

	return p.equityAtEntry.
		Sub(p.entryFee).
		Add(p.realisedPnL(fill)).
		Sub(exitFee)
}

// stopReachedBy reports whether the bar traded through the stop.
func (p *openPosition) stopReachedBy(bar models.Candle) bool {
	return p.levels().StopReachedBy(bar)
}

// levels is the position's stop and target in the form the shared rule takes.
func (p *openPosition) levels() backtest.Levels {
	return backtest.Levels{Direction: p.direction, Stop: p.stop, Target: p.target}
}

// targetReachedBy reports whether the bar traded through the target.
func (p *openPosition) targetReachedBy(bar models.Candle) bool {
	return p.levels().TargetReachedBy(bar)
}

// trailStop is where the trail would sit given a running extreme.
//
// The distance uses the ATR at entry rather than the current bar's, so it is
// fixed when the position opens and cannot drift with volatility mid-trade. A
// trail that widened in a volatile hour would loosen a stop that was already
// placed, which is the one thing a stop may never do.
func (p *openPosition) trailStop(extreme decimal.Decimal, exits backtest.Exits) decimal.Decimal {
	if !exits.Trailing() || p.entryATR <= 0 {
		return decimal.Zero
	}

	distance := decimal.NewFromFloat(p.entryATR).Mul(decimal.NewFromFloat(exits.TrailingATRMult))
	if p.direction == constants.DirectionLong {
		return extreme.Sub(distance)
	}
	return extreme.Add(distance)
}

// armed reports whether the position has moved far enough in its favour for
// the trail to take over from the fixed stop.
func (p *openPosition) armed(extreme decimal.Decimal, exits backtest.Exits) bool {
	if exits.TrailingActivateATR <= 0 {
		return true
	}
	if p.entryATR <= 0 {
		return false
	}

	required := decimal.NewFromFloat(p.entryATR).Mul(decimal.NewFromFloat(exits.TrailingActivateATR))
	if p.direction == constants.DirectionLong {
		return extreme.Sub(p.entryPrice).GreaterThanOrEqual(required)
	}
	return p.entryPrice.Sub(extreme).GreaterThanOrEqual(required)
}

// favourable reports whether a candidate stop is an improvement.
//
// Only in the position's own direction, always. A stop that can loosen is not
// a stop, and the arithmetic that would loosen it — a new extreme that is
// worse than the last, a widened ATR — is exactly the arithmetic that shows up
// when a trade is going wrong.
func (p *openPosition) favourable(candidate decimal.Decimal) bool {
	if !candidate.IsPositive() {
		return false
	}
	if p.stop.IsZero() {
		return true
	}
	if p.direction == constants.DirectionLong {
		return candidate.GreaterThan(p.stop)
	}
	return candidate.LessThan(p.stop)
}

// barExtreme is the best price this bar reached for the position.
func (p *openPosition) barExtreme(bar models.Candle) decimal.Decimal {
	if p.direction == constants.DirectionLong {
		return bar.High
	}
	return bar.Low
}

// levelHitBy reports which attached level the bar reached.
//
// # The stop wins when both are reachable
//
// A 1m bar records four prices and says nothing about the path between them,
// so a bar spanning both levels genuinely does not say which came first.
// Taking the stop is the pessimistic reading, and it is deliberate: the
// optimistic one is how a backtest quietly inflates itself, and it would do
// so precisely on the bars where the data cannot contradict it.
//
// These bars are counted into the report. A strategy whose result depends on
// many of them is being scored on an assumption rather than on evidence.
func (p *openPosition) levelHitBy(bar models.Candle) (reason backtest.ExitReason, level decimal.Decimal, ambiguous, hit bool) {
	reason, level, ambiguous, hit = p.levels().HitBy(bar)

	// A stop that has been moved in the position's favour is reported apart
	// from one that never moved, so whether a strategy's results come from its
	// own stop or from the trail can be counted. The engine knows that; the
	// shared rule does not need to.
	if hit && reason == backtest.ExitStop && p.trailing {
		reason = backtest.ExitTrailingStop
	}
	return reason, level, ambiguous, hit
}
