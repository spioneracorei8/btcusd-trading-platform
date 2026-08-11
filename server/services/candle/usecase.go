package candle

import (
	"context"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
)

// CandleCursor reads a stored series forward, one candle at a time.
//
// It holds no database resource between calls — it pages through the
// repository — so there is nothing to close and no lifetime to manage.
type CandleCursor interface {
	// Next returns the following candle. ok is false once the window is
	// exhausted, which is the end of the series and not an error.
	//
	// A cursor only ever moves forward. There is deliberately no rewind: the
	// engine replays history in one direction, and a cursor that could go back
	// would let a caller re-read a bar it had already acted on.
	Next(ctx context.Context) (candle models.Candle, ok bool, err error)
}

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

	// OpenCursor returns a forward-only reader over a window, paging
	// underneath exactly as StreamCandles does.
	//
	// StreamCandles pushes; this pulls. The multi-timeframe trend filter needs
	// several series advanced in lockstep against one another, which a
	// callback cannot express: with push, each series drives its own loop and
	// nothing can hold one back to wait for another.
	OpenCursor(params FetchCandlesParams) CandleCursor

	// StreamCandles calls onCandle for every stored candle in a window,
	// oldest first, reading the series in pages so it is never all resident.
	//
	// This is how the backtest engine replays history: a callback rather than
	// a returned slice because the point is that the slice must not exist.
	// Returning an error from onCandle stops the scan and is returned as-is,
	// so a caller can end a run early without it looking like a read failure.
	StreamCandles(ctx context.Context, params FetchCandlesParams, onCandle func(models.Candle) error) error
	FetchLatestCandle(ctx context.Context, symbol string, marketType constants.MarketType, timeframe constants.Timeframe) (models.Candle, error)

	// FetchEarliestCandle returns the oldest stored candle, which bounds how
	// far a backfill has reached.
	FetchEarliestCandle(ctx context.Context, symbol string, marketType constants.MarketType, timeframe constants.Timeframe) (models.Candle, error)
	CountCandles(ctx context.Context, symbol string, marketType constants.MarketType, timeframe constants.Timeframe) (int64, error)
}
