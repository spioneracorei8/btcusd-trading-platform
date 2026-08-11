package usecase_test

import (
	"context"
	"io"
	"log/slog"
	"time"

	"github.com/shopspring/decimal"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/backtest"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/candle"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/datagap"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/strategy"
)

// ---------------------------------------------------------------------------
// Fixture strategies.
//
// The engine is verified against strategies whose correct answer is known in
// advance, because the alternative — sketching "just a simple strategy" to
// test with — produces a number nobody can check. Every one of these has an
// arithmetic answer that can be computed by hand from the fixture series.
// ---------------------------------------------------------------------------

// alwaysFlat never trades. Any fill at all is a phantom.
type alwaysFlat struct{}

func (alwaysFlat) OnBar(strategy.BarContext) []strategy.Intent { return nil }
func (alwaysFlat) WarmupPeriod() int                           { return 0 }
func (alwaysFlat) Name() string                                { return "always_flat" }
func (alwaysFlat) Version() string                             { return "v1" }

// buyAndHold enters once and never exits. Its result is the price change over
// the held range, minus exactly one round trip of costs.
type buyAndHold struct{}

func (buyAndHold) OnBar(bar strategy.BarContext) []strategy.Intent {
	if bar.Position.IsOpen() {
		return nil
	}
	return []strategy.Intent{strategy.EnterLong("buy and hold")}
}
func (buyAndHold) WarmupPeriod() int { return 0 }
func (buyAndHold) Name() string      { return "buy_and_hold" }
func (buyAndHold) Version() string   { return "v1" }

// guaranteedLoss enters whenever flat and exits whenever open, so it pays a
// round trip as often as the engine allows and never holds a view.
//
// It is the strongest single test of the cost model: its net result must be
// exactly the negative of the costs it accrued, because it never gives the
// price a chance to move in its favour on a flat series.
type guaranteedLoss struct{}

func (guaranteedLoss) OnBar(bar strategy.BarContext) []strategy.Intent {
	if bar.Position.IsOpen() {
		return []strategy.Intent{strategy.Exit("immediate")}
	}
	return []strategy.Intent{strategy.EnterLong("immediate")}
}
func (guaranteedLoss) WarmupPeriod() int { return 0 }
func (guaranteedLoss) Name() string      { return "guaranteed_loss" }
func (guaranteedLoss) Version() string   { return "v1" }

// alternating enters and exits every n bars, so the trade count is exactly
// predictable and cost accounting can be checked against it.
type alternating struct {
	everyN int
	bar    int
}

func (a *alternating) OnBar(bar strategy.BarContext) []strategy.Intent {
	a.bar++
	if a.bar%a.everyN != 0 {
		return nil
	}
	if bar.Position.IsOpen() {
		return []strategy.Intent{strategy.Exit("alternate")}
	}
	return []strategy.Intent{strategy.EnterLong("alternate")}
}
func (a *alternating) WarmupPeriod() int { return 0 }
func (a *alternating) Name() string      { return "alternating" }
func (a *alternating) Version() string   { return "v1" }

// longWithLevels enters once and attaches a stop and a target, which is what
// exercises the stop-before-target rule.
type longWithLevels struct {
	stop   decimal.Decimal
	target decimal.Decimal
	armed  bool
}

func (s *longWithLevels) OnBar(bar strategy.BarContext) []strategy.Intent {
	if !bar.Position.IsOpen() && !s.armed {
		s.armed = true
		return []strategy.Intent{strategy.EnterLong("with levels")}
	}
	if bar.Position.IsOpen() && bar.Position.Stop.IsZero() {
		return []strategy.Intent{
			strategy.SetStop(s.stop, "fixed stop"),
			strategy.SetTarget(s.target, "fixed target"),
		}
	}
	return nil
}
func (s *longWithLevels) WarmupPeriod() int { return 0 }
func (s *longWithLevels) Name() string      { return "long_with_levels" }
func (s *longWithLevels) Version() string   { return "v1" }

// shortOnce asks for a short on its first bar, which a spot run must refuse.
type shortOnce struct{ asked bool }

func (s *shortOnce) OnBar(strategy.BarContext) []strategy.Intent {
	if s.asked {
		return nil
	}
	s.asked = true
	return []strategy.Intent{strategy.EnterShort("short it")}
}
func (s *shortOnce) WarmupPeriod() int { return 0 }
func (s *shortOnce) Name() string      { return "short_once" }
func (s *shortOnce) Version() string   { return "v1" }

// recordingStrategy captures what it was shown, so the engine's promises
// about ordering and position state can be checked from the inside.
type recordingStrategy struct {
	bars      []strategy.BarContext
	onEachBar func(strategy.BarContext) []strategy.Intent
}

func (r *recordingStrategy) OnBar(bar strategy.BarContext) []strategy.Intent {
	r.bars = append(r.bars, bar)
	if r.onEachBar == nil {
		return nil
	}
	return r.onEachBar(bar)
}
func (r *recordingStrategy) WarmupPeriod() int { return 0 }
func (r *recordingStrategy) Name() string      { return "recording" }
func (r *recordingStrategy) Version() string   { return "v1" }

// ---------------------------------------------------------------------------
// Fakes. Nothing here touches a database or the network.
// ---------------------------------------------------------------------------

// fakeCandles serves a prepared series.
type fakeCandles struct {
	series []models.Candle
}

func (f *fakeCandles) StreamCandles(ctx context.Context, params candle.FetchCandlesParams, onCandle func(models.Candle) error) error {
	for _, c := range f.series {
		if err := ctx.Err(); err != nil {
			return err
		}
		if c.OpenTime.Before(params.From) {
			continue
		}
		if !params.To.IsZero() && c.OpenTime.After(params.To) {
			break
		}
		if err := onCandle(c); err != nil {
			return err
		}
	}
	return nil
}

func (f *fakeCandles) SaveCandle(context.Context, models.Candle) error    { return nil }
func (f *fakeCandles) SaveCandles(context.Context, []models.Candle) error { return nil }

func (f *fakeCandles) FindGaps(context.Context, string, constants.MarketType, constants.Timeframe) ([]candle.Gap, error) {
	return nil, nil
}

func (f *fakeCandles) FetchCandles(context.Context, candle.FetchCandlesParams) ([]models.Candle, error) {
	return f.series, nil
}

// OpenCursor serves the same series, bounded the same way StreamCandles is.
func (f *fakeCandles) OpenCursor(params candle.FetchCandlesParams) candle.CandleCursor {
	var window []models.Candle
	for _, c := range f.series {
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

// sliceCursor walks a prepared slice, which is what a real cursor does once
// its paging is stripped away.
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

func (f *fakeCandles) FetchLatestCandle(context.Context, string, constants.MarketType, constants.Timeframe) (models.Candle, error) {
	if len(f.series) == 0 {
		return models.Candle{}, constants.ErrNotFound
	}
	return f.series[len(f.series)-1], nil
}

func (f *fakeCandles) FetchEarliestCandle(context.Context, string, constants.MarketType, constants.Timeframe) (models.Candle, error) {
	if len(f.series) == 0 {
		return models.Candle{}, constants.ErrNotFound
	}
	return f.series[0], nil
}

func (f *fakeCandles) CountCandles(context.Context, string, constants.MarketType, constants.Timeframe) (int64, error) {
	return int64(len(f.series)), nil
}

// fakeGaps reports a fixed set of unfilled gaps.
type fakeGaps struct {
	gaps []models.DataGap
	err  error
}

func (f *fakeGaps) ListUnfilledInRange(_ context.Context, params datagap.GapRangeParams) ([]models.DataGap, error) {
	if f.err != nil {
		return nil, f.err
	}

	var out []models.DataGap
	for _, gap := range f.gaps {
		if gap.GapStart.After(params.To) || !gap.GapEnd.After(params.From) {
			continue
		}
		out = append(out, gap)
	}
	return out, nil
}

func (f *fakeGaps) RecordGap(_ context.Context, gap models.DataGap) (models.DataGap, error) {
	return gap, nil
}
func (f *fakeGaps) MarkFilled(context.Context, int64) error { return nil }
func (f *fakeGaps) RecordFillAttempt(_ context.Context, id int64, _ string) (models.DataGap, error) {
	return models.DataGap{Id: id}, nil
}
func (f *fakeGaps) CountUnfilled(context.Context, string, constants.MarketType, constants.Timeframe) (int64, error) {
	return int64(len(f.gaps)), nil
}

// ---------------------------------------------------------------------------
// Series builders and shared values.
// ---------------------------------------------------------------------------

const (
	testSymbol = "BTCUSDT"
	// testFeePct and testTick are round numbers so every expected figure in
	// these tests can be worked out with a calculator and checked by eye.
	testFeePct = "0.05"
	testTick   = "0.01"
)

var (
	seriesStart   = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	initialEquity = decimal.NewFromInt(10000)
)

func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func testCosts() backtest.Costs {
	return backtest.Costs{
		FeeTakerPct:   decimal.RequireFromString(testFeePct),
		SlippageTicks: 1,
		TickSize:      decimal.RequireFromString(testTick),
	}
}

// zeroCosts is used only where a test is isolating a non-cost behaviour and
// the cost arithmetic would obscure it. It is never the default: a cost-free
// backtest is exactly the flattering fiction this engine exists to avoid.
func zeroCosts() backtest.Costs {
	return backtest.Costs{
		FeeTakerPct:   decimal.Zero,
		SlippageTicks: 0,
		TickSize:      decimal.RequireFromString(testTick),
	}
}

// bar builds one candle with explicit prices.
func bar(openTime time.Time, open, high, low, closePrice string) models.Candle {
	return models.Candle{
		Symbol:      testSymbol,
		MarketType:  constants.MarketTypeSpot,
		Timeframe:   constants.Timeframe1m,
		OpenTime:    openTime,
		CloseTime:   openTime.Add(time.Minute),
		Open:        decimal.RequireFromString(open),
		High:        decimal.RequireFromString(high),
		Low:         decimal.RequireFromString(low),
		Close:       decimal.RequireFromString(closePrice),
		Volume:      decimal.RequireFromString("10"),
		QuoteVolume: decimal.RequireFromString("100000"),
		TradeCount:  100,
		IsClosed:    true,
	}
}

// flatSeries is n bars all at exactly the same price.
//
// A flat market is the cleanest test surface there is: any change in equity
// over it came from the cost model, because nothing else could have moved.
func flatSeries(n int, price string) []models.Candle {
	series := make([]models.Candle, 0, n)
	for i := range n {
		series = append(series, bar(seriesStart.Add(time.Duration(i)*time.Minute), price, price, price, price))
	}
	return series
}

// risingSeries is n bars each one unit above the last, open == close, so the
// price at bar i is start+i and every figure stays hand-checkable.
func risingSeries(n int, start int64) []models.Candle {
	series := make([]models.Candle, 0, n)
	for i := range n {
		price := decimal.NewFromInt(start + int64(i)).String()
		series = append(series, bar(seriesStart.Add(time.Duration(i)*time.Minute), price, price, price, price))
	}
	return series
}

// runParams is the default run over a series, with real costs applied.
func runParams(series []models.Candle, strat strategy.Strategy) backtest.RunParams {
	last := series[len(series)-1].OpenTime
	return backtest.RunParams{
		Symbol:        testSymbol,
		MarketType:    constants.MarketTypeSpot,
		Timeframe:     constants.Timeframe1m,
		From:          seriesStart,
		To:            last,
		InitialEquity: initialEquity,
		Costs:         testCosts(),
		GapPolicy:     backtest.GapHalt,
		Strategy:      strat,
	}
}
