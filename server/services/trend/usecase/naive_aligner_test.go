package usecase_test

import (
	"context"
	"time"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/trend"
)

// naiveAligner is the wrong implementation, kept on purpose.
//
// It does what the obvious first attempt does: find the higher-timeframe
// candle that *contains* the current instant and hand it over. At 14:23 that
// is the 14:00-15:00 hourly bar, which will not finish forming until 15:00 and
// therefore contains 37 minutes of price action that has not happened yet.
//
// It exists so the alignment test can be shown to fail against it. A test that
// has never failed is a test that proves nothing, and this particular mistake
// is invisible in a passing backtest — the numbers simply come out better than
// live will ever reproduce.
type naiveAligner struct {
	series map[constants.Timeframe][]models.Candle
	sets   map[constants.Timeframe]*fakeSnapshots
}

func newNaiveAligner(series map[constants.Timeframe][]models.Candle) *naiveAligner {
	sets := make(map[constants.Timeframe]*fakeSnapshots, len(series))
	for timeframe := range series {
		sets[timeframe] = &fakeSnapshots{}
	}
	return &naiveAligner{series: series, sets: sets}
}

func (a *naiveAligner) Advance(_ context.Context, t time.Time) ([]trend.TimeframeView, error) {
	var views []trend.TimeframeView

	for _, timeframe := range a.Timeframes() {
		for _, candle := range a.series[timeframe] {
			// The bug, stated plainly: "the candle covering t" rather than
			// "the newest candle that had closed by t".
			if !candle.OpenTime.After(t) && candle.CloseTime.After(t) {
				views = append(views, trend.TimeframeView{
					Timeframe:  timeframe,
					Candle:     candle,
					Indicators: a.sets[timeframe].snapshotOf(candle),
					CloseTime:  candle.CloseTime,
					Ready:      true,
				})
				break
			}
		}
	}
	return views, nil
}

func (a *naiveAligner) WarmupBaseBars() int { return 0 }

func (a *naiveAligner) Timeframes() []constants.Timeframe {
	return []constants.Timeframe{constants.Timeframe1h}
}

// fakeSnapshots turns a candle into a snapshot without running the real
// indicators, so an alignment test measures alignment and nothing else.
type fakeSnapshots struct{}

func (f *fakeSnapshots) snapshotOf(c models.Candle) models.IndicatorSnapshot {
	closePrice, _ := c.Close.Float64()
	openPrice, _ := c.Open.Float64()

	// A stand-in EMA that trails the bar: below the close on an up bar, above
	// it on a down bar. Enough for "which way is this timeframe pointing".
	return models.IndicatorSnapshot{
		OpenTime: c.OpenTime,
		EMA:      (closePrice + openPrice) / 2,
		RSI:      50,
		ATR:      1,
		VWAP:     (closePrice + openPrice) / 2,
	}
}
