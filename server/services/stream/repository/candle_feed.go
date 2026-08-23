package repository

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/market"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/stream"
)

// candleFeed watches the exchange for display purposes only.
//
// # Why the api opens a second connection
//
// The forming bar exists only in the collector's memory, and the collector is
// a different process. Storing it is forbidden (CLAUDE.md §3.1) and there is
// no shared memory, so watching the same public feed is the only way to show
// a live price at all.
//
// # What makes it safe
//
// This type holds a market data client and nothing else. It has no candle
// repository, no usecase and no database handle, so there is nothing here that
// could persist a forming bar even by mistake — the guarantee is the absence
// of the capability rather than a rule somebody has to follow.
type candleFeed struct {
	data market.MarketDataRepository
	log  *slog.Logger
}

// NewCandleFeedImpl builds the api's read-only market feed.
func NewCandleFeedImpl(data market.MarketDataRepository, log *slog.Logger) stream.CandleSource {
	return &candleFeed{data: data, log: log}
}

// Watch forwards every kline, closed or forming, until ctx is cancelled.
//
// It reconnects on its own, because a display feed dropping silently would
// leave a chart frozen on a stale price with nothing to say so. The backoff is
// deliberately simple: this is not the ingestion path, and a gap here costs a
// redraw rather than a hole in the series.
func (f *candleFeed) Watch(
	ctx context.Context,
	symbol string, marketType constants.MarketType, timeframes []constants.Timeframe,
	onCandle func(models.Candle),
) error {
	backoff := time.Second

	for ctx.Err() == nil {
		err := f.data.StreamKlines(ctx, market.StreamParams{
			Symbol: symbol, MarketType: marketType, Timeframes: timeframes,
		}, func(kline market.StreamedKline) {
			onCandle(kline.Candle)
		})

		if ctx.Err() != nil {
			return nil
		}
		if err != nil && !errors.Is(err, constants.ErrStreamClosed) {
			f.log.WarnContext(ctx, "the display feed dropped; reconnecting",
				"error", err, "in", backoff.String())
		}

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}

		if backoff *= 2; backoff > constants.StreamFeedMaxBackoff {
			backoff = constants.StreamFeedMaxBackoff
		}
	}
	return nil
}
