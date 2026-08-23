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

	// Evaluator is what the live signal path is doing.
	//
	// It is published here because the api runs in a different process and
	// cannot see the collector's memory. Readiness that is never written down
	// is readiness nobody outside can observe.
	Evaluator EvaluatorState

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

	// EarliestOpenTime is the oldest stored candle. With LatestOpenTime and
	// the target range it shows a long backfill advancing, which is the only
	// way to tell progress from a stall.
	EarliestOpenTime *time.Time

	// LatestOpenTime is the open time of the newest stored candle, nil when
	// the series is still empty.
	LatestOpenTime *time.Time

	// LatestAgeSeconds is how old that candle is. Nil when there is none.
	LatestAgeSeconds *int64

	// UnfilledGaps counts data_gaps rows still awaiting a successful backfill.
	UnfilledGaps int64

	// BackfillFrom and BackfillTo bound the range the collector is working
	// towards, so a user watching a three-year backfill can see where it is.
	BackfillFrom time.Time
	BackfillTo   time.Time
}

// MarketStatus is the body of GET /internal/market/status.
type MarketStatus struct {
	Symbol     string
	MarketType constants.MarketType

	Collector  CollectorStatus
	Timeframes []TimeframeStatus

	// Stale reports the combination worth waking up for: the stream claims to
	// be connected while the newest candle has gone cold.
	//
	// It is a pointer because "the check did not run" is a third answer,
	// distinct from true and false. Outside the live state the question is
	// meaningless — an old candle during a backfill is progress — so the
	// result is nil rather than a false that looks like an all-clear.
	Stale *bool
}

// EvaluatorState is the live signal path, as the collector last reported it.
//
// # Why silence needs three different explanations
//
// A strategy at a tenth of a signal a day is quiet for weeks by design. From
// outside, "no strategy configured", "still warming up" and "warm and found
// nothing" look identical — and the first two are faults while the third is
// the system working. Nothing in phase 07 could tell them apart.
type EvaluatorState struct {
	// Strategy is empty when none is configured, which is the difference
	// between a pipeline that is off and one that is stuck.
	Strategy  string
	Timeframe constants.Timeframe

	// Ready is whether the strategy may decide, and Reason why not when it
	// may not. Reason is empty when Ready, and when no strategy is
	// configured at all.
	Ready  bool
	Reason string
}

// Configured reports whether a strategy is running at all.
func (e EvaluatorState) Configured() bool { return e.Strategy != "" }
