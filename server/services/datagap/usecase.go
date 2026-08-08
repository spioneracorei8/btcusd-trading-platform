package datagap

import (
	"context"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
)

// DataGapUsecase holds the rules around recording and chasing a gap.
type DataGapUsecase interface {
	// RecordGap notes a detected range, or returns the existing row when the
	// scan has already seen it.
	RecordGap(ctx context.Context, gap models.DataGap) (models.DataGap, error)

	// MarkFilled records that a range was successfully backfilled.
	MarkFilled(ctx context.Context, id int64) error

	// RecordFillAttempt counts a failed backfill and stores why.
	RecordFillAttempt(ctx context.Context, id int64, note string) (models.DataGap, error)

	// CountUnfilled reports how much data is still missing for a timeframe.
	CountUnfilled(ctx context.Context, symbol string, marketType constants.MarketType, timeframe constants.Timeframe) (int64, error)

	// ListUnfilledInRange returns every unfilled gap overlapping a window,
	// including those whose retries are exhausted. It is what a backtest
	// consults before agreeing to report a number over that period.
	ListUnfilledInRange(ctx context.Context, params GapRangeParams) ([]models.DataGap, error)
}
