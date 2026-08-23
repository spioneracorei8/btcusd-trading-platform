// Package pipeline declares the contract for reporting whether the signal
// pipeline is alive.
//
// # Why this exists
//
// The audit of phase 07 asked, of each component: if this stopped working
// entirely, how long before anyone noticed? For everything after ingestion the
// answer was "indefinitely". /internal/market/status answers it for candles;
// nothing answered it for signals, outcomes or delivery.
//
// The difficulty is that silence is the normal output. A strategy at a tenth
// of a signal a day is quiet for weeks by design, so an endpoint that only
// said "no signals recently" would be useless. What it reports instead is the
// state of each stage and the age of the last thing each one did, leaving the
// judgement to a person who knows what they configured.
package pipeline

import (
	"context"
	"net/http"
	"time"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
)

// Status is the health of the whole signal pipeline.
type Status struct {
	Symbol     string
	MarketType string

	// ObservedAt is when this was assembled, so every age below is measured
	// from one instant rather than from four.
	ObservedAt time.Time

	Collector CollectorHealth
	Evaluator EvaluatorHealth
	Outcomes  OutcomeHealth
	Delivery  DeliveryHealth

	// Concerns are the specific things that look wrong, in the order they
	// would be investigated. Empty is the healthy answer and is reported as
	// an empty list rather than omitted — a missing field reads as a check
	// that did not run.
	Concerns []Concern
}

// CollectorHealth is ingestion, from the status row the collector publishes.
type CollectorHealth struct {
	// Reachable is whether a status row exists at all. False means the
	// collector has never run against this symbol, which is a different
	// problem from one that has stopped.
	Reachable bool

	State       string
	WSConnected bool

	StartedAt    *time.Time
	UpdatedAt    *time.Time
	HeartbeatAge *time.Duration

	ReconnectCount int32
	LastDisconnect string
}

// EvaluatorHealth is the live signal path, as the collector last published it.
type EvaluatorHealth struct {
	// Configured is false when no strategy is running. That is a
	// configuration, not a fault, and the distinction is the whole point:
	// "switched off" and "stuck warming up" both produce no signals.
	Configured bool

	Strategy  string
	Timeframe string

	Ready bool

	// Reason is why it is not deciding, when it is not.
	Reason string

	LastSignalAt  *time.Time
	LastSignalAge *time.Duration
	SignalsTotal  int64
}

// OutcomeHealth is the follower.
type OutcomeHealth struct {
	Open int64

	// OldestOpenAt is the signal that has been followed longest without
	// resolving. A follower that has stopped has no other symptom: this age
	// simply keeps growing.
	OldestOpenAt  *time.Time
	OldestOpenAge *time.Duration

	// Missing counts signals with no outcome row at all. Non-zero for longer
	// than one pass means the follower is not opening them.
	Missing int64
}

// DeliveryHealth is the notification queue.
type DeliveryHealth struct {
	// Mode is silent or notify. A queue that never drains is expected in
	// silent mode and a fault in notify mode, and the number alone cannot
	// say which.
	Mode string

	Pending int64
	Sent    int64

	// Failed is the number that matters. Nothing retries a failed row, so a
	// permanently broken destination shows up here and nowhere else.
	Failed int64

	LastSentAt *time.Time
}

// Concern is one thing worth looking at, with what to look at first.
type Concern struct {
	// Component is which stage: collector, evaluator, outcomes, delivery.
	Component string

	// Detail says what was observed, with the number that produced it.
	Detail string
}

// PipelineUsecase assembles the status.
type PipelineUsecase interface {
	// Status reports the state of every stage at one instant.
	Status(ctx context.Context) (Status, error)
}

// PipelineRepository reads what the pipeline has been doing.
type PipelineRepository interface {
	// SignalActivity reports when signals were last produced and how much is
	// still being followed.
	SignalActivity(ctx context.Context, symbol string, marketType constants.MarketType) (SignalActivity, error)

	// DeliveryActivity reports the delivery queue by state.
	DeliveryActivity(ctx context.Context, symbol string, marketType constants.MarketType) (DeliveryActivity, error)
}

// SignalActivity is the signal and outcome half.
type SignalActivity struct {
	LastSignalAt       time.Time
	SignalsTotal       int64
	OutcomesOpen       int64
	OldestOpenSignalAt time.Time
	OutcomesMissing    int64
}

// DeliveryActivity is the queue half.
type DeliveryActivity struct {
	Pending    int64
	Sent       int64
	Failed     int64
	LastSentAt time.Time
}

// StatusHandler serves the pipeline status.
type StatusHandler interface {
	// Status answers GET /api/v1/status.
	Status(w http.ResponseWriter, r *http.Request)
}
