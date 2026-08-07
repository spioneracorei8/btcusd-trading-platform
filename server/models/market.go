package models

import (
	"time"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
)

// CollectorStatus is what the collector publishes about itself so the api,
// which runs in a different container, can report on it.
type CollectorStatus struct {
	Symbol     string
	MarketType constants.MarketType

	// State is the lifecycle phase. It decides whether staleness is even a
	// meaningful question: an old newest candle is normal progress while
	// backfilling and a silent failure while live.
	State          constants.CollectorState
	StateChangedAt time.Time

	// WSConnected is only meaningful next to a fresh UpdatedAt. A collector
	// that died mid-connection leaves this true forever.
	WSConnected bool

	LastConnectedAt    *time.Time
	LastDisconnectedAt *time.Time
	LastDisconnectNote string
	ReconnectCount     int32

	// StartedAt is when this collector process started. Together with
	// UpdatedAt it separates "up for three days" from "restarting every ten
	// seconds", which look identical from a single heartbeat.
	StartedAt time.Time
	UpdatedAt time.Time
}

// Uptime is how long the collector has been running.
func (s CollectorStatus) Uptime(now time.Time) time.Duration {
	return now.Sub(s.StartedAt)
}

// HeartbeatAge is how stale the reading is. A value far beyond the configured
// heartbeat interval means the collector is gone, whatever WSConnected says.
func (s CollectorStatus) HeartbeatAge(now time.Time) time.Duration {
	return now.Sub(s.UpdatedAt)
}

// TimeframeStatus is the per-timeframe health of the stored candle series.
type TimeframeStatus struct {
	Timeframe constants.Timeframe

	// LatestOpenTime is the open time of the newest stored candle, nil when
	// the series is still empty.
	LatestOpenTime *time.Time

	// LatestAgeSeconds is how old that candle is. Nil when there is none.
	LatestAgeSeconds *int64

	// UnfilledGaps counts data_gaps rows still awaiting a successful backfill.
	UnfilledGaps int64
}

// MarketStatus is the body of GET /internal/market/status.
type MarketStatus struct {
	Symbol     string
	MarketType constants.MarketType

	Collector  CollectorStatus
	Timeframes []TimeframeStatus

	// Stale reports the combination worth waking up for: the stream claims to
	// be connected while the newest candle has gone cold.
	Stale bool
}
