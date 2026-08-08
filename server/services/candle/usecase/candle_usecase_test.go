package usecase_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/candle"
	_candle_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/candle/usecase"
)

// spyCandleRepository records what the usecase asked it to do.
//
// series and pageRequests exist for the keyset scan: they let a paging bug be
// caught without a database, which matters because a cursor that skipped or
// repeated a bar would corrupt every backtest run over that series.
type spyCandleRepository struct {
	upserted []models.Candle

	series       []models.Candle
	pageRequests []candle.FetchCandlePageParams
	pageErr      error
}

func (s *spyCandleRepository) UpsertCandle(_ context.Context, c models.Candle) error {
	s.upserted = append(s.upserted, c)
	return nil
}

func (s *spyCandleRepository) FetchCandles(context.Context, candle.FetchCandlesParams) ([]models.Candle, error) {
	return nil, nil
}

// FetchCandlePage serves series the way the SQL does: After exclusive, To
// inclusive, capped at PageSize, oldest first.
func (s *spyCandleRepository) FetchCandlePage(_ context.Context, params candle.FetchCandlePageParams) ([]models.Candle, error) {
	s.pageRequests = append(s.pageRequests, params)
	if s.pageErr != nil {
		return nil, s.pageErr
	}

	page := make([]models.Candle, 0, params.PageSize)
	for _, c := range s.series {
		if !c.OpenTime.After(params.After) {
			continue
		}
		if !params.To.IsZero() && c.OpenTime.After(params.To) {
			break
		}
		page = append(page, c)
		if params.PageSize > 0 && len(page) == params.PageSize {
			break
		}
	}
	return page, nil
}

func (s *spyCandleRepository) FetchLatestCandle(context.Context, string, constants.MarketType, constants.Timeframe) (models.Candle, error) {
	return models.Candle{}, constants.ErrNotFound
}

func (s *spyCandleRepository) CountCandles(context.Context, string, constants.MarketType, constants.Timeframe) (int64, error) {
	return 0, nil
}

func (s *spyCandleRepository) UpsertCandles(_ context.Context, candles []models.Candle) error {
	s.upserted = append(s.upserted, candles...)
	return nil
}

func (s *spyCandleRepository) FindGaps(context.Context, string, constants.MarketType, constants.Timeframe) ([]candle.Gap, error) {
	return nil, nil
}

func (s *spyCandleRepository) FetchEarliestCandle(context.Context, string, constants.MarketType, constants.Timeframe) (models.Candle, error) {
	return models.Candle{}, constants.ErrNotFound
}

func testCandle() models.Candle {
	open := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	return models.Candle{
		Symbol:      "BTCUSDT",
		MarketType:  constants.MarketTypeSpot,
		Timeframe:   constants.Timeframe1m,
		OpenTime:    open,
		CloseTime:   open.Add(time.Minute),
		Open:        decimal.RequireFromString("64000.10000000"),
		High:        decimal.RequireFromString("64100.55000000"),
		Low:         decimal.RequireFromString("63950.00000000"),
		Close:       decimal.RequireFromString("64080.25000000"),
		Volume:      decimal.RequireFromString("12.34567890"),
		QuoteVolume: decimal.RequireFromString("790123.45678900"),
		TradeCount:  431,
		IsClosed:    true,
	}
}

// TestSaveCandleRejectsUnclosedCandle is the rule that keeps a flickering bar
// out of everything downstream: an unclosed candle must never be stored.
func TestSaveCandleRejectsUnclosedCandle(t *testing.T) {
	repo := &spyCandleRepository{}
	us := _candle_us.NewCandleUsecaseImpl(repo)

	c := testCandle()
	c.IsClosed = false

	err := us.SaveCandle(context.Background(), c)
	if !errors.Is(err, constants.ErrUnclosedCandle) {
		t.Fatalf("SaveCandle() returned %v, want ErrUnclosedCandle", err)
	}
	if len(repo.upserted) != 0 {
		t.Errorf("repository was called %d times, want 0", len(repo.upserted))
	}
}

func TestSaveCandleStoresClosedCandle(t *testing.T) {
	repo := &spyCandleRepository{}
	us := _candle_us.NewCandleUsecaseImpl(repo)

	if err := us.SaveCandle(context.Background(), testCandle()); err != nil {
		t.Fatalf("SaveCandle() returned error: %v", err)
	}
	if len(repo.upserted) != 1 {
		t.Fatalf("repository was called %d times, want 1", len(repo.upserted))
	}
	if !repo.upserted[0].IsClosed {
		t.Error("an unclosed candle reached the repository")
	}
}

// TestSaveCandlesRejectsBatchWithAnyUnclosedCandle covers the batch path used
// by backfill. A batch is either wholly trustworthy or wholly rejected: one
// forming bar buried in a thousand good ones must not slip through because
// the faster route skipped the check.
func TestSaveCandlesRejectsBatchWithAnyUnclosedCandle(t *testing.T) {
	repo := &spyCandleRepository{}
	us := _candle_us.NewCandleUsecaseImpl(repo)

	batch := make([]models.Candle, 0, 100)
	for i := range 100 {
		c := testCandle()
		c.OpenTime = c.OpenTime.Add(time.Duration(i) * time.Minute)
		c.CloseTime = c.OpenTime.Add(time.Minute)
		batch = append(batch, c)
	}
	// One forming bar, in the middle where a naive check would miss it.
	batch[57].IsClosed = false

	err := us.SaveCandles(context.Background(), batch)
	if !errors.Is(err, constants.ErrUnclosedCandle) {
		t.Fatalf("SaveCandles() returned %v, want ErrUnclosedCandle", err)
	}
	if len(repo.upserted) != 0 {
		t.Errorf("%d candles were written despite the batch being rejected", len(repo.upserted))
	}
}

func TestSaveCandlesStoresWholeBatch(t *testing.T) {
	repo := &spyCandleRepository{}
	us := _candle_us.NewCandleUsecaseImpl(repo)

	batch := []models.Candle{testCandle(), testCandle(), testCandle()}
	if err := us.SaveCandles(context.Background(), batch); err != nil {
		t.Fatalf("SaveCandles() returned error: %v", err)
	}
	if len(repo.upserted) != len(batch) {
		t.Errorf("wrote %d candles, want %d", len(repo.upserted), len(batch))
	}
}

func TestSaveCandlesWithEmptyBatchIsANoop(t *testing.T) {
	repo := &spyCandleRepository{}
	us := _candle_us.NewCandleUsecaseImpl(repo)

	if err := us.SaveCandles(context.Background(), nil); err != nil {
		t.Fatalf("SaveCandles(nil) returned error: %v", err)
	}
	if len(repo.upserted) != 0 {
		t.Errorf("an empty batch wrote %d candles", len(repo.upserted))
	}
}

// makeSeries builds n consecutive 1m candles from start.
func makeSeries(start time.Time, n int) []models.Candle {
	series := make([]models.Candle, 0, n)
	for i := range n {
		c := testCandle()
		c.OpenTime = start.Add(time.Duration(i) * time.Minute)
		c.CloseTime = c.OpenTime.Add(time.Minute)
		series = append(series, c)
	}
	return series
}

// TestStreamCandlesDeliversEveryBarExactlyOnce is the property the backtest
// engine depends on completely.
//
// The series is deliberately not a multiple of the page size, so the last
// partial page is exercised. A cursor off by one bar in either direction —
// skipping the first candle of each page, or repeating the last — would still
// produce a plausible-looking run and a wrong result, so this asserts the
// exact sequence rather than only the count.
func TestStreamCandlesDeliversEveryBarExactlyOnce(t *testing.T) {
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	const bars = 2503

	repo := &spyCandleRepository{series: makeSeries(start, bars)}
	us := _candle_us.NewCandleUsecaseImpl(repo)

	var seen []time.Time
	err := us.StreamCandles(context.Background(), candle.FetchCandlesParams{
		Symbol:     "BTCUSDT",
		MarketType: constants.MarketTypeSpot,
		Timeframe:  constants.Timeframe1m,
		From:       start,
		To:         start.Add(time.Duration(bars) * time.Minute),
	}, func(c models.Candle) error {
		seen = append(seen, c.OpenTime)
		return nil
	})
	if err != nil {
		t.Fatalf("StreamCandles() returned error: %v", err)
	}

	if len(seen) != bars {
		t.Fatalf("streamed %d candles, want %d", len(seen), bars)
	}
	for i, openTime := range seen {
		want := start.Add(time.Duration(i) * time.Minute)
		if !openTime.Equal(want) {
			t.Fatalf("candle %d has open time %s, want %s", i, openTime, want)
		}
	}

	// More than one page must actually have been read, or the test would pass
	// on an implementation that never pages at all.
	if len(repo.pageRequests) < 2 {
		t.Errorf("made %d page requests, want several: the scan did not page", len(repo.pageRequests))
	}
}

// TestStreamCandlesIncludesTheFirstBar guards the inclusive/exclusive seam.
// From is inclusive on the interface but the underlying cursor is exclusive,
// so an off-by-one here would silently drop the opening bar of every run.
func TestStreamCandlesIncludesTheFirstBar(t *testing.T) {
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)

	repo := &spyCandleRepository{series: makeSeries(start, 3)}
	us := _candle_us.NewCandleUsecaseImpl(repo)

	var first time.Time
	count := 0
	err := us.StreamCandles(context.Background(), candle.FetchCandlesParams{
		From: start,
		To:   start.Add(2 * time.Minute),
	}, func(c models.Candle) error {
		if count == 0 {
			first = c.OpenTime
		}
		count++
		return nil
	})
	if err != nil {
		t.Fatalf("StreamCandles() returned error: %v", err)
	}
	if count != 3 {
		t.Fatalf("streamed %d candles, want 3", count)
	}
	if !first.Equal(start) {
		t.Errorf("first candle is %s, want the bar at From (%s)", first, start)
	}
}

// TestStreamCandlesStopsOnCallbackError checks that a caller ending its own
// scan gets its error back unwrapped, so errors.Is still matches. The engine
// uses this to stop at a gap boundary without it looking like a read failure.
func TestStreamCandlesStopsOnCallbackError(t *testing.T) {
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	stop := errors.New("caller is done")

	repo := &spyCandleRepository{series: makeSeries(start, 500)}
	us := _candle_us.NewCandleUsecaseImpl(repo)

	seen := 0
	err := us.StreamCandles(context.Background(), candle.FetchCandlesParams{From: start, To: start.Add(500 * time.Minute)},
		func(models.Candle) error {
			seen++
			if seen == 4 {
				return stop
			}
			return nil
		})

	if !errors.Is(err, stop) {
		t.Fatalf("StreamCandles() returned %v, want the callback's own error", err)
	}
	if seen != 4 {
		t.Errorf("callback ran %d times, want 4: the scan did not stop", seen)
	}
}
