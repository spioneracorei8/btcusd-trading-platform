package usecase

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/market"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/pipeline"
)

// Config is what assembling the status needs.
type Config struct {
	Symbol     string
	MarketType constants.MarketType

	// SignalMode is what delivery is configured to do. A queue that never
	// drains is expected in silent mode and a fault in notify mode, and the
	// number alone cannot say which.
	SignalMode constants.SignalMode

	// HeartbeatInterval is how often the collector publishes. It sets what
	// counts as a stale heartbeat: three intervals, so one missed tick is not
	// an alarm.
	HeartbeatInterval time.Duration

	Now func() time.Time
}

type pipelineUsecase struct {
	repo   pipeline.PipelineRepository
	market market.MarketUsecase
	cfg    Config
}

// NewPipelineUsecaseImpl builds the status reporter.
func NewPipelineUsecaseImpl(
	repo pipeline.PipelineRepository, marketUs market.MarketUsecase, cfg Config,
) (pipeline.PipelineUsecase, error) {
	switch {
	case repo == nil:
		return nil, errors.New("pipeline: no repository")
	case marketUs == nil:
		return nil, errors.New("pipeline: no way to read the collector's status")
	case cfg.Symbol == "":
		return nil, errors.New("pipeline: no symbol")
	case !cfg.MarketType.Valid():
		return nil, fmt.Errorf("pipeline: %q is not a market type", cfg.MarketType)
	}

	if cfg.HeartbeatInterval <= 0 {
		cfg.HeartbeatInterval = constants.DefaultHeartbeatInterval
	}
	if cfg.Now == nil {
		cfg.Now = func() time.Time { return time.Now().UTC() }
	}
	return &pipelineUsecase{repo: repo, market: marketUs, cfg: cfg}, nil
}

// Status reports the state of every stage at one instant.
//
// Every age below is measured from the same ObservedAt rather than from
// time.Now() at each field, so the numbers are consistent with one another —
// a reader comparing two of them is comparing two observations, not two
// clocks.
func (u *pipelineUsecase) Status(ctx context.Context) (pipeline.Status, error) {
	now := u.cfg.Now()

	status := pipeline.Status{
		Symbol:     u.cfg.Symbol,
		MarketType: u.cfg.MarketType.String(),
		ObservedAt: now,
		Concerns:   []pipeline.Concern{},
	}

	// Asked at the same instant everything else is measured against, so the
	// staleness the market usecase computes and the ages below agree.
	market, collectorErr := u.market.Status(ctx, now)
	if collectorErr == nil {
		status.Collector = collectorHealth(market.Collector, now)
		status.Evaluator = evaluatorHealth(market.Collector.Evaluator)
		status.Ingestion = ingestionHealth(market.Timeframes)
	}

	signals, err := u.repo.SignalActivity(ctx, u.cfg.Symbol, u.cfg.MarketType)
	if err != nil {
		return pipeline.Status{}, err
	}
	delivery, err := u.repo.DeliveryActivity(ctx, u.cfg.Symbol, u.cfg.MarketType)
	if err != nil {
		return pipeline.Status{}, err
	}

	status.Evaluator.SignalsTotal = signals.SignalsTotal
	status.Evaluator.LastSignalAt = age(signals.LastSignalAt, now, &status.Evaluator.LastSignalAge)

	status.Outcomes = pipeline.OutcomeHealth{
		Open:    signals.OutcomesOpen,
		Missing: signals.OutcomesMissing,
	}
	status.Outcomes.OldestOpenAt = age(signals.OldestOpenSignalAt, now, &status.Outcomes.OldestOpenAge)

	status.Delivery = pipeline.DeliveryHealth{
		Mode:              u.cfg.SignalMode.String(),
		Pending:           delivery.Pending,
		Sent:              delivery.Sent,
		Failed:            delivery.Failed,
		LastSentAt:        nullable(delivery.LastSentAt),
		DevicesRegistered: delivery.DevicesRegistered,
	}

	status.Concerns = u.concerns(status, collectorErr)
	return status, nil
}

// concerns names what looks wrong, in the order it would be investigated.
//
// # Why this is a list and not a boolean
//
// "Healthy" is not answerable here. A strategy at a tenth of a signal a day is
// silent for weeks by design, and no threshold separates that from a stopped
// evaluator without knowing what was configured and why. What the endpoint can
// do is state each observation and let a person judge — so every entry carries
// the number that produced it rather than a verdict.
func (u *pipelineUsecase) concerns(status pipeline.Status, collectorErr error) []pipeline.Concern {
	found := []pipeline.Concern{}
	add := func(component, detail string) {
		found = append(found, pipeline.Concern{Component: component, Detail: detail})
	}

	switch {
	case collectorErr != nil:
		add("collector", "the collector's status could not be read: "+collectorErr.Error())
	case !status.Collector.Reachable:
		add("collector", "no collector has ever registered for this symbol")
	default:
		if stale := u.cfg.HeartbeatInterval * 3; status.Collector.HeartbeatAge != nil &&
			*status.Collector.HeartbeatAge > stale {
			add("collector", fmt.Sprintf(
				"the last heartbeat was %s ago, more than three intervals of %s — "+
					"the collector process may be gone",
				status.Collector.HeartbeatAge.Round(time.Second), u.cfg.HeartbeatInterval))
		}
		if !status.Collector.WSConnected {
			add("collector", "the market data stream is not connected")
		}
	}

	if status.Evaluator.Configured && !status.Evaluator.Ready {
		add("evaluator", "not deciding: "+status.Evaluator.Reason)
	}

	// A signal with no outcome row is the follower not opening them, which is
	// distinct from one that is open and not resolving.
	if status.Outcomes.Missing > 0 {
		add("outcomes", fmt.Sprintf(
			"%d signals have no outcome row; the follower opens them once a pass, so a "+
				"count that persists means it is not running", status.Outcomes.Missing))
	}

	// Everything is configured, the queue is filling, and there is nowhere to
	// send. Stated as a sentence rather than left as a zero beside a mode,
	// because the reader would otherwise have to know that "notify" plus
	// "0 devices" means "recorded but not delivered" — and the person reading
	// this page is usually reading it because something is already confusing.
	if u.cfg.SignalMode.Delivers() && status.Delivery.DevicesRegistered == 0 {
		add("delivery", "no device is registered, so signals will be recorded and queued "+
			"but not delivered; open the app to register this phone")
	}

	if status.Delivery.Failed > 0 {
		add("delivery", fmt.Sprintf(
			"%d notifications were given up on; nothing retries a failed row, so these "+
				"were never delivered", status.Delivery.Failed))
	}

	// A gap is the collector noticing a hole and queueing a backfill, which is
	// the mechanism working. A count that stays put is the finding: every
	// signal whose window overlaps an unfilled gap resolves as invalidated and
	// leaves the statistics.
	if status.Ingestion.UnfilledGaps > 0 {
		add("ingestion", fmt.Sprintf(
			"%d candle gaps are unfilled (%s); signals whose window overlaps one resolve "+
				"as invalidated and are excluded from every figure",
			status.Ingestion.UnfilledGaps, gapBreakdown(status.Ingestion.Timeframes)))
	}

	// A queue that does not drain means something only in notify mode. In
	// silent mode nothing should be queued at all, which is its own finding.
	switch {
	case u.cfg.SignalMode.Delivers() && status.Delivery.Pending > 0:
		add("delivery", fmt.Sprintf(
			"%d notifications are waiting; in notify mode the worker should be draining them",
			status.Delivery.Pending))
	case !u.cfg.SignalMode.Delivers() && status.Delivery.Pending > 0:
		add("delivery", fmt.Sprintf(
			"%d notifications are queued while the mode is silent; they were queued before "+
				"delivery was switched off and nothing will send them",
			status.Delivery.Pending))
	}

	return found
}

// gapBreakdown names the timeframes actually holding gaps.
//
// Only the non-zero ones: a list reading "1m 3, 5m 0, 15m 0, 1h 0, 4h 0, 1d 0"
// buries the one fact it contains.
func gapBreakdown(timeframes []pipeline.TimeframeGaps) string {
	parts := make([]string, 0, len(timeframes))
	for _, tf := range timeframes {
		if tf.UnfilledGaps > 0 {
			parts = append(parts, fmt.Sprintf("%s: %d", tf.Timeframe, tf.UnfilledGaps))
		}
	}
	if len(parts) == 0 {
		return "timeframe unknown"
	}
	return strings.Join(parts, ", ")
}

// ingestionHealth summarises the unfilled gaps across every timeframe.
//
// The total and the breakdown are both carried. A total answers "is the data
// whole"; the breakdown answers "which series is stuck", and one series stuck
// while the rest advance is a different problem from all of them stalling.
func ingestionHealth(timeframes []models.TimeframeStatus) pipeline.IngestionHealth {
	health := pipeline.IngestionHealth{
		Timeframes: make([]pipeline.TimeframeGaps, 0, len(timeframes)),
	}
	for _, tf := range timeframes {
		health.UnfilledGaps += tf.UnfilledGaps

		row := pipeline.TimeframeGaps{
			Timeframe:      tf.Timeframe.String(),
			UnfilledGaps:   tf.UnfilledGaps,
			LatestOpenTime: tf.LatestOpenTime,
		}
		if tf.LatestAgeSeconds != nil {
			age := time.Duration(*tf.LatestAgeSeconds) * time.Second
			row.LatestAge = &age
		}
		health.Timeframes = append(health.Timeframes, row)
	}
	return health
}

// collectorHealth converts the status row.
func collectorHealth(status models.CollectorStatus, now time.Time) pipeline.CollectorHealth {
	health := pipeline.CollectorHealth{
		Reachable:      !status.StartedAt.IsZero(),
		State:          status.State.String(),
		WSConnected:    status.WSConnected,
		StartedAt:      nullable(status.StartedAt),
		ReconnectCount: status.ReconnectCount,
		LastDisconnect: status.LastDisconnectNote,
	}
	health.UpdatedAt = age(status.UpdatedAt, now, &health.HeartbeatAge)
	return health
}

// evaluatorHealth converts what the collector published about the live path.
func evaluatorHealth(state models.EvaluatorState) pipeline.EvaluatorHealth {
	return pipeline.EvaluatorHealth{
		Configured: state.Configured(),
		Strategy:   state.Strategy,
		Timeframe:  state.Timeframe.String(),
		Ready:      state.Ready,
		Reason:     state.Reason,
	}
}

// age renders an instant and fills in how long ago it was.
//
// Both, because one without the other is awkward: an age alone cannot be
// checked against a log, and an instant alone makes the reader do arithmetic
// against a clock that may not be the server's.
func age(at, now time.Time, into **time.Duration) *time.Time {
	if at.IsZero() {
		return nil
	}
	elapsed := now.Sub(at.UTC())
	*into = &elapsed

	utc := at.UTC()
	return &utc
}

// nullable renders an instant that may be absent. Absent is null, never the
// zero instant — which sorts and formats like a real one.
func nullable(at time.Time) *time.Time {
	if at.IsZero() {
		return nil
	}
	utc := at.UTC()
	return &utc
}
