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

// Gap is a hole found in the stored sequence.
type Gap struct {
	// Start is the open time of the first missing candle, End the open time
	// of the first candle present again.
	Start time.Time
	End   time.Time
}

// CandleRepository stores and reads candles.
type CandleRepository interface {
	// UpsertCandle writes one closed candle, replacing any existing bar with
	// the same (symbol, market_type, timeframe, open_time).
	UpsertCandle(ctx context.Context, candle models.Candle) error

	// UpsertCandles writes many candles in batches. Re-running it over data
	// that is already stored changes nothing.
	UpsertCandles(ctx context.Context, candles []models.Candle) error

	// FindGaps returns the missing ranges in the stored sequence, computed in
	// the database rather than by differencing the series in memory.
	FindGaps(ctx context.Context, symbol string, marketType constants.MarketType, timeframe constants.Timeframe) ([]Gap, error)

	// FetchEarliestCandle returns the oldest stored candle, or
	// constants.ErrNotFound when the series is empty.
	FetchEarliestCandle(ctx context.Context, symbol string, marketType constants.MarketType, timeframe constants.Timeframe) (models.Candle, error)

	// FetchCandles returns the stored candles of a window, oldest first.
	FetchCandles(ctx context.Context, params FetchCandlesParams) ([]models.Candle, error)

	// FetchLatestCandle returns the newest stored candle for a series, or
	// constants.ErrNotFound when the series is still empty.
	FetchLatestCandle(ctx context.Context, symbol string, marketType constants.MarketType, timeframe constants.Timeframe) (models.Candle, error)

	// CountCandles returns how many candles are stored for a series.
	CountCandles(ctx context.Context, symbol string, marketType constants.MarketType, timeframe constants.Timeframe) (int64, error)
}
