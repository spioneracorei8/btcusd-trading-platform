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

// feeOn is the fee charged on one side of a round trip, in quote currency.
//
// maker is whether that side rested on the book rather than crossing the
// spread. It is a property of the fill, not of the configuration: an exit
// configured as a limit still pays taker when it leaves through a stop.
func feeOn(notional decimal.Decimal, costs backtest.Costs, maker bool) decimal.Decimal {
	rate := costs.FeeTakerPct
	if maker {
		rate = costs.MakerFeePct()
	}
	return notional.Mul(rate).Div(hundred)
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
	exitFee := feeOn(fill.Mul(p.size), costs, false)

	return p.equityAtEntry.
		Sub(p.entryFee).
		Add(p.realisedPnL(fill)).
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
