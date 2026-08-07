package usecase

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
)

// Status assembles the answer to "is my data healthy right now" without
// anybody having to open psql.
//
// It reads entirely from the database, so the api can serve it even though
// the collector runs in a different container.
func (u *marketUsecase) Status(ctx context.Context, now time.Time) (models.MarketStatus, error) {
	status := models.MarketStatus{
		Symbol:     u.cfg.Symbol,
		MarketType: u.cfg.MarketType,
		Timeframes: make([]models.TimeframeStatus, 0, len(u.cfg.Timeframes)),
	}

	collector, err := u.status.FetchStatus(ctx, u.cfg.Symbol, u.cfg.MarketType)
	switch {
	case errors.Is(err, constants.ErrNotFound):
		// No collector has ever registered. That is a valid state and the
		// single most important thing this endpoint can report — returning an
		// error here would send the reader back to the container logs, which
		// is exactly the workflow the endpoint replaces.
		status.Collector = models.CollectorStatus{
			Symbol:     u.cfg.Symbol,
			MarketType: u.cfg.MarketType,
			State:      constants.CollectorNeverStarted,
		}
	case err != nil:
		// A database that cannot be read is a genuine failure, and the only
		// thing that should produce a 500 here.
		return models.MarketStatus{}, fmt.Errorf("read collector status: %w", err)
	default:
		status.Collector = collector
	}

	for _, timeframe := range u.cfg.Timeframes {
		timeframeStatus, err := u.timeframeStatus(ctx, timeframe, now)
		if err != nil {
			return models.MarketStatus{}, err
		}
		status.Timeframes = append(status.Timeframes, timeframeStatus)
	}

	status.Stale = evaluateStaleness(status, now)
	return status, nil
}

// timeframeStatus reports the health of one stored series.
func (u *marketUsecase) timeframeStatus(ctx context.Context, timeframe constants.Timeframe, now time.Time) (models.TimeframeStatus, error) {
	result := models.TimeframeStatus{
		Timeframe: timeframe,
		// The range the collector is working towards, so a long backfill can
		// be seen advancing rather than merely being old.
		BackfillFrom: u.cfg.BackfillFrom,
		BackfillTo:   now,
	}

	latest, err := u.candles.FetchLatestCandle(ctx, u.cfg.Symbol, u.cfg.MarketType, timeframe)
	switch {
	case errors.Is(err, constants.ErrNotFound):
		// No candles yet; the times stay nil rather than reporting a zero
		// timestamp that would read as 1970.
	case err != nil:
		return models.TimeframeStatus{}, fmt.Errorf("read latest %s candle: %w", timeframe, err)
	default:
		openTime := latest.OpenTime
		age := int64(now.Sub(openTime) / time.Second)
		result.LatestOpenTime = &openTime
		result.LatestAgeSeconds = &age
	}

	earliest, err := u.candles.FetchEarliestCandle(ctx, u.cfg.Symbol, u.cfg.MarketType, timeframe)
	switch {
	case errors.Is(err, constants.ErrNotFound):
	case err != nil:
		return models.TimeframeStatus{}, fmt.Errorf("read earliest %s candle: %w", timeframe, err)
	default:
		openTime := earliest.OpenTime
		result.EarliestOpenTime = &openTime
	}

	unfilled, err := u.gaps.CountUnfilled(ctx, u.cfg.Symbol, u.cfg.MarketType, timeframe)
	if err != nil {
		return models.TimeframeStatus{}, fmt.Errorf("count %s gaps: %w", timeframe, err)
	}
	result.UnfilledGaps = unfilled

	return result, nil
}

// evaluateStaleness answers the staleness question only where it means
// something.
//
// The check exists to catch one specific failure: the stream reports itself
// connected while data has quietly stopped arriving. That is only meaningful
// once the collector is live. During a backfill the newest candle is
// legitimately years old, and reporting false there would be indistinguishable
// from a genuine all-clear.
//
// Outside the live state the result is nil — the check did not run, so it has
// no answer.
func evaluateStaleness(status models.MarketStatus, now time.Time) *bool {
	if status.Collector.State != constants.CollectorLive {
		return nil
	}
	// A heartbeat that stopped advancing means the collector is gone, which is
	// a different problem from a stalled stream and is visible from
	// heartbeat_age_seconds directly.
	if status.Collector.HeartbeatAge(now) > constants.StaleCandleThreshold {
		return nil
	}
	// The question is specifically "connected, yet not advancing". Without a
	// connection it has no answer — the data is not silently stale, it is
	// openly disconnected, which ws_connected already says.
	if !status.Collector.WSConnected {
		return nil
	}

	for _, timeframe := range status.Timeframes {
		if timeframe.Timeframe != constants.Timeframe1m {
			continue
		}

		stale := timeframe.LatestOpenTime == nil ||
			now.Sub(*timeframe.LatestOpenTime) > constants.StaleCandleThreshold
		return &stale
	}
	return nil
}
