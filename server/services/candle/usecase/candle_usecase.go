package usecase

import (
	"context"
	"fmt"
	"time"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/candle"
)

type candleUsecase struct {
	candleRepository candle.CandleRepository
}

// NewCandleUsecaseImpl builds the candle usecase on top of a repository.
func NewCandleUsecaseImpl(candleRepository candle.CandleRepository) candle.CandleUsecase {
	return &candleUsecase{
		candleRepository: candleRepository,
	}
}

// SaveCandle persists a closed candle.
//
// An unclosed bar is rejected here rather than in the repository: it is a
// rule about what the system is allowed to reason over, not a detail of how
// rows are written.
func (u *candleUsecase) SaveCandle(ctx context.Context, c models.Candle) error {
	if !c.IsClosed {
		return fmt.Errorf("%w: %s %s %s at %s",
			constants.ErrUnclosedCandle, c.Symbol, c.MarketType, c.Timeframe, c.OpenTime.UTC().Format("2006-01-02T15:04:05Z"))
	}
	return u.candleRepository.UpsertCandle(ctx, c)
}

func (u *candleUsecase) FetchCandles(ctx context.Context, params candle.FetchCandlesParams) ([]models.Candle, error) {
	return u.candleRepository.FetchCandles(ctx, params)
}

// StreamCandles replays a window one candle at a time, paging underneath.
//
// The loop is deliberately dull. The keyset cursor is the previous page's
// last open_time, which the repository treats as exclusive, so a page that
// comes back short or empty ends the scan without needing a count.
func (u *candleUsecase) StreamCandles(ctx context.Context, params candle.FetchCandlesParams, onCandle func(models.Candle) error) error {
	page := candle.FetchCandlePageParams{
		Symbol:     params.Symbol,
		MarketType: params.MarketType,
		Timeframe:  params.Timeframe,
		To:         params.To,
		PageSize:   constants.CandlePageSize,
	}

	// From is inclusive on the interface but After is exclusive underneath, so
	// the cursor starts one instant earlier to keep the first candle.
	page.After = params.From.Add(-time.Nanosecond)

	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("stream candles: %w", err)
		}

		candles, err := u.candleRepository.FetchCandlePage(ctx, page)
		if err != nil {
			return err // the repository already names the operation
		}
		if len(candles) == 0 {
			return nil
		}

		for _, c := range candles {
			// Returned unwrapped: a caller stopping its own scan is not a
			// read failure and must stay comparable with errors.Is.
			if err := onCandle(c); err != nil {
				return err
			}
		}
		page.After = candles[len(candles)-1].OpenTime
	}
}

func (u *candleUsecase) FetchLatestCandle(ctx context.Context, symbol string, marketType constants.MarketType, timeframe constants.Timeframe) (models.Candle, error) {
	return u.candleRepository.FetchLatestCandle(ctx, symbol, marketType, timeframe)
}

func (u *candleUsecase) CountCandles(ctx context.Context, symbol string, marketType constants.MarketType, timeframe constants.Timeframe) (int64, error) {
	return u.candleRepository.CountCandles(ctx, symbol, marketType, timeframe)
}

// SaveCandles persists many candles, rejecting the whole batch if any one of
// them is still forming.
//
// Backfill takes this path rather than calling the repository directly, so
// the closed-candle rule cannot be bypassed by choosing the faster route. The
// check runs before the first write: a batch is either wholly trustworthy or
// wholly rejected, never half applied.
func (u *candleUsecase) SaveCandles(ctx context.Context, candles []models.Candle) error {
	if len(candles) == 0 {
		return nil
	}

	for _, c := range candles {
		if !c.IsClosed {
			return fmt.Errorf("%w: %s %s %s at %s",
				constants.ErrUnclosedCandle, c.Symbol, c.MarketType, c.Timeframe,
				c.OpenTime.UTC().Format("2006-01-02T15:04:05Z"))
		}
	}
	return u.candleRepository.UpsertCandles(ctx, candles)
}

func (u *candleUsecase) FindGaps(ctx context.Context, symbol string, marketType constants.MarketType, timeframe constants.Timeframe) ([]candle.Gap, error) {
	return u.candleRepository.FindGaps(ctx, symbol, marketType, timeframe)
}

func (u *candleUsecase) FetchEarliestCandle(ctx context.Context, symbol string, marketType constants.MarketType, timeframe constants.Timeframe) (models.Candle, error) {
	return u.candleRepository.FetchEarliestCandle(ctx, symbol, marketType, timeframe)
}
