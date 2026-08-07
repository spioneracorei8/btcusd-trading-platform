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
type spyCandleRepository struct {
	upserted []models.Candle
}

func (s *spyCandleRepository) UpsertCandle(_ context.Context, c models.Candle) error {
	s.upserted = append(s.upserted, c)
	return nil
}

func (s *spyCandleRepository) FetchCandles(context.Context, candle.FetchCandlesParams) ([]models.Candle, error) {
	return nil, nil
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
