package candle

import (
	"context"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
)

// CandleUsecase holds the rules that apply to candles regardless of where
// they are stored.
//
// The one rule that exists in phase 01 is the important one: a candle that is
// still forming must never be persisted, because a flickering bar reaching
// the strategies would make live signals and backtests disagree.
type CandleUsecase interface {
	SaveCandle(ctx context.Context, candle models.Candle) error

	// SaveCandles persists many candles at once, applying the same
	// closed-candle rule as SaveCandle to every one of them. Backfill goes
	// through here rather than straight to the repository, so the rule cannot
	// be bypassed by taking the faster path.
	SaveCandles(ctx context.Context, candles []models.Candle) error

	// FindGaps reports the missing ranges in a stored series.
	FindGaps(ctx context.Context, symbol string, marketType constants.MarketType, timeframe constants.Timeframe) ([]Gap, error)
	FetchCandles(ctx context.Context, params FetchCandlesParams) ([]models.Candle, error)
	FetchLatestCandle(ctx context.Context, symbol string, marketType constants.MarketType, timeframe constants.Timeframe) (models.Candle, error)

	// FetchEarliestCandle returns the oldest stored candle, which bounds how
	// far a backfill has reached.
	FetchEarliestCandle(ctx context.Context, symbol string, marketType constants.MarketType, timeframe constants.Timeframe) (models.Candle, error)
	CountCandles(ctx context.Context, symbol string, marketType constants.MarketType, timeframe constants.Timeframe) (int64, error)
}
