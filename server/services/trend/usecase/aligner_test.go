package usecase_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/candle"
	_indicator_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/indicator/usecase"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/trend"
	_trend_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/trend/usecase"
)

// fakeCandles serves prepared series per timeframe, with no database.
type fakeCandles struct {
	series map[constants.Timeframe][]models.Candle

	// cursorsOpened counts OpenCursor calls, so a test can prove the aligner
	// opens one cursor per timeframe rather than one per bar.
	cursorsOpened int
}

func (f *fakeCandles) OpenCursor(params candle.FetchCandlesParams) candle.CandleCursor {
	f.cursorsOpened++

	var window []models.Candle
	for _, c := range f.series[params.Timeframe] {
		if c.OpenTime.Before(params.From) {
			continue
		}
		if !params.To.IsZero() && c.OpenTime.After(params.To) {
			break
		}
		window = append(window, c)
	}
	return &sliceCursor{series: window}
}

type sliceCursor struct {
	series []models.Candle
	next   int
}

func (c *sliceCursor) Next(ctx context.Context) (models.Candle, bool, error) {
	if err := ctx.Err(); err != nil {
		return models.Candle{}, false, err
	}
	if c.next >= len(c.series) {
		return models.Candle{}, false, nil
	}
	result := c.series[c.next]
	c.next++
	return result, true, nil
}

func (f *fakeCandles) SaveCandle(context.Context, models.Candle) error    { return nil }
func (f *fakeCandles) SaveCandles(context.Context, []models.Candle) error { return nil }

func (f *fakeCandles) FindGaps(context.Context, string, constants.MarketType, constants.Timeframe) ([]candle.Gap, error) {
	return nil, nil
}

func (f *fakeCandles) FetchCandles(_ context.Context, params candle.FetchCandlesParams) ([]models.Candle, error) {
	return f.series[params.Timeframe], nil
}

func (f *fakeCandles) StreamCandles(ctx context.Context, params candle.FetchCandlesParams, onCandle func(models.Candle) error) error {
	for _, c := range f.series[params.Timeframe] {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := onCandle(c); err != nil {
			return err
		}
	}
	return nil
}

func (f *fakeCandles) FetchLatestCandle(_ context.Context, _ string, _ constants.MarketType, timeframe constants.Timeframe) (models.Candle, error) {
	series := f.series[timeframe]
	if len(series) == 0 {
		return models.Candle{}, constants.ErrNotFound
	}
	return series[len(series)-1], nil
}

func (f *fakeCandles) FetchEarliestCandle(_ context.Context, _ string, _ constants.MarketType, timeframe constants.Timeframe) (models.Candle, error) {
	series := f.series[timeframe]
	if len(series) == 0 {
		return models.Candle{}, constants.ErrNotFound
	}
	return series[0], nil
}

func (f *fakeCandles) CountCandles(_ context.Context, _ string, _ constants.MarketType, timeframe constants.Timeframe) (int64, error) {
	return int64(len(f.series[timeframe])), nil
}

// shortIndicators keeps the warm-up small so a test series need not be
// thousands of bars long. The warm-up rule itself belongs to phase 03.
func shortIndicators() _indicator_us.SetConfig {
	return _indicator_us.SetConfig{EMAPeriod: 2, RSIPeriod: 2, ATRPeriod: 2}
}

// newAligner builds the real aligner over prepared series.
func newAligner(t *testing.T, series map[constants.Timeframe][]models.Candle, higher ...constants.Timeframe) (trend.Aligner, *fakeCandles) {
	t.Helper()

	candles := &fakeCandles{series: series}
	aligner, err := _trend_us.NewAlignerImpl(_trend_us.AlignerConfig{
		Symbol:     "BTCUSDT",
		MarketType: constants.MarketTypeSpot,
		Base:       constants.Timeframe1m,
		Higher:     higher,
		From:       alignDay().Add(-24 * time.Hour),
		To:         alignDay().Add(48 * time.Hour),
		Indicators: shortIndicators(),
	}, candles)
	if err != nil {
		t.Fatalf("NewAlignerImpl() returned error: %v", err)
	}
	return aligner, candles
}

// TestAlignerUsesThePreviousClosedHourlyBar is the phase-05 §1 requirement.
//
// It is the same check TestNaiveAlignerFailsTheAlignmentRule runs against the
// wrong implementation, pointed at the right one. The pair is the point: one
// proves the rule holds, the other proves the check can tell when it does not.
func TestAlignerUsesThePreviousClosedHourlyBar(t *testing.T) {
	aligner, _ := newAligner(t, map[constants.Timeframe][]models.Candle{
		constants.Timeframe1h: hourlySeries(),
	}, constants.Timeframe1h)

	if err := checkUsesPreviousHourlyBar(aligner); err != nil {
		t.Fatal(err)
	}
}

// TestNoContributionEverClosesAfterTheDecisionInstant walks the whole day a
// minute at a time and checks the invariant on every single bar.
//
// The §1 test pins one instant, chosen because it is the interesting one. This
// pins all 1440 of them, which is what catches an off-by-one that only shows
// up on an exact boundary — the top of the hour, where a bar's close_time and
// the decision instant are equal and "at or before" has to mean at.
func TestNoContributionEverClosesAfterTheDecisionInstant(t *testing.T) {
	aligner, _ := newAligner(t, map[constants.Timeframe][]models.Candle{
		constants.Timeframe5m:  minuteMultipleSeries(constants.Timeframe5m, 2),
		constants.Timeframe15m: minuteMultipleSeries(constants.Timeframe15m, 2),
		constants.Timeframe1h:  minuteMultipleSeries(constants.Timeframe1h, 2),
	}, constants.Timeframe5m, constants.Timeframe15m, constants.Timeframe1h)

	ctx := context.Background()
	checked := 0

	for minute := range 24 * 60 {
		at := alignDay().Add(time.Duration(minute) * time.Minute)

		views, err := aligner.Advance(ctx, at)
		if err != nil {
			t.Fatalf("Advance(%s) returned error: %v", at.Format(time.RFC3339), err)
		}
		for _, view := range views {
			checked++
			if view.CloseTime.After(at) {
				t.Fatalf("at %s the %s view comes from a bar closing at %s — look-ahead of %s",
					at.Format(time.RFC3339), view.Timeframe,
					view.CloseTime.Format(time.RFC3339), view.CloseTime.Sub(at))
			}
			if !view.Candle.CloseTime.Equal(view.CloseTime) {
				t.Fatalf("view CloseTime %s does not match its candle's %s",
					view.CloseTime, view.Candle.CloseTime)
			}
		}
	}

	if checked == 0 {
		t.Fatal("no views were checked; the aligner contributed nothing all day")
	}
	t.Logf("checked %d contributions across 1440 decision instants", checked)
}

// TestAlignerAdvancesExactlyOnTheClose pins the boundary the previous test
// sweeps over, so a failure names the instant rather than a minute near it.
func TestAlignerAdvancesExactlyOnTheClose(t *testing.T) {
	aligner, _ := newAligner(t, map[constants.Timeframe][]models.Candle{
		constants.Timeframe1h: minuteMultipleSeries(constants.Timeframe1h, 2),
	}, constants.Timeframe1h)

	ctx := context.Background()
	topOfHour := alignDay().Add(3 * time.Hour)

	// One nanosecond before 03:00 the 02:00-03:00 bar has not closed.
	before, err := aligner.Advance(ctx, topOfHour.Add(-time.Nanosecond))
	if err != nil {
		t.Fatalf("Advance() returned error: %v", err)
	}
	view, ok := viewFor(before, constants.Timeframe1h)
	if !ok {
		t.Fatal("no 1h view just before the close")
	}
	if !view.Candle.OpenTime.Equal(alignDay().Add(time.Hour)) {
		t.Errorf("just before 03:00 the view is the %s bar, want the 01:00 one",
			view.Candle.OpenTime.Format(time.RFC3339))
	}

	// At exactly 03:00 it has. "close_time <= t" must include equality, or
	// every bar would be one period stale for its whole life.
	at, err := aligner.Advance(ctx, topOfHour)
	if err != nil {
		t.Fatalf("Advance() returned error: %v", err)
	}
	view, ok = viewFor(at, constants.Timeframe1h)
	if !ok {
		t.Fatal("no 1h view at the close")
	}
	if !view.Candle.OpenTime.Equal(alignDay().Add(2 * time.Hour)) {
		t.Errorf("at exactly 03:00 the view is the %s bar, want the 02:00 one that just closed",
			view.Candle.OpenTime.Format(time.RFC3339))
	}
}

// TestAlignerOpensOneCursorPerTimeframe is the §5 requirement stated as a
// count. A lookup per bar would be half a million queries for a year of 1m
// data; the merge is one pass per series.
func TestAlignerOpensOneCursorPerTimeframe(t *testing.T) {
	aligner, candles := newAligner(t, map[constants.Timeframe][]models.Candle{
		constants.Timeframe5m: minuteMultipleSeries(constants.Timeframe5m, 1),
		constants.Timeframe1h: minuteMultipleSeries(constants.Timeframe1h, 1),
	}, constants.Timeframe5m, constants.Timeframe1h)

	ctx := context.Background()
	for minute := range 600 {
		if _, err := aligner.Advance(ctx, alignDay().Add(time.Duration(minute)*time.Minute)); err != nil {
			t.Fatalf("Advance() returned error: %v", err)
		}
	}

	if candles.cursorsOpened != 2 {
		t.Errorf("opened %d cursors over 600 bars, want 2 — one per higher timeframe",
			candles.cursorsOpened)
	}
}

// TestAlignerRefusesToRewind. A cursor cannot go backwards, so an out-of-order
// call would return whatever the cursors happen to be sitting on rather than
// the right answer. Failing loudly is the only honest response.
func TestAlignerRefusesToRewind(t *testing.T) {
	aligner, _ := newAligner(t, map[constants.Timeframe][]models.Candle{
		constants.Timeframe1h: minuteMultipleSeries(constants.Timeframe1h, 1),
	}, constants.Timeframe1h)

	ctx := context.Background()
	if _, err := aligner.Advance(ctx, alignDay().Add(5*time.Hour)); err != nil {
		t.Fatalf("Advance() returned error: %v", err)
	}
	if _, err := aligner.Advance(ctx, alignDay().Add(4*time.Hour)); err == nil {
		t.Fatal("Advance() accepted a call going backwards in time")
	}
}

// TestWarmupIsCountedInBaseBars is the number §2 warns is easy to
// underestimate.
//
// A 1h contributor warming up over N hourly closes is silent for N*60 1m bars.
// Reporting the hourly count would understate the wait by a factor of sixty,
// and a run shorter than the real figure is vetoed end to end.
func TestWarmupIsCountedInBaseBars(t *testing.T) {
	aligner, _ := newAligner(t, map[constants.Timeframe][]models.Candle{
		constants.Timeframe5m: minuteMultipleSeries(constants.Timeframe5m, 1),
		constants.Timeframe1h: minuteMultipleSeries(constants.Timeframe1h, 1),
	}, constants.Timeframe5m, constants.Timeframe1h)

	set, err := _indicator_us.NewSet(shortIndicators())
	if err != nil {
		t.Fatalf("build set: %v", err)
	}
	closes := set.WarmupPeriod()

	// The 1h contributor dominates: the same number of closes costs sixty
	// times as many base bars as it would at 1m.
	want := closes * 60
	if got := aligner.WarmupBaseBars(); got != want {
		t.Errorf("WarmupBaseBars() = %d, want %d (%d hourly closes x 60 base bars each)",
			got, want, closes)
	}
}

// TestProductionWarmupIsSixWeeks documents the real number, so nobody starts a
// two-week backtest with the filter on and wonders why it vetoed everything.
func TestProductionWarmupIsSixWeeks(t *testing.T) {
	aligner, _ := newAligner(t, map[constants.Timeframe][]models.Candle{
		constants.Timeframe1h: minuteMultipleSeries(constants.Timeframe1h, 1),
	}, constants.Timeframe1h)

	// Rebuild with the real periods rather than the shortened test ones.
	candles := &fakeCandles{series: map[constants.Timeframe][]models.Candle{
		constants.Timeframe1h: minuteMultipleSeries(constants.Timeframe1h, 1),
	}}
	production, err := _trend_us.NewAlignerImpl(_trend_us.AlignerConfig{
		Symbol:     "BTCUSDT",
		MarketType: constants.MarketTypeSpot,
		Base:       constants.Timeframe1m,
		Higher:     []constants.Timeframe{constants.Timeframe1h},
		From:       alignDay(),
		To:         alignDay().Add(24 * time.Hour),
		Indicators: _indicator_us.DefaultSetConfig(),
	}, candles)
	if err != nil {
		t.Fatalf("NewAlignerImpl() returned error: %v", err)
	}

	// EMA(200) at phase 03's 5x warm-up is 1000 hourly closes, and each hour
	// is 60 base bars.
	const want = 1000 * 60
	if got := production.WarmupBaseBars(); got != want {
		t.Errorf("production warm-up is %d base bars, want %d", got, want)
	}
	t.Logf("with a 1h EMA(200), the filter says nothing for %d 1m bars — %.0f days",
		want, float64(want)/(24*60))

	_ = aligner
}

// TestHigherTimeframeMustActuallyBeHigher. A contributor closing no less often
// than the base adds nothing but an extra chance to misalign.
func TestHigherTimeframeMustActuallyBeHigher(t *testing.T) {
	candles := &fakeCandles{series: map[constants.Timeframe][]models.Candle{}}

	_, err := _trend_us.NewAlignerImpl(_trend_us.AlignerConfig{
		Symbol:     "BTCUSDT",
		MarketType: constants.MarketTypeSpot,
		Base:       constants.Timeframe5m,
		Higher:     []constants.Timeframe{constants.Timeframe1m},
		Indicators: shortIndicators(),
	}, candles)

	if err == nil {
		t.Fatal("a 1m contributor was accepted under a 5m base timeframe")
	}
}

// decimalOf renders an integer price as a decimal.
func decimalOf(v int64) decimal.Decimal {
	return decimal.RequireFromString(strconv.FormatInt(v, 10))
}

// minuteMultipleSeries builds `days` days of candles for a timeframe, rising
// steadily so every bar is distinguishable from its neighbours.
func minuteMultipleSeries(timeframe constants.Timeframe, days int) []models.Candle {
	duration := timeframe.Duration()
	count := int(time.Duration(days) * 24 * time.Hour / duration)

	series := make([]models.Candle, 0, count)
	at := alignDay()

	for i := range count {
		price := 27000 + int64(i)
		series = append(series, models.Candle{
			Symbol:      "BTCUSDT",
			MarketType:  constants.MarketTypeSpot,
			Timeframe:   timeframe,
			OpenTime:    at,
			CloseTime:   at.Add(duration),
			Open:        decimalOf(price),
			High:        decimalOf(price + 5),
			Low:         decimalOf(price - 5),
			Close:       decimalOf(price + 1),
			Volume:      decimalOf(100),
			QuoteVolume: decimalOf(price * 100),
			TradeCount:  500,
			IsClosed:    true,
		})
		at = at.Add(duration)
	}
	return series
}
