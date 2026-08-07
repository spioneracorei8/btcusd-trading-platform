package market

import (
	"context"
	"time"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
)

// MarketUsecase runs the ingestion pipeline and reports on it.
type MarketUsecase interface {
	// Run ingests market data until ctx is cancelled. It backfills, then
	// streams, reconnecting with backoff for as long as the context is live.
	Run(ctx context.Context) error

	// Backfill brings every configured timeframe up to date from the last
	// stored candle, or from the configured start when nothing is stored.
	Backfill(ctx context.Context) error

	// LatestOpenCandle returns the still-forming bar held in memory for a
	// timeframe. It is display-only and never reaches storage.
	LatestOpenCandle(timeframe constants.Timeframe) (models.Candle, bool)

	// Status assembles what /internal/market/status reports.
	Status(ctx context.Context, now time.Time) (models.MarketStatus, error)
}
