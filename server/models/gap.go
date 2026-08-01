package models

import (
	"time"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
)

// DataGap records a stretch of time where candles are known to be missing.
//
// Gaps exist because a WebSocket drop is normal, not exceptional. Recording
// them lets the backfill worker catch up and lets a backtest refuse to trust
// a period whose data was never complete.
type DataGap struct {
	Id         int64
	Symbol     string
	MarketType constants.MarketType
	Timeframe  constants.Timeframe

	// GapStart is the open time of the first missing candle and GapEnd the
	// open time of the first candle present again, both UTC.
	GapStart time.Time
	GapEnd   time.Time

	DetectedAt time.Time

	// FilledAt is nil until the gap has been backfilled successfully.
	FilledAt *time.Time

	Note string
}
