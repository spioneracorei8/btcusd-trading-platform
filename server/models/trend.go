package models

import (
	"time"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
)

// TimeframeReading is one higher timeframe's contribution at a decision point.
//
// It lives here rather than in services/trend for the reason CLAUDE.md §5
// gives for IndicatorSnapshot: two services need it. The trend filter has
// always consumed it, and from phase 06 a strategy may consume it too — and a
// service interface file may import models and constants only. services/trend
// aliases it as trend.TimeframeView.
//
// # CloseTime is evidence, not decoration
//
// It is the proof that this reading came from a bar that had definitively
// closed at the instant being evaluated. At 14:23 the 1m candle for 14:22 has
// closed and the 1h candle for 14:00 has not; a reading whose CloseTime is
// after the decision instant contains the next 37 minutes of price action.
// The alignment tests assert on this field, so a look-ahead bug shows up as a
// failing test rather than as an excellent backtest nobody can reproduce.
type TimeframeReading struct {
	Timeframe constants.Timeframe

	// Candle is the most recent candle of this timeframe whose close_time is
	// at or before the decision instant.
	Candle Candle

	// Indicators are that candle's values, computed only from candles up to
	// and including it.
	Indicators IndicatorSnapshot

	// CloseTime is Candle.CloseTime, lifted out so it can be asserted on
	// without reaching through the candle.
	CloseTime time.Time

	// Ready is false while this timeframe is still warming up, or while it is
	// recovering from a gap. A reading that is not ready contributes nothing;
	// it is not read as neutral, it is not read at all.
	Ready bool
}

// TrendSnapshot is the set of higher-timeframe readings visible to a strategy
// at one decision point.
//
// # Why a strategy may hold this at all
//
// A trend filter is a veto applied to another strategy's signals. This is the
// other shape: a strategy whose entry condition *is* the agreement of several
// timeframes. The distinction matters for evaluation — a filter's contribution
// is measured by running with and without it, and a strategy built this way
// has no unfiltered counterpart to compare against.
//
// It is carried as a pointer on BarContext so that "this strategy asked for no
// higher timeframes" is a different value from "it asked, and none were
// ready". The three single-timeframe strategies leave it nil and cannot
// observe one by accident.
type TrendSnapshot struct {
	// Readings is ordered shortest timeframe first. A slice rather than a map:
	// Go randomises map iteration, and a backtest report must be byte-identical
	// across runs, which a map in this position would quietly break.
	Readings []TimeframeReading
}

// For returns the reading for a timeframe.
//
// It returns ok=false rather than a zero value, because a zero reading has a
// zero price and a false Ready, and code that read it as real would derive a
// direction from a candle that does not exist.
func (s *TrendSnapshot) For(timeframe constants.Timeframe) (TimeframeReading, bool) {
	if s == nil {
		return TimeframeReading{}, false
	}
	for _, reading := range s.Readings {
		if reading.Timeframe == timeframe {
			return reading, true
		}
	}
	return TimeframeReading{}, false
}

// Ready reports whether every requested timeframe has a reading and all of
// them are warm.
//
// All, not any. A strategy that requires several timeframes to agree cannot
// act on a subset: the missing one is the one that would have disagreed as
// often as not, and treating absence as consent is how a warm-up hole turns
// into a run that traded on less evidence than it claimed to.
func (s *TrendSnapshot) Ready(timeframes ...constants.Timeframe) bool {
	if s == nil {
		return false
	}
	for _, timeframe := range timeframes {
		reading, ok := s.For(timeframe)
		if !ok || !reading.Ready {
			return false
		}
	}
	return len(timeframes) > 0
}
