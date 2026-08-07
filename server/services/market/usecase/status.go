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
		// The collector has never started. Everything else is still worth
		// reporting, so this is not an error.
	case err != nil:
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

	status.Stale = isStale(status, now)
	return status, nil
}

// timeframeStatus reports the health of one stored series.
func (u *marketUsecase) timeframeStatus(ctx context.Context, timeframe constants.Timeframe, now time.Time) (models.TimeframeStatus, error) {
	result := models.TimeframeStatus{Timeframe: timeframe}

	latest, err := u.candles.FetchLatestCandle(ctx, u.cfg.Symbol, u.cfg.MarketType, timeframe)
	switch {
	case errors.Is(err, constants.ErrNotFound):
		// No candles yet; leave the times nil rather than reporting a zero
		// timestamp that would read as 1970.
	case err != nil:
		return models.TimeframeStatus{}, fmt.Errorf("read latest %s candle: %w", timeframe, err)
	default:
		openTime := latest.OpenTime
		age := int64(now.Sub(openTime) / time.Second)
		result.LatestOpenTime = &openTime
		result.LatestAgeSeconds = &age
	}

	unfilled, err := u.gaps.CountUnfilled(ctx, u.cfg.Symbol, u.cfg.MarketType, timeframe)
	if err != nil {
		return models.TimeframeStatus{}, fmt.Errorf("count %s gaps: %w", timeframe, err)
	}
	result.UnfilledGaps = unfilled

	return result, nil
}

// isStale reports the one combination that means something is wrong in a way
// no other check catches: the stream claims to be connected while the newest
// 1m candle has gone cold.
//
// Either signal alone is ordinary — a disconnect is routine, and an old
// candle during a known outage is expected — but together they mean the
// collector believes it is working while it is not.
func isStale(status models.MarketStatus, now time.Time) bool {
	if !status.Collector.WSConnected {
		return false
	}
	// A heartbeat that stopped advancing means the collector is gone, and a
	// dead collector is a different problem than a stalled one.
	if status.Collector.HeartbeatAge(now) > constants.StaleCandleThreshold {
		return false
	}

	for _, timeframe := range status.Timeframes {
		if timeframe.Timeframe != constants.Timeframe1m {
			continue
		}
		if timeframe.LatestOpenTime == nil {
			return true
		}
		return now.Sub(*timeframe.LatestOpenTime) > constants.StaleCandleThreshold
	}
	return false
}
