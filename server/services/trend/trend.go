// Package trend declares the multi-timeframe trend filter contract.
//
// # A filter is a veto, not a signal
//
// It decides when entering is *permitted*. It never decides what is taken and
// it cannot emit an entry: nothing in this package can express one. Keeping
// the veto separate from the decision is what makes "does the filter help"
// answerable — run the same strategy with and without it and compare.
//
// # The hazard this package exists to contain
//
// At 14:23 the 1m candle for 14:22 has closed; the 1h candle for 14:00 has
// not. Joining on timestamp hands a strategy the completed 14:00-15:00 hourly
// bar, which contains the next 37 minutes of price action. The backtest then
// looks excellent and cannot be reproduced live, because live has no such
// data. Every type here is shaped to make that mistake impossible to express
// rather than merely discouraged.
package trend

import (
	"context"
	"time"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
)

// Filter reads one closed base-timeframe bar and reports what the higher
// timeframes permit.
//
// Like strategy.Strategy it is pure: no context, no error, no I/O. The same
// implementation runs in a backtest and, from phase 06, live, and there is no
// way for it to ask which.
type Filter interface {
	// OnBar is called once per closed base-timeframe candle, in order.
	OnBar(bar BarContext) TrendState

	// WarmupPeriod is how many base-timeframe bars must precede the first
	// meaningful answer. It counts base bars even though the constraint comes
	// from higher timeframes, because the base timeframe is what the engine
	// iterates — see WarmupBaseBars.
	WarmupPeriod() int

	// Name and Version are recorded in every report beside the strategy's.
	// Version changes whenever the scoring logic changes, so a surprising
	// number six weeks from now can be traced to the code that produced it.
	Name() string
	Version() string
}

// BarContext is everything a filter may observe at a decision point.
//
// # Why this is not strategy.BarContext
//
// The phase-04 spec asks for "the same BarContext shape as Strategy", and it
// cannot literally be that type: a filter needs a reading per timeframe where
// a strategy needs one. Widening strategy.BarContext would also defeat its
// allow-list test, which exists precisely to stop fields being added to it.
//
// What carries over is the discipline, unchanged. No slice of candles, no
// index into one, no reader, no clock. Every higher-timeframe reading arrives
// already filtered to bars that had definitively closed, so a filter cannot
// reach a bar it should not see even by trying.
type BarContext struct {
	// Candle is the base-timeframe bar that just closed.
	Candle models.Candle

	// Indicators are the base-timeframe values at that close.
	Indicators models.IndicatorSnapshot

	// Higher holds one view per contributing higher timeframe, ordered
	// shortest first. It is a slice rather than a map: Go randomises map
	// iteration, and phase 04 requires a backtest report to be byte-identical
	// across runs, which a map in this position would quietly break.
	Higher []TimeframeView
}

// ViewFor returns the view for a timeframe.
//
// It exists so callers need not know the slice order, and returns ok=false
// rather than a zero value that could be scored as a real reading.
func (b BarContext) ViewFor(timeframe constants.Timeframe) (TimeframeView, bool) {
	for _, view := range b.Higher {
		if view.Timeframe == timeframe {
			return view, true
		}
	}
	return TimeframeView{}, false
}

// TimeframeView is one higher timeframe's contribution at a decision point.
//
// CloseTime is not decoration. It is the evidence that this reading came from
// a bar that had definitively closed, and the alignment tests assert on it: a
// value whose CloseTime is after the decision instant is look-ahead, whatever
// else looks right about it.
type TimeframeView struct {
	Timeframe constants.Timeframe

	// Candle is the most recent candle of this timeframe whose close_time is
	// at or before the decision instant.
	Candle models.Candle

	// Indicators are that candle's values, computed only from candles up to
	// and including it.
	Indicators models.IndicatorSnapshot

	// CloseTime is Candle.CloseTime, lifted out so it can be asserted on
	// without reaching through the candle.
	CloseTime time.Time

	// Ready is false while this timeframe is still warming up, or while it is
	// recovering from a gap. A view that is not ready contributes nothing;
	// it is not scored as neutral, it is not scored at all.
	Ready bool
}

// TimeframeState is one timeframe's scored contribution, reported alongside
// the aggregate so a bias can be explained rather than merely stated.
type TimeframeState struct {
	Timeframe constants.Timeframe

	// Score is this timeframe's directional reading in [-1, +1].
	Score float64

	// Weight is what the aggregation gave it.
	Weight float64

	// CloseTime is the bar the reading came from. Every value in a report
	// must be traceable to a bar that had closed.
	CloseTime time.Time

	Ready bool
}

// TrendState is what a filter reports for one base bar.
type TrendState struct {
	// Bias is the direction entering is permitted in. Neutral permits
	// nothing: phase 06 must read it as "no entries", not as "no opinion,
	// proceed freely".
	Bias constants.Bias

	// Confidence is the absolute weighted score normalised to [0, 1]. It is
	// zero whenever Ready is false.
	Confidence float64

	// PerTimeframe is ordered shortest timeframe first, for the same
	// determinism reason BarContext.Higher is a slice.
	PerTimeframe []TimeframeState

	// Ready is false until every contributing timeframe has completed its own
	// warm-up, and false again while any of them is recovering from a gap.
	Ready bool

	// NotReadyReason says which of those it is, so an operator reading a run
	// that vetoed everything can tell "still warming up" from "gap recovery"
	// without opening the code.
	NotReadyReason string
}

// Permits reports whether an entry in a direction is allowed.
//
// This is the whole output of the filter. A Neutral or not-ready state
// permits nothing at all, which is the conservative reading and the one the
// spec requires: a filter that has no answer must not be treated as consent.
func (s TrendState) Permits(direction constants.Direction) bool {
	if !s.Ready {
		return false
	}
	switch direction {
	case constants.DirectionLong:
		return s.Bias == constants.BiasBullish
	case constants.DirectionShort:
		return s.Bias == constants.BiasBearish
	default:
		return false
	}
}

// Aligner produces the higher-timeframe views visible at a decision instant.
//
// It is the component that enforces §1: a candle becomes visible only once
// its close_time is at or before the instant being evaluated. Implementations
// hold one cursor per higher timeframe and advance them in lockstep with the
// base stream, so this is a merge rather than a query per bar.
type Aligner interface {
	// Advance reports the views visible at t, which is the base candle's
	// close_time — the instant the decision is being made at.
	//
	// It must be called once per base bar, in chronological order. Calling it
	// out of order is a programming error; an implementation may not rewind.
	Advance(ctx context.Context, t time.Time) ([]TimeframeView, error)

	// WarmupBaseBars is how many base-timeframe bars must pass before every
	// contributing timeframe has finished warming up.
	//
	// This is the number that is easy to underestimate: a 1h EMA(200) at a 5x
	// warm-up needs 1000 hourly closes, which is 60,000 1m bars — six weeks
	// of continuous data before the filter says anything at all.
	WarmupBaseBars() int

	// Timeframes lists the contributing higher timeframes, shortest first.
	Timeframes() []constants.Timeframe
}
