// Package datagap declares the contracts for recording holes in the candle
// series.
//
// A gap is not an error condition to be swallowed: it is the record that lets
// backfill catch up and lets a backtest refuse to trust a period whose data
// was never complete.
package datagap

import (
	"context"

	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
)

// DataGapRepository stores detected gaps.
type DataGapRepository interface {
	InsertGap(ctx context.Context, gap models.DataGap) (models.DataGap, error)
}
