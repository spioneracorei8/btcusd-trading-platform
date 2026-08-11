package usecase

import (
	"context"
	"time"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/candle"
)

// candleCursor pages a window and hands out one candle at a time.
//
// It shares StreamCandles' keyset paging rather than duplicating it: the same
// exclusive-After, inclusive-To convention, the same page size, so a bug in
// one would show up in the other rather than in only half the system.
type candleCursor struct {
	repository candle.CandleRepository
	page       candle.FetchCandlePageParams

	buffer []models.Candle
	next   int

	// drained records that the last page came back short, so the cursor stops
	// asking. Without it an exhausted window would issue one pointless query
	// per remaining call.
	drained bool
}

// OpenCursor returns a forward-only reader over a window.
func (u *candleUsecase) OpenCursor(params candle.FetchCandlesParams) candle.CandleCursor {
	return &candleCursor{
		repository: u.candleRepository,
		page: candle.FetchCandlePageParams{
			Symbol:     params.Symbol,
			MarketType: params.MarketType,
			Timeframe:  params.Timeframe,
			// From is inclusive on the interface and After exclusive
			// underneath, matching StreamCandles.
			After:    params.From.Add(-time.Nanosecond),
			To:       params.To,
			PageSize: constants.CandlePageSize,
		},
	}
}

// Next returns the following candle in the window.
func (c *candleCursor) Next(ctx context.Context) (models.Candle, bool, error) {
	if c.next < len(c.buffer) {
		result := c.buffer[c.next]
		c.next++
		return result, true, nil
	}
	if c.drained {
		return models.Candle{}, false, nil
	}

	if err := ctx.Err(); err != nil {
		return models.Candle{}, false, err
	}

	page, err := c.repository.FetchCandlePage(ctx, c.page)
	if err != nil {
		return models.Candle{}, false, err // the repository names the operation
	}
	if len(page) == 0 {
		c.drained = true
		return models.Candle{}, false, nil
	}

	// A short page means the window is exhausted; the next call can answer
	// from the buffer and then stop without another round trip.
	if len(page) < c.page.PageSize {
		c.drained = true
	}

	c.buffer = page
	c.next = 1
	c.page.After = page[len(page)-1].OpenTime
	return page[0], true, nil
}
