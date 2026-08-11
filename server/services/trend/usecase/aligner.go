package usecase

import (
	"context"
	"fmt"
	"time"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/candle"
	_indicator_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/indicator/usecase"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/trend"
)

// AlignerConfig describes the higher timeframes a run consults.
type AlignerConfig struct {
	Symbol     string
	MarketType constants.MarketType

	// Base is the timeframe the engine iterates. It is not a contributor —
	// its snapshot reaches the filter directly from the engine.
	Base constants.Timeframe

	// Higher are the contributing timeframes, in any order; the aligner
	// sorts them shortest first so output ordering never depends on how a
	// caller happened to list them.
	Higher []constants.Timeframe

	// From and To bound the candles each cursor reads. From should already
	// include the higher timeframes' warm-up: a run that starts its cursors
	// at the range start will spend the whole range warming up.
	From time.Time
	To   time.Time

	Indicators _indicator_us.SetConfig
}

// aligner merges one cursor per higher timeframe against the base stream.
//
// # The rule it exists to enforce
//
// A higher-timeframe candle becomes visible only once its close_time is at or
// before the instant being evaluated. Nothing else in the system enforces
// this, and nothing downstream can detect its absence: a backtest with the
// rule broken does not fail, it produces better numbers than live will ever
// reproduce.
//
// # Why this is a merge and not a lookup
//
// Both series are ordered by open_time and the base advances monotonically, so
// each higher timeframe needs a cursor that steps forward when its next candle
// has closed and stays put otherwise. That is one pass over each series. A
// query per base bar would be 500,000 queries for a year of 1m data.
type aligner struct {
	config      AlignerConfig
	candles     candle.CandleUsecase
	timeframes  []constants.Timeframe
	streams     map[constants.Timeframe]*timeframeStream
	lastAdvance time.Time
}

// NewAlignerImpl builds the aligner over the stored candle series.
func NewAlignerImpl(config AlignerConfig, candles candle.CandleUsecase) (trend.Aligner, error) {
	if !config.Base.Valid() {
		return nil, fmt.Errorf("trend: base timeframe %q is not valid", config.Base)
	}

	ordered := sortedTimeframes(config.Higher)
	streams := make(map[constants.Timeframe]*timeframeStream, len(ordered))

	for _, timeframe := range ordered {
		if timeframe.Duration() <= config.Base.Duration() {
			return nil, fmt.Errorf(
				"trend: %s is not higher than the base timeframe %s; a contributor that "+
					"closes no less often than the base adds nothing but a chance to misalign",
				timeframe, config.Base)
		}

		set, err := _indicator_us.NewSet(config.Indicators)
		if err != nil {
			return nil, fmt.Errorf("trend: build %s indicators: %w", timeframe, err)
		}

		streams[timeframe] = &timeframeStream{
			timeframe: timeframe,
			set:       set,
			cursor: candles.OpenCursor(candle.FetchCandlesParams{
				Symbol:     config.Symbol,
				MarketType: config.MarketType,
				Timeframe:  timeframe,
				From:       config.From,
				To:         config.To,
			}),
			warmupCloses: set.WarmupPeriod(),
		}
	}

	return &aligner{
		config:     config,
		candles:    candles,
		timeframes: ordered,
		streams:    streams,
	}, nil
}

// sortedTimeframes orders by duration, shortest first, dropping duplicates.
func sortedTimeframes(timeframes []constants.Timeframe) []constants.Timeframe {
	seen := make(map[constants.Timeframe]struct{}, len(timeframes))
	out := make([]constants.Timeframe, 0, len(timeframes))

	for _, timeframe := range timeframes {
		if _, dup := seen[timeframe]; dup {
			continue
		}
		seen[timeframe] = struct{}{}
		out = append(out, timeframe)
	}

	// Insertion sort: the list is four entries at most, and this keeps the
	// ordering obvious rather than hidden behind a comparator.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].Duration() < out[j-1].Duration(); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// Advance reports the views visible at t.
func (a *aligner) Advance(ctx context.Context, t time.Time) ([]trend.TimeframeView, error) {
	// A cursor cannot rewind, so going backwards would silently return stale
	// views rather than the right ones. Refusing is the only honest answer.
	if !a.lastAdvance.IsZero() && t.Before(a.lastAdvance) {
		return nil, fmt.Errorf(
			"trend: Advance called with %s after %s; the aligner only moves forward",
			t.Format(time.RFC3339), a.lastAdvance.Format(time.RFC3339))
	}
	a.lastAdvance = t

	views := make([]trend.TimeframeView, 0, len(a.timeframes))
	for _, timeframe := range a.timeframes {
		view, ok, err := a.streams[timeframe].advanceTo(ctx, t)
		if err != nil {
			return nil, err
		}
		if !ok {
			// Nothing of this timeframe has closed yet. Emitting a zero view
			// would be scored as a real reading of zero.
			continue
		}

		assertClosedBy(view, t)
		views = append(views, view)
	}
	return views, nil
}

// WarmupBaseBars is how many base bars must pass before every contributor is
// warm.
//
// The conversion is the part that is easy to get wrong by an order of
// magnitude. A 1h EMA(200) at phase 03's 5x warm-up needs 1000 hourly closes;
// each hour is 60 base bars at 1m, so the filter says nothing for 60,000 bars —
// about six weeks of continuous data. A run shorter than that will be vetoed
// end to end, which is correct and worth knowing before starting it.
func (a *aligner) WarmupBaseBars() int {
	baseDuration := a.config.Base.Duration()
	if baseDuration <= 0 {
		return 0
	}

	longest := 0
	for _, timeframe := range a.timeframes {
		barsPerClose := int(timeframe.Duration() / baseDuration)
		if required := a.streams[timeframe].warmupCloses * barsPerClose; required > longest {
			longest = required
		}
	}
	return longest
}

// Timeframes lists the contributors, shortest first.
func (a *aligner) Timeframes() []constants.Timeframe {
	return append([]constants.Timeframe(nil), a.timeframes...)
}

// timeframeStream is one higher timeframe's cursor and indicator state.
type timeframeStream struct {
	timeframe constants.Timeframe
	cursor    candle.CandleCursor
	set       *_indicator_us.Set

	// pending is a candle read from the cursor whose close_time is still in
	// the future. It is held, never contributed, and never fed to the
	// indicators — reading ahead is how the merge knows when to stop, and it
	// is only look-ahead if the value escapes.
	pending    models.Candle
	hasPending bool
	exhausted  bool

	// current is the newest candle whose close_time was at or before the last
	// instant asked about, and snapshot is its indicator reading.
	current    models.Candle
	hasCurrent bool
	snapshot   models.IndicatorSnapshot
	ready      bool

	warmupCloses int
	closesSeen   int
}

// advanceTo moves this timeframe forward to the newest candle that had closed
// by t, and reports what it contributes.
func (s *timeframeStream) advanceTo(ctx context.Context, t time.Time) (trend.TimeframeView, bool, error) {
	for {
		if !s.hasPending {
			if s.exhausted {
				break
			}
			next, ok, err := s.cursor.Next(ctx)
			if err != nil {
				return trend.TimeframeView{}, false, fmt.Errorf("trend: read %s candle: %w", s.timeframe, err)
			}
			if !ok {
				s.exhausted = true
				break
			}
			s.pending, s.hasPending = next, true
		}

		// The rule, in one line: a candle contributes only once it has closed.
		if s.pending.CloseTime.After(t) {
			break
		}

		s.consume(s.pending)
		s.hasPending = false
	}

	if !s.hasCurrent {
		return trend.TimeframeView{}, false, nil
	}
	return trend.TimeframeView{
		Timeframe:  s.timeframe,
		Candle:     s.current,
		Indicators: s.snapshot,
		CloseTime:  s.current.CloseTime,
		Ready:      s.ready,
	}, true, nil
}

// consume feeds one closed candle into this timeframe's indicators.
func (s *timeframeStream) consume(c models.Candle) {
	// A hole in this timeframe's own series makes its indicators stale by
	// however long the hole ran. Resuming as if nothing happened would carry a
	// pre-gap EMA forward as though it described post-gap price; the honest
	// answer is to start over and say so until the warm-up is re-earned.
	if s.hasCurrent && !c.OpenTime.Equal(s.current.OpenTime.Add(s.timeframe.Duration())) {
		s.set.Reset()
		s.closesSeen = 0
		s.ready = false
	}

	snapshot, ok := s.set.Update(c)
	s.closesSeen++
	s.current, s.hasCurrent = c, true
	s.snapshot = snapshot
	s.ready = ok && s.closesSeen >= s.warmupCloses
}
