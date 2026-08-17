package usecase

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/market"
)

// Backfill brings every configured timeframe up to date, and back to the
// configured beginning of history.
//
// Both directions, because only one of them is implied by "up to date". The
// forward walk resumes from the newest stored bar, which is what makes it
// restartable; on its own it can never reach history older than whatever was
// collected first. Moving MARKET_BACKFILL_FROM earlier then changes nothing at
// all, silently — the deployment that prompted this had .env asking for
// 2022-07-01 and a series that still began 2023-01-01, with nothing anywhere
// reporting a problem.
//
// The older history goes first. A process interrupted part-way through leaves
// the series shorter but still contiguous at its recent end, which is the half
// the live feed depends on.
func (u *marketUsecase) Backfill(ctx context.Context) error {
	for _, timeframe := range u.cfg.Timeframes {
		if err := u.backfillHistory(ctx, timeframe); err != nil {
			return fmt.Errorf("backfill %s history: %w", timeframe, err)
		}
		if err := u.backfillTimeframe(ctx, timeframe); err != nil {
			return fmt.Errorf("backfill %s: %w", timeframe, err)
		}
	}
	return nil
}

// backfillHistory fills the stretch between the configured start of history and
// the oldest bar actually stored.
//
// # Why this is derived from the data rather than remembered
//
// It asks the series where it begins and compares that with the configuration.
// There is no progress marker to fall out of step, which is the same reasoning
// the forward walk uses: a partially completed backward fill simply has an
// earlier earliest bar next time, and resumes from there.
//
// It costs one indexed lookup per timeframe when there is nothing to do, and
// nothing at all beyond that — no request is made unless the gap is real.
func (u *marketUsecase) backfillHistory(ctx context.Context, timeframe constants.Timeframe) error {
	earliest, err := u.candles.FetchEarliestCandle(ctx, u.cfg.Symbol, u.cfg.MarketType, timeframe)
	switch {
	case errors.Is(err, constants.ErrNotFound):
		// An empty series has no prefix to fill: the forward walk starts at
		// BackfillFrom and covers everything.
		return nil
	case err != nil:
		return fmt.Errorf("find the start of the series: %w", err)
	}

	if !earliest.OpenTime.After(u.cfg.BackfillFrom) {
		return nil
	}

	u.log.InfoContext(ctx, "filling history older than the stored series",
		"timeframe", timeframe.String(),
		"from", u.cfg.BackfillFrom.Format(time.RFC3339),
		"to", earliest.OpenTime.Format(time.RFC3339))

	stored, err := u.backfillRange(ctx, timeframe, u.cfg.BackfillFrom, earliest.OpenTime)
	if err != nil {
		return err
	}

	// Progress is whether the series now starts earlier, not whether rows were
	// written.
	//
	// Those differ, and the difference is a loop that never ends. When the
	// exchange's oldest bar is the one already held, the window returns that
	// same bar, the upsert rewrites it, and a count of stored rows reports
	// success — while the series begins exactly where it did. Backfill runs
	// before every reconnect, so that would re-request the same page for the
	// life of the deployment and log a completion each time.
	after, err := u.candles.FetchEarliestCandle(ctx, u.cfg.Symbol, u.cfg.MarketType, timeframe)
	if err != nil {
		return fmt.Errorf("confirm the start of the series: %w", err)
	}

	if !after.OpenTime.Before(earliest.OpenTime) {
		// The exchange has nothing that far back. Worth saying, because the
		// consequence is silent otherwise: every warm-up budget computed from
		// MARKET_BACKFILL_FROM is then wrong, and a filter or strategy that
		// depends on this timeframe will never become ready.
		//
		// Reported once per process. It is re-checked on every reconnect, so
		// repeating it each time would bury the reconnect it came with.
		u.warnOnce(ctx, timeframe, after.OpenTime)
		return nil
	}

	u.log.InfoContext(ctx, "history fill complete",
		"timeframe", timeframe.String(),
		"candles", stored,
		"series_now_starts", after.OpenTime.Format(time.RFC3339))
	return nil
}

// warnOnce reports an unreachable start of history the first time it is seen,
// and at debug level afterwards.
//
// The deduplication is over log lines only. Nothing about what gets fetched
// depends on it, so a wrong entry can cost at most a quieter message.
func (u *marketUsecase) warnOnce(ctx context.Context, timeframe constants.Timeframe, earliest time.Time) {
	seen := u.historyWarned.mark(timeframe)

	message := "the exchange has no candles as far back as MARKET_BACKFILL_FROM"
	fields := []any{
		"timeframe", timeframe.String(),
		"backfill_from", u.cfg.BackfillFrom.Format(time.RFC3339),
		"earliest_available", earliest.Format(time.RFC3339),
	}

	if seen {
		u.log.DebugContext(ctx, message, fields...)
		return
	}
	u.log.WarnContext(ctx, message, fields...)
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
