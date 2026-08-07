package usecase

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/market"
)

// Backfill brings every configured timeframe up to date.
func (u *marketUsecase) Backfill(ctx context.Context) error {
	for _, timeframe := range u.cfg.Timeframes {
		if err := u.backfillTimeframe(ctx, timeframe); err != nil {
			return fmt.Errorf("backfill %s: %w", timeframe, err)
		}
	}
	return nil
}

// backfillTimeframe pages history forward until it reaches the present.
//
// The resume point is the latest stored open_time, which is what makes this
// restartable for free: a process killed 60% through a three year backfill
// picks up where it stopped, because the database already knows how far it
// got. No bespoke progress state is kept, and none should be — state that can
// disagree with the data is worse than no state.
func (u *marketUsecase) backfillTimeframe(ctx context.Context, timeframe constants.Timeframe) error {
	from, err := u.backfillStart(ctx, timeframe)
	if err != nil {
		return err
	}

	total := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		candles, err := u.marketData.FetchKlines(ctx, market.FetchKlinesParams{
			Symbol:     u.cfg.Symbol,
			MarketType: u.cfg.MarketType,
			Timeframe:  timeframe,
			From:       from,
			Limit:      constants.KlineLimit,
		})
		if err != nil {
			return err
		}
		if len(candles) == 0 {
			break
		}

		if err := u.candles.SaveCandles(ctx, candles); err != nil {
			return err
		}
		total += len(candles)

		// Advance past the last bar received. The +1ms matters: without it
		// the next page starts on the bar just stored and the loop never
		// terminates.
		last := candles[len(candles)-1].OpenTime
		next := last.Add(time.Millisecond)
		if !next.After(from) {
			// The exchange returned nothing newer than the cursor, so there
			// is no more history to walk.
			break
		}
		from = next

		// A short page means the exchange has nothing left to give.
		if len(candles) < constants.KlineLimit {
			break
		}
	}

	if total > 0 {
		u.log.InfoContext(ctx, "backfill complete",
			"timeframe", timeframe.String(), "candles", total)
	}
	return nil
}

// backfillStart decides where a timeframe resumes from.
func (u *marketUsecase) backfillStart(ctx context.Context, timeframe constants.Timeframe) (time.Time, error) {
	latest, err := u.candles.FetchLatestCandle(ctx, u.cfg.Symbol, u.cfg.MarketType, timeframe)
	switch {
	case errors.Is(err, constants.ErrNotFound):
		// Nothing stored: start from the configured beginning of history.
		return u.cfg.BackfillFrom, nil
	case err != nil:
		return time.Time{}, fmt.Errorf("find resume point: %w", err)
	}

	// Resume on the bar after the newest one stored.
	return latest.OpenTime.Add(time.Millisecond), nil
}

// backfillRange refetches one explicit window, used to fill a detected gap.
func (u *marketUsecase) backfillRange(ctx context.Context, timeframe constants.Timeframe, from, to time.Time) (int, error) {
	stored := 0
	cursor := from

	for cursor.Before(to) {
		if err := ctx.Err(); err != nil {
			return stored, err
		}

		candles, err := u.marketData.FetchKlines(ctx, market.FetchKlinesParams{
			Symbol:     u.cfg.Symbol,
			MarketType: u.cfg.MarketType,
			Timeframe:  timeframe,
			From:       cursor,
			To:         to,
			Limit:      constants.KlineLimit,
		})
		if err != nil {
			return stored, err
		}
		if len(candles) == 0 {
			break
		}

		if err := u.candles.SaveCandles(ctx, candles); err != nil {
			return stored, err
		}
		stored += len(candles)

		next := candles[len(candles)-1].OpenTime.Add(time.Millisecond)
		if !next.After(cursor) {
			break
		}
		cursor = next
	}
	return stored, nil
}
