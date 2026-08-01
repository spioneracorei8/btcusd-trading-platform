package usecase

import (
	"context"
	"fmt"

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

func (u *candleUsecase) FetchLatestCandle(ctx context.Context, symbol string, marketType constants.MarketType, timeframe constants.Timeframe) (models.Candle, error) {
	return u.candleRepository.FetchLatestCandle(ctx, symbol, marketType, timeframe)
}

func (u *candleUsecase) CountCandles(ctx context.Context, symbol string, marketType constants.MarketType, timeframe constants.Timeframe) (int64, error) {
	return u.candleRepository.CountCandles(ctx, symbol, marketType, timeframe)
}
