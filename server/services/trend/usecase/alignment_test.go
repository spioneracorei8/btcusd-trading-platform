package usecase_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/trend"
)

// The alignment scenario, built once and used by every test below.
//
// The shape is chosen so the two possible answers are opposites and cannot be
// confused for one another:
//
//	13:00-14:00  hourly bar, closed   27000 -> 26000   falling hard
//	14:00-15:00  hourly bar, forming  26000 -> 30000   rising hard
//
// The decision instant is 14:23, at which the 1m bar for 14:22 has closed. The
// only hourly bar that had definitively closed by then is the 13:00 one, and
// it points down. An implementation that reaches for the bar *containing*
// 14:23 sees the 14:00 bar and reports up — using four thousand dollars of
// price action that has not happened yet.
const (
	alignHour  = 14
	alignPrior = "27000"
	alignDrop  = "26000"
	alignSpike = "30000"
)

func alignDay() time.Time { return time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC) }

// decisionInstant is the close_time of the 1m bar for 14:22 — the moment the
// engine is deciding at.
func decisionInstant() time.Time {
	return alignDay().Add(time.Duration(alignHour)*time.Hour + 23*time.Minute)
}

// hourlySeries is the two hourly bars described above, plus enough earlier
// history that an implementation has something to walk through.
func hourlySeries() []models.Candle {
	priorClose := alignDay().Add(time.Duration(alignHour) * time.Hour)

	var series []models.Candle
	// Quiet hours before the interesting pair.
	for h := range alignHour - 1 {
		at := alignDay().Add(time.Duration(h) * time.Hour)
		series = append(series, hourCandle(at, alignPrior, alignPrior))
	}

	// 13:00-14:00, closed by the decision instant, falling.
	series = append(series, hourCandle(priorClose.Add(-time.Hour), alignPrior, alignDrop))
	// 14:00-15:00, still forming at the decision instant, rising hard.
	series = append(series, hourCandle(priorClose, alignDrop, alignSpike))
	return series
}

func hourCandle(openTime time.Time, open, closePrice string) models.Candle {
	high, low := open, closePrice
	if closePrice > open {
		high, low = closePrice, open
	}
	return models.Candle{
		Symbol:      "BTCUSDT",
		MarketType:  constants.MarketTypeSpot,
		Timeframe:   constants.Timeframe1h,
		OpenTime:    openTime,
		CloseTime:   openTime.Add(time.Hour),
		Open:        decimal.RequireFromString(open),
		High:        decimal.RequireFromString(high),
		Low:         decimal.RequireFromString(low),
		Close:       decimal.RequireFromString(closePrice),
		Volume:      decimal.RequireFromString("100"),
		QuoteVolume: decimal.RequireFromString("2700000"),
		TradeCount:  1000,
		IsClosed:    true,
	}
}

// checkUsesPreviousHourlyBar is the §1 requirement itself, factored out so it
// can be pointed at any Aligner.
//
// It returns an error rather than failing a test, because it is used twice:
// once expecting success against the real implementation, and once expecting
// failure against the naive one. An assertion that can only fail a test cannot
// be checked for being able to fail.
//
// It is written as a property, not an expected value: whatever the aligner
// returns must have closed by the decision instant. That catches the whole
// class of mistake rather than only the one the naive implementation makes.
func checkUsesPreviousHourlyBar(aligner trend.Aligner) error {
	at := decisionInstant()

	views, err := aligner.Advance(context.Background(), at)
	if err != nil {
		return fmt.Errorf("Advance() returned error: %w", err)
	}
	if len(views) == 0 {
		return errors.New("no higher-timeframe view at all; the aligner contributed nothing to score")
	}

	view, ok := viewFor(views, constants.Timeframe1h)
	if !ok {
		return errors.New("no 1h view returned")
	}

	// The property: nothing may be contributed by a bar that had not closed.
	if view.CloseTime.After(at) {
		return fmt.Errorf("LOOK-AHEAD: the 1h view comes from a bar closing at %s, "+
			"after the decision instant %s — it contains %s of price action that has not happened yet",
			view.CloseTime.Format(time.RFC3339), at.Format(time.RFC3339),
			view.CloseTime.Sub(at))
	}

	// The specific answer too, so an implementation that was merely
	// conservative — returning something far too old — would not pass either.
	wantOpen := alignDay().Add(time.Duration(alignHour-1) * time.Hour)
	if !view.Candle.OpenTime.Equal(wantOpen) {
		return fmt.Errorf("the 1h view comes from the bar opening at %s, want %s "+
			"(the newest bar that had closed by %s)",
			view.Candle.OpenTime.Format(time.RFC3339), wantOpen.Format(time.RFC3339),
			at.Format(time.RFC3339))
	}

	// The direction it implies must be the falling one. This is what reads
	// "rising" on the naive implementation.
	if !view.Candle.Close.LessThan(view.Candle.Open) {
		return fmt.Errorf("the contributed 1h bar is rising (%s -> %s); the bar that had "+
			"closed by the decision instant was falling",
			view.Candle.Open, view.Candle.Close)
	}
	return nil
}

func viewFor(views []trend.TimeframeView, timeframe constants.Timeframe) (trend.TimeframeView, bool) {
	for _, view := range views {
		if view.Timeframe == timeframe {
			return view, true
		}
	}
	return trend.TimeframeView{}, false
}

// TestNaiveAlignerFailsTheAlignmentRule is the failing case, kept.
//
// The phase-05 spec asks for the alignment test to be written before the
// implementation and watched to fail. Keeping the wrong implementation beside
// the right one turns that one-off exercise into a standing check: if this
// test ever stops reporting a violation, the assertion has stopped being able
// to detect one, and the real test next to it is no longer proving anything.
func TestNaiveAlignerFailsTheAlignmentRule(t *testing.T) {
	naive := newNaiveAligner(map[constants.Timeframe][]models.Candle{
		constants.Timeframe1h: hourlySeries(),
	})

	err := checkUsesPreviousHourlyBar(naive)
	if err == nil {
		t.Fatal("the naive aligner passed the alignment check.\n" +
			"That means the check cannot detect look-ahead across timeframes, " +
			"so the real alignment test proves nothing.")
	}
	t.Logf("naive aligner rejected as expected: %v", err)
}
