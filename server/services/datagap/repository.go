// Package datagap declares the contracts for recording holes in the candle
// series.
//
// A gap is not an error condition to be swallowed: it is the record that lets
// backfill catch up and lets a backtest refuse to trust a period whose data
// was never complete.
package datagap

import (
	"context"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
)

// DataGapRepository stores detected gaps.
type DataGapRepository interface {
	// InsertGap records a detected range. Detection runs on a ticker and
	// re-finds an unfilled gap every pass, so this is an upsert: repeated
	// scans must not grow duplicate rows.
	InsertGap(ctx context.Context, gap models.DataGap) (models.DataGap, error)

	// MarkFilled records that a range was backfilled successfully.
	MarkFilled(ctx context.Context, id int64) error

	// RecordFillAttempt counts one failed attempt and stores why, returning
	// the updated row so the caller can see whether the budget is spent.
	RecordFillAttempt(ctx context.Context, id int64, note string) (models.DataGap, error)

	// ListUnfilled returns gaps still worth retrying, oldest first.
	ListUnfilled(ctx context.Context, symbol string, marketType constants.MarketType, timeframe constants.Timeframe, maxAttempts int32) ([]models.DataGap, error)

	// CountUnfilled counts every unfilled gap, including those whose retries
	// are exhausted: the status endpoint reports what is missing, not what is
	// still being chased.
	CountUnfilled(ctx context.Context, symbol string, marketType constants.MarketType, timeframe constants.Timeframe) (int64, error)
}
