// Package candle declares the candle service contracts. The interfaces live
// here and their implementations in the usecase/ and repository/
// subpackages, so a usecase depends on the repository interface and never on
// the SQL behind it.
package candle

import (
	"context"
	"time"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
)

// FetchCandlesParams selects a closed window of candles.
type FetchCandlesParams struct {
	Symbol     string
	MarketType constants.MarketType
	Timeframe  constants.Timeframe
	// From and To bound open_time inclusively.
	From time.Time
	To   time.Time
}

// CandleRepository stores and reads candles.
type CandleRepository interface {
	// UpsertCandle writes one closed candle, replacing any existing bar with
	// the same (symbol, market_type, timeframe, open_time).
	UpsertCandle(ctx context.Context, candle models.Candle) error

	// FetchCandles returns the stored candles of a window, oldest first.
	FetchCandles(ctx context.Context, params FetchCandlesParams) ([]models.Candle, error)

	// FetchLatestCandle returns the newest stored candle for a series, or
	// constants.ErrNotFound when the series is still empty.
	FetchLatestCandle(ctx context.Context, symbol string, marketType constants.MarketType, timeframe constants.Timeframe) (models.Candle, error)

	// CountCandles returns how many candles are stored for a series.
	CountCandles(ctx context.Context, symbol string, marketType constants.MarketType, timeframe constants.Timeframe) (int64, error)
}
