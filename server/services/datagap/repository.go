// Package datagap declares the contracts for recording holes in the candle
// series.
//
// A gap is not an error condition to be swallowed: it is the record that lets
// backfill catch up and lets a backtest refuse to trust a period whose data
// was never complete.
package datagap

import (
	"context"
	"time"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
)

// GapRangeParams selects the unfilled gaps overlapping a window.
type GapRangeParams struct {
	Symbol     string
	MarketType constants.MarketType
	Timeframe  constants.Timeframe

	// From and To bound the window being asked about. A gap counts when it
	// overlaps that window at all, not only when it falls wholly inside:
	// a gap straddling either edge still leaves the range incomplete.
	From time.Time
	To   time.Time
}

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

	// ListUnfilledInRange returns every unfilled gap overlapping a window,
	// oldest first, regardless of how many fill attempts it has used.
	//
	// The attempt count is deliberately not a filter here. ListUnfilled hides
	// exhausted gaps because it feeds the backfill worker, which has nothing
	// left to try. A backtest needs the opposite: a gap nobody can fill is
	// the strongest reason to refuse to report a number over that period.
	ListUnfilledInRange(ctx context.Context, params GapRangeParams) ([]models.DataGap, error)

	// CountUnfilled counts every unfilled gap, including those whose retries
	// are exhausted: the status endpoint reports what is missing, not what is
	// still being chased.
	CountUnfilled(ctx context.Context, symbol string, marketType constants.MarketType, timeframe constants.Timeframe) (int64, error)
}
