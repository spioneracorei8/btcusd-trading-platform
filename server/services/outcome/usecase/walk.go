package usecase

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/backtest"
)

// walked is what following one signal through a series produced.
type walked struct {
	entry decimal.Decimal

	status        constants.OutcomeStatus
	resolvedAt    time.Time
	resolvedPrice decimal.Decimal

	// adverse and favourable are the worst and best distances from the entry,
	// as non-negative magnitudes.
	adverse    decimal.Decimal
	favourable decimal.Decimal
	excursed   bool

	bars int

	// ambiguous marks a resolution where one bar reached both levels and the
	// stop was assumed.
	ambiguous bool

	// gappedPast marks an entry that filled beyond a level it was supposed to
	// be protected by, and the level it filled beyond.
	gappedPast backtest.ExitReason

	// lastClose is where the series had got to, used to price an expiry.
	lastClose decimal.Decimal
}

func (w walked) mae() decimal.NullDecimal {
	if !w.excursed {
		return decimal.NullDecimal{}
	}
	return decimal.NullDecimal{Decimal: w.adverse, Valid: true}
}

func (w walked) mfe() decimal.NullDecimal {
	if !w.excursed {
		return decimal.NullDecimal{}
	}
	return decimal.NullDecimal{Decimal: w.favourable, Valid: true}
}

// walk follows one signal through the bars that closed after it.
//
// # Why this is the backtest's rule and not a second opinion
//
// Every decision here is the engine's, taken from backtest.Levels and
// backtest.FillPrice rather than restated: the entry fills at the first bar's
// open plus slippage, the levels are tested against that same bar's range,
// and a bar reaching both is read as the stop.
//
// The comparison this whole phase exists to produce is between what the
// backtest predicted and what live did. If the two sides each had their own
// reading of "did this bar hit the stop", that comparison would be measuring
// the difference between two implementations, and a divergence would say
// nothing about the strategy.
func (u *outcomeUsecase) walk(signalRow models.Signal, bars []models.Candle) walked {
	buying := signalRow.Direction == constants.DirectionLong
	levels := backtest.Levels{
		Direction: signalRow.Direction,
		Stop:      levelOf(signalRow.StopLoss),
		Target:    levelOf(signalRow.TakeProfit),
	}

	// The entry is the first bar's open plus slippage. The decision was taken
	// on the previous bar's close, and nothing could have filled there.
	result := walked{entry: backtest.FillPrice(bars[0].Open, buying, u.cfg.Costs)}
	result.gappedPast = openedPast(levels, result.entry)

	for i, bar := range bars {
		result.bars = i + 1
		result.lastClose = bar.Close
		result.observe(levels, bar)

		reason, level, ambiguous, hit := levels.HitBy(bar)
		if !hit {
			continue
		}

		result.ambiguous = ambiguous
		result.resolvedAt = bar.CloseTime.UTC()
		// The level is the reference price, exactly as the engine records it:
		// a stop is a market order once it triggers, and the fill is priced
		// from the level rather than from where the bar happened to close.
		result.resolvedPrice = backtest.FillPrice(level, !buying, u.cfg.Costs)
		result.status = statusFor(reason)
		return result
	}

	// Neither level in the window. Expired is a real outcome and counts: a
	// strategy whose signals mostly expire is a strategy that mostly does
	// nothing, and hiding that would flatter it.
	if len(bars) >= u.cfg.ExpiryBars {
		last := bars[len(bars)-1]
		result.status = constants.OutcomeExpired
		result.resolvedAt = last.CloseTime.UTC()
		result.resolvedPrice = backtest.FillPrice(last.Close, !buying, u.cfg.Costs)
		return result
	}

	result.status = constants.OutcomeOpen
	return result
}

// observe records how far this bar went for and against the position.
//
// Both extremes of every bar are counted, including the bar that resolves the
// trade. A maximum adverse excursion that ignored the bar the stop was hit on
// would understate exactly the case it exists to describe.
func (w *walked) observe(levels backtest.Levels, bar models.Candle) {
	for _, price := range []decimal.Decimal{bar.High, bar.Low} {
		distance := price.Sub(w.entry).Abs()
		if levels.Favours(price, w.entry) {
			if !w.excursed || distance.GreaterThan(w.favourable) {
				w.favourable = distance
			}
			continue
		}
		if !w.excursed || distance.GreaterThan(w.adverse) {
			w.adverse = distance
		}
	}

	// The first bar establishes both, so neither can be reported as unknown
	// afterwards. A price exactly at the entry counts as neither, which
	// leaves a zero standing rather than an absence.
	if !w.excursed {
		w.excursed = true
		if w.favourable.IsZero() && w.adverse.IsZero() {
			return
		}
	}
}

// openedPast reports that the entry filled beyond a level it was meant to be
// protected by, and which one.
//
// # Why this is recorded rather than corrected
//
// A signal decided on one bar's close fills at the next bar's open, and the
// market can gap between the two — past the stop, or past the target. The
// engine takes the position anyway and then closes it at the level, because
// the level is what it prices a triggered stop from. That fill is optimistic:
// on such a bar the market never traded at the level, and a long that gapped
// down is recorded as a stop that made money.
//
// The follower does exactly the same thing, on purpose. Matching the engine
// is what makes the live-against-backtest comparison mean anything, and
// quietly correcting it here would make the two disagree for a reason that
// has nothing to do with the strategy. What can be done without breaking that
// is to mark the row, so a reconciliation can see the assumption instead of
// averaging a fictional profit into the stop bucket.
func openedPast(levels backtest.Levels, entry decimal.Decimal) backtest.ExitReason {
	// Already at or beyond the stop: for a long, an entry no higher than the
	// stop it sits under.
	if levels.Stop.IsPositive() && !levels.Favours(entry, levels.Stop) {
		return backtest.ExitStop
	}
	// Already at or beyond the target.
	if levels.Target.IsPositive() && !levels.Favours(levels.Target, entry) {
		return backtest.ExitTarget
	}
	return ""
}

// statusFor maps the engine's exit reason onto an outcome status.
func statusFor(reason backtest.ExitReason) constants.OutcomeStatus {
	switch reason {
	case backtest.ExitTarget:
		return constants.OutcomeTarget
	default:
		// Every other way a level ends a position is a stop from the outcome
		// table's point of view. The engine distinguishes a trailing stop
		// from a fixed one; this table records what the owner would have
		// experienced, which is the same thing either way.
		return constants.OutcomeStop
	}
}

// levelOf reads an advisory level, absent becoming zero — which is what
// backtest.Levels treats as "no level attached".
func levelOf(level decimal.NullDecimal) decimal.Decimal {
	if !level.Valid {
		return decimal.Zero
	}
	return level.Decimal
}

// wouldHave is the engine's own accounting of a resolved signal.
//
// It is stored as jsonb rather than as columns because it is a record of what
// one version of the engine computed, and it is read by a person comparing it
// against a live outcome — not aggregated, not indexed, and not something a
// later change to the engine should have to migrate.
type wouldHave struct {
	// EntryPrice and ExitPrice are what the engine's fill rule produces.
	EntryPrice string `json:"entry_price"`
	ExitPrice  string `json:"exit_price"`

	// Status is the engine's reading of the same bars, which is the same
	// reading by construction — both sides use backtest.Levels. Recorded
	// anyway: the day it stops matching, that is the finding.
	Status string `json:"status"`

	BarsHeld int `json:"bars_held"`

	// GrossReturnPct is before cost and NetReturnPct after it, both per unit
	// of the entry price. Scalping at these timeframes is dominated by cost,
	// so a gross figure on its own is not a result.
	GrossReturnPct string `json:"gross_return_pct"`
	NetReturnPct   string `json:"net_return_pct"`

	// CostPct is what the round trip charged, as a share of the entry.
	CostPct string `json:"cost_pct"`

	// AmbiguousBar records that one bar reached both levels and the stop was
	// assumed. A win rate resting largely on these rests on an assumption.
	AmbiguousBar bool `json:"ambiguous_bar"`

	// GappedPast names a level the entry filled beyond, when it did. The
	// return above is then better than a real fill would have been, because
	// the exit is priced at a level the market did not trade at.
	GappedPast string `json:"gapped_past,omitempty"`
}

// accountFor computes what the backtest makes of this trade, and notes any
// disagreement with the live reading.
func (u *outcomeUsecase) accountFor(
	signalRow models.Signal, w walked,
) (json.RawMessage, string, error) {
	if w.status == constants.OutcomeInvalidated {
		// There is nothing to account for: the window has a hole in it, and
		// an accounting drawn across missing data would be a guess wearing a
		// number's clothes.
		return nil, "", nil
	}
	if !w.entry.IsPositive() {
		return nil, "", fmt.Errorf("signal %s: entry price %s is not positive",
			signalRow.Id, w.entry)
	}

	// Gross is the move in the position's favour, per unit of entry.
	move := w.resolvedPrice.Sub(w.entry)
	if signalRow.Direction == constants.DirectionShort {
		move = w.entry.Sub(w.resolvedPrice)
	}
	gross := move.Div(w.entry).Mul(hundred)

	// Both sides pay taker: an entry crossing the spread, and a stop or an
	// expiry leaving as a market order. A target could rest, but the live
	// path places nothing, so assuming the cheaper side would flatter it.
	cost := u.cfg.Costs.FeeTakerPct.Mul(decimal.NewFromInt(2))

	accounting := wouldHave{
		EntryPrice:     w.entry.String(),
		ExitPrice:      w.resolvedPrice.String(),
		Status:         w.status.String(),
		BarsHeld:       w.bars,
		GrossReturnPct: gross.StringFixed(4),
		NetReturnPct:   gross.Sub(cost).StringFixed(4),
		CostPct:        cost.StringFixed(4),
		AmbiguousBar:   w.ambiguous,
		GappedPast:     string(w.gappedPast),
	}

	encoded, err := json.Marshal(accounting)
	if err != nil {
		return nil, "", fmt.Errorf("signal %s: encode the accounting: %w", signalRow.Id, err)
	}
	return encoded, divergenceNote(w), nil
}

// hundred turns a fraction into a percentage.
var hundred = decimal.NewFromInt(100)

// divergenceNote says when a resolution rests on something other than the
// data.
//
// The live reading and the engine's cannot disagree on status — both come
// from backtest.Levels over the same stored candles, which is the point. What
// can be said, and is worth saying at the row rather than only in an
// aggregate, is when the answer came from an assumption.
func divergenceNote(w walked) string {
	var notes []string

	if w.gappedPast != "" {
		// The louder of the two, because it can turn a loss into a recorded
		// profit rather than only choosing between two real outcomes.
		notes = append(notes, fmt.Sprintf(
			"the entry gapped past its own %s: it filled at %s, already beyond the level. "+
				"The exit is modelled at the level, which the market did not trade at on that "+
				"bar, so the return recorded here is better than a real fill would have been. "+
				"The backtest engine models it the same way, which is why it is noted rather "+
				"than corrected.",
			w.gappedPast, w.entry.String()))
	}

	if w.ambiguous {
		notes = append(notes,
			"one bar reached both the stop and the target; the stop was assumed, "+
				"matching the backtest's pessimistic rule. The bar does not say which came first.")
	}

	return strings.Join(notes, " ")
}
