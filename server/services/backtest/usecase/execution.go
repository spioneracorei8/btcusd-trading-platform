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
// Slippage always works against the trade: a buy fills higher than it hoped
// and a sell lower. The symmetry matters more than the magnitude — a model
// where slippage could help in either direction would turn a cost into an
// occasional bonus, and the average would come out flattering.
func fillPrice(reference decimal.Decimal, buying bool, costs backtest.Costs) decimal.Decimal {
	slip := costs.SlippageAmount()
	if buying {
		return reference.Add(slip)
	}

	filled := reference.Sub(slip)
	// A fill cannot go through zero. On any real instrument the slippage is
	// vanishing next to the price, so this guards a nonsense configuration
	// rather than a plausible fill.
	if filled.IsNegative() {
		return decimal.Zero
	}
	return filled
}

// feeOn is the taker fee charged on one side of a round trip, in quote
// currency.
func feeOn(notional decimal.Decimal, costs backtest.Costs) decimal.Decimal {
	return notional.Mul(costs.FeeTakerPct).Div(hundred)
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

	barsHeld int

	entryNote string
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
func (p *openPosition) slippageCost(costs backtest.Costs) decimal.Decimal {
	return costs.SlippageAmount().Mul(p.size).Mul(decimal.NewFromInt(2))
}

// equityAt marks the position to market without closing it.
//
// The exit fee is subtracted even though the position is still open. The
// equity curve answers "what is the account worth if it stops here", and a
// curve that ignored the cost of getting out would overstate every point and
// understate every drawdown — the one statistic that most needs to be
// pessimistic.
func (p *openPosition) equityAt(price decimal.Decimal, costs backtest.Costs) decimal.Decimal {
	if !p.isOpen() {
		return p.equityAtEntry
	}
	exitFee := feeOn(price.Mul(p.size), costs)
	return p.equityAtEntry.
		Sub(p.entryFee).
		Add(p.realisedPnL(price)).
		Sub(exitFee)
}

// stopReachedBy reports whether the bar traded through the stop.
func (p *openPosition) stopReachedBy(bar models.Candle) bool {
	if p.stop.IsZero() {
		return false
	}
	if p.direction == constants.DirectionLong {
		return bar.Low.LessThanOrEqual(p.stop)
	}
	return bar.High.GreaterThanOrEqual(p.stop)
}

// targetReachedBy reports whether the bar traded through the target.
func (p *openPosition) targetReachedBy(bar models.Candle) bool {
	if p.target.IsZero() {
		return false
	}
	if p.direction == constants.DirectionLong {
		return bar.High.GreaterThanOrEqual(p.target)
	}
	return bar.Low.LessThanOrEqual(p.target)
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
	stopHit := p.stopReachedBy(bar)
	targetHit := p.targetReachedBy(bar)

	switch {
	case stopHit && targetHit:
		return backtest.ExitStop, p.stop, true, true
	case stopHit:
		return backtest.ExitStop, p.stop, false, true
	case targetHit:
		return backtest.ExitTarget, p.target, false, true
	default:
		return "", decimal.Zero, false, false
	}
}
