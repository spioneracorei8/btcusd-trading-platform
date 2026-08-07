package usecase

import (
	"context"
	"fmt"
	"time"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/candle"
)

// gapcheckLoop scans for holes on a ticker.
//
// An unfilled gap is never a crash condition: the collector keeps running and
// keeps recording. Phase 04's backtest engine is what refuses to produce
// results over a range containing one, which is why the record has to be
// faithful rather than merely alarming.
func (u *marketUsecase) gapcheckLoop(ctx context.Context) error {
	ticker := time.NewTicker(u.cfg.GapcheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := u.RunGapcheck(ctx); err != nil {
				if ctx.Err() != nil {
					return nil
				}
				u.log.WarnContext(ctx, "gap scan failed", "error", err)
			}
		}
	}
}

// RunGapcheck scans every timeframe and tries to fill what it finds.
func (u *marketUsecase) RunGapcheck(ctx context.Context) error {
	for _, timeframe := range u.cfg.Timeframes {
		if err := u.gapcheckTimeframe(ctx, timeframe); err != nil {
			return fmt.Errorf("gap scan %s: %w", timeframe, err)
		}
	}
	return nil
}

func (u *marketUsecase) gapcheckTimeframe(ctx context.Context, timeframe constants.Timeframe) error {
	found, err := u.candles.FindGaps(ctx, u.cfg.Symbol, u.cfg.MarketType, timeframe)
	if err != nil {
		return err
	}

	for _, gap := range found {
		if err := ctx.Err(); err != nil {
			return err
		}

		stored, err := u.gaps.RecordGap(ctx, models.DataGap{
			Symbol:     u.cfg.Symbol,
			MarketType: u.cfg.MarketType,
			Timeframe:  timeframe,
			GapStart:   gap.Start,
			GapEnd:     gap.End,
			Note:       "detected by scan",
		})
		if err != nil {
			return err
		}
		if stored.FilledAt != nil || stored.FillAttempts >= constants.MaxGapFillAttempts {
			continue
		}

		u.log.WarnContext(ctx, "candle gap detected",
			"timeframe", timeframe.String(),
			"from", gap.Start.Format(time.RFC3339),
			"to", gap.End.Format(time.RFC3339),
			"attempts", stored.FillAttempts,
		)
		u.attemptFill(ctx, stored, timeframe, gap.Start, gap.End)
	}
	return nil
}

// attemptFill refetches a missing range and records the outcome.
func (u *marketUsecase) attemptFill(ctx context.Context, gap models.DataGap, timeframe constants.Timeframe, from, to time.Time) {
	fetched, err := u.backfillRange(ctx, timeframe, from, to)
	if ctx.Err() != nil {
		return
	}

	if err != nil {
		u.recordFailedFill(ctx, gap, fmt.Sprintf("refetch failed: %v", err))
		return
	}

	// Only claim the gap is filled if the range is genuinely whole again.
	// Counting the rows we wrote would call a partial fill a success.
	remaining, err := u.candles.FindGaps(ctx, u.cfg.Symbol, u.cfg.MarketType, timeframe)
	if err != nil {
		u.recordFailedFill(ctx, gap, fmt.Sprintf("could not verify the fill: %v", err))
		return
	}
	if overlapsAny(remaining, from, to) {
		u.recordFailedFill(ctx, gap,
			fmt.Sprintf("refetched %d candles but the range is still incomplete", fetched))
		return
	}

	if err := u.gaps.MarkFilled(ctx, gap.Id); err != nil {
		u.log.WarnContext(ctx, "could not mark the gap filled", "gap_id", gap.Id, "error", err)
		return
	}
	u.log.InfoContext(ctx, "candle gap filled",
		"timeframe", timeframe.String(),
		"from", from.Format(time.RFC3339),
		"to", to.Format(time.RFC3339),
		"candles", fetched,
	)
}

// recordFailedFill counts the attempt and explains why it failed.
func (u *marketUsecase) recordFailedFill(ctx context.Context, gap models.DataGap, note string) {
	updated, err := u.gaps.RecordFillAttempt(ctx, gap.Id, note)
	if err != nil {
		u.log.WarnContext(ctx, "could not record the failed fill", "gap_id", gap.Id, "error", err)
		return
	}

	if updated.FillAttempts >= constants.MaxGapFillAttempts {
		// Stop chasing it. Some ranges genuinely do not exist, and retrying
		// those for ever buries the ones that are recoverable.
		u.log.WarnContext(ctx, "gap left unfilled after the retry budget was spent",
			"gap_id", gap.Id,
			"attempts", updated.FillAttempts,
			"from", gap.GapStart.Format(time.RFC3339),
			"to", gap.GapEnd.Format(time.RFC3339),
			"note", note,
		)
		return
	}
	u.log.WarnContext(ctx, "gap fill attempt failed",
		"gap_id", gap.Id, "attempts", updated.FillAttempts, "note", note)
}

// overlapsAny reports whether any remaining hole intersects the range that was
// just refetched.
func overlapsAny(gaps []candle.Gap, from, to time.Time) bool {
	for _, gap := range gaps {
		if gap.Start.Before(to) && gap.End.After(from) {
			return true
		}
	}
	return false
}
