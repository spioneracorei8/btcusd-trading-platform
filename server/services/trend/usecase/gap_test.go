package usecase_test

import (
	"context"
	"testing"
	"time"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	_indicator_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/indicator/usecase"
	_trend_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/trend/usecase"
)

// The March 2023 outage, as phase 04 records it. Binance halted spot trading
// during a matching-engine incident, so no candle exists for the window on any
// timeframe — including the hourly one, where a single missing bar leaves the
// filter reasoning about sixty base bars it cannot see.
//
// The bounds come from the phase-04 spec and have not been re-verified against
// Binance's own record from this environment. See ADR 0013.
var (
	outageStart = time.Date(2023, 3, 24, 12, 40, 0, 0, time.UTC)
	outageEnd   = time.Date(2023, 3, 24, 14, 0, 0, 0, time.UTC)
)

// hourlySeriesWithOutage builds hourly candles across the outage day with the
// bars covering the halt genuinely absent, which is what a real gap looks
// like: not a marker, an absence.
func hourlySeriesWithOutage(hoursBefore, hoursAfter int) []models.Candle {
	var series []models.Candle

	start := outageStart.Truncate(time.Hour).Add(-time.Duration(hoursBefore) * time.Hour)
	for at := start; at.Before(outageStart.Truncate(time.Hour)); at = at.Add(time.Hour) {
		series = append(series, hourlyBar(at, 27000))
	}

	// The 12:00 and 13:00 bars are missing: 12:00-13:00 was cut short by the
	// halt at 12:40 and 13:00-14:00 fell entirely inside it.
	resume := outageEnd
	for i := range hoursAfter {
		series = append(series, hourlyBar(resume.Add(time.Duration(i)*time.Hour), 28000+int64(i)))
	}
	return series
}

func hourlyBar(at time.Time, price int64) models.Candle {
	return models.Candle{
		Symbol:      "BTCUSDT",
		MarketType:  constants.MarketTypeSpot,
		Timeframe:   constants.Timeframe1h,
		OpenTime:    at,
		CloseTime:   at.Add(time.Hour),
		Open:        decimalOf(price),
		High:        decimalOf(price + 50),
		Low:         decimalOf(price - 50),
		Close:       decimalOf(price + 10),
		Volume:      decimalOf(500),
		QuoteVolume: decimalOf(price * 500),
		TradeCount:  5000,
		IsClosed:    true,
	}
}

// TestFilterIsNotReadyAcrossAndAfterTheOutage is phase-05 §6.
//
// Two separate claims, and the second is the one that is easy to get wrong:
//
//  1. During the halt nothing new closes, so the filter has nothing fresh and
//     must not pretend otherwise.
//  2. *After* the halt the hourly indicators are stale by however long it ran.
//     Resuming as though nothing happened would carry a pre-outage EMA forward
//     as if it described post-outage price — here a $1,000 jump. The filter
//     must stay not-ready until the timeframe has re-earned its warm-up.
func TestFilterIsNotReadyAcrossAndAfterTheOutage(t *testing.T) {
	set, err := _indicator_us.NewSet(shortIndicators())
	if err != nil {
		t.Fatalf("build set: %v", err)
	}
	warmupCloses := set.WarmupPeriod()

	// Enough history before the halt to be fully warm, and enough after to
	// re-earn the warm-up with room to spare.
	series := hourlySeriesWithOutage(warmupCloses+4, warmupCloses+6)

	candles := &fakeCandles{series: map[constants.Timeframe][]models.Candle{
		constants.Timeframe1h: series,
	}}
	aligner, err := _trend_us.NewAlignerImpl(_trend_us.AlignerConfig{
		Symbol:     "BTCUSDT",
		MarketType: constants.MarketTypeSpot,
		Base:       constants.Timeframe1m,
		Higher:     []constants.Timeframe{constants.Timeframe1h},
		From:       series[0].OpenTime,
		To:         series[len(series)-1].CloseTime,
		Indicators: shortIndicators(),
	}, candles)
	if err != nil {
		t.Fatalf("NewAlignerImpl() returned error: %v", err)
	}

	ctx := context.Background()

	// Warm before the halt: walk up to the last close that precedes it.
	beforeHalt := outageStart.Truncate(time.Hour)
	views, err := aligner.Advance(ctx, beforeHalt)
	if err != nil {
		t.Fatalf("Advance() returned error: %v", err)
	}
	view, ok := viewFor(views, constants.Timeframe1h)
	if !ok || !view.Ready {
		t.Fatalf("the 1h contributor is not ready before the halt (ok=%v); "+
			"the test cannot show it going not-ready if it never was ready", ok)
	}

	// Across the halt: no new hourly bar closes, so the newest contribution is
	// the pre-halt one and it goes on being stale by more and more.
	for at := outageStart; at.Before(outageEnd); at = at.Add(10 * time.Minute) {
		views, err := aligner.Advance(ctx, at)
		if err != nil {
			t.Fatalf("Advance(%s) returned error: %v", at.Format(time.RFC3339), err)
		}
		view, ok := viewFor(views, constants.Timeframe1h)
		if !ok {
			continue
		}
		if view.CloseTime.After(at) {
			t.Fatalf("during the halt the 1h view closes at %s, after %s",
				view.CloseTime.Format(time.RFC3339), at.Format(time.RFC3339))
		}
		if !view.CloseTime.Before(outageStart) {
			t.Errorf("during the halt the 1h view closes at %s, inside the outage window",
				view.CloseTime.Format(time.RFC3339))
		}
	}

	// The first bar to close after the halt breaks the sequence, and that must
	// reset the timeframe rather than be absorbed silently.
	firstAfter := outageEnd.Add(time.Hour)
	views, err = aligner.Advance(ctx, firstAfter)
	if err != nil {
		t.Fatalf("Advance() returned error: %v", err)
	}
	view, ok = viewFor(views, constants.Timeframe1h)
	if !ok {
		t.Fatal("no 1h view after the halt")
	}
	if view.Ready {
		t.Fatal("the 1h contributor is ready on the first bar after the halt.\n" +
			"Its indicators still carry pre-outage state across a gap the market " +
			"moved $1,000 through; the warm-up has to be re-earned.")
	}

	// And it must recover eventually, or the filter would be dead for the rest
	// of the run rather than merely cautious.
	recoveredAt := outageEnd.Add(time.Duration(warmupCloses+2) * time.Hour)
	views, err = aligner.Advance(ctx, recoveredAt)
	if err != nil {
		t.Fatalf("Advance() returned error: %v", err)
	}
	view, ok = viewFor(views, constants.Timeframe1h)
	if !ok || !view.Ready {
		t.Errorf("the 1h contributor has not recovered %d closes after the halt (ok=%v)",
			warmupCloses+2, ok)
	}
}

// TestRecoveryTakesTheFullWarmup pins how long "not-ready" lasts, since the
// spec asks for the correct recovery window rather than merely some window.
//
// The answer is the whole warm-up again. A shorter one would mean scoring
// against an EMA that is still mostly the seed it restarted from, which is the
// same unconverged value phase 03's warm-up rule exists to withhold.
func TestRecoveryTakesTheFullWarmup(t *testing.T) {
	set, err := _indicator_us.NewSet(shortIndicators())
	if err != nil {
		t.Fatalf("build set: %v", err)
	}
	warmupCloses := set.WarmupPeriod()

	series := hourlySeriesWithOutage(warmupCloses+4, warmupCloses+6)
	candles := &fakeCandles{series: map[constants.Timeframe][]models.Candle{
		constants.Timeframe1h: series,
	}}
	aligner, err := _trend_us.NewAlignerImpl(_trend_us.AlignerConfig{
		Symbol:     "BTCUSDT",
		MarketType: constants.MarketTypeSpot,
		Base:       constants.Timeframe1m,
		Higher:     []constants.Timeframe{constants.Timeframe1h},
		From:       series[0].OpenTime,
		To:         series[len(series)-1].CloseTime,
		Indicators: shortIndicators(),
	}, candles)
	if err != nil {
		t.Fatalf("NewAlignerImpl() returned error: %v", err)
	}

	ctx := context.Background()
	readyAfter := -1

	// Step hour by hour from the resumption and record when readiness returns.
	for closes := 1; closes <= warmupCloses+5; closes++ {
		at := outageEnd.Add(time.Duration(closes) * time.Hour)

		views, err := aligner.Advance(ctx, at)
		if err != nil {
			t.Fatalf("Advance() returned error: %v", err)
		}
		view, ok := viewFor(views, constants.Timeframe1h)
		if ok && view.Ready {
			readyAfter = closes
			break
		}
	}

	if readyAfter != warmupCloses {
		t.Errorf("readiness returned after %d fresh closes, want %d — the full warm-up again",
			readyAfter, warmupCloses)
	}
	t.Logf("after the halt the 1h contributor needed %d fresh closes; at the production "+
		"EMA(200) that would be 1000 hours, about 42 days", readyAfter)
}
