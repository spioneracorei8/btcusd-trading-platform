package usecase

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/shopspring/decimal"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/candle"
	_indicator_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/indicator/usecase"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/signal"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/strategy"
)

// EvaluatorConfig is what the live signal path needs to know.
type EvaluatorConfig struct {
	Symbol     string
	MarketType constants.MarketType
	Timeframe  constants.Timeframe

	// Strategy is the code under evaluation, and Params the resolved
	// parameter set it was built with. Params is recorded on every signal.
	Strategy strategy.Strategy
	Params   map[string]string

	// Indicators is the same configuration the backtest engine runs.
	Indicators _indicator_us.SetConfig
}

// evaluator drives one strategy over the live candle stream.
type evaluator struct {
	config  EvaluatorConfig
	log     *slog.Logger
	candles candle.CandleUsecase
	signals signal.SignalUsecase

	set *_indicator_us.Set

	// fed counts the bars pushed through the indicators and the strategy,
	// whatever their source. Readiness is derived from it on every bar rather
	// than latched once at start-up: a collector starting against an empty
	// database has nothing to replay, and a warm flag set at that moment would
	// stay false for the life of the process while the bars it needed streamed
	// past it one at a time.
	fed int

	// announced keeps the line saying the strategy went live to one.
	announced bool

	// lastBar is the most recent close fed to the strategy. It is what makes
	// an out-of-order or repeated bar detectable, and both happen — a
	// reconnect backfills bars the stream then delivers again.
	lastBar time.Time
}

// NewSignalEvaluatorImpl builds the live signal path.
func NewSignalEvaluatorImpl(
	config EvaluatorConfig,
	log *slog.Logger,
	candles candle.CandleUsecase,
	signals signal.SignalUsecase,
) (signal.SignalEvaluator, error) {
	if config.Strategy == nil {
		return nil, errors.New("signal: no strategy to evaluate")
	}
	if !config.Timeframe.Valid() {
		return nil, fmt.Errorf("signal: %q is not a timeframe", config.Timeframe)
	}

	set, err := _indicator_us.NewSet(config.Indicators)
	if err != nil {
		return nil, fmt.Errorf("signal: %w", err)
	}

	return &evaluator{
		config:  config,
		log:     log,
		candles: candles,
		signals: signals,
		set:     set,
	}, nil
}

// warmupBars is how much history the strategy and the indicators need.
//
// The longer of the two, exactly as the backtest engine computes it: an
// indicator that has not converged and a strategy that has not seen enough
// history are the same failure, a decision made on values that do not yet mean
// what they will mean later.
func (e *evaluator) warmupBars() int {
	return max(e.set.WarmupPeriod(), e.config.Strategy.WarmupPeriod())
}

// Warmup replays stored history through the same path a live bar takes.
//
// # Why the replayed bars emit nothing
//
// They have already happened. A push notification about a bar that closed
// three months ago is noise, and recording a signal for it would put a row in
// the table dated long before the process that wrote it — which is exactly the
// shape of the look-ahead this system is built to make impossible to express.
//
// The cost is real and worth stating: bars that closed while the collector was
// down are replayed as warm-up, so their signals are never recorded. That is
// visible rather than silent — the signal series has a hole matching the
// outage, and the collector logs the range it replayed.
func (e *evaluator) Warmup(ctx context.Context) error {
	required := e.warmupBars()
	if required <= 0 {
		return nil
	}

	latest, err := e.candles.FetchLatestCandle(ctx, e.config.Symbol, e.config.MarketType, e.config.Timeframe)
	switch {
	case errors.Is(err, constants.ErrNotFound):
		e.log.WarnContext(ctx, "there is no history to warm the strategy up on",
			"timeframe", e.config.Timeframe.String(),
			"reason", fmt.Sprintf("no %s candles are stored yet", e.config.Timeframe),
			"note", "the strategy will warm up on live bars instead")
		return nil
	case err != nil:
		return fmt.Errorf("signal: find the end of the series: %w", err)
	}

	// A margin above the strict requirement, because the series may have
	// holes: fetching exactly as many bars as the warm-up needs would come up
	// short by however many are missing, and come up short silently.
	span := time.Duration(required*2) * e.config.Timeframe.Duration()
	from := latest.OpenTime.Add(-span)

	seen := 0
	err = e.candles.StreamCandles(ctx, candle.FetchCandlesParams{
		Symbol:     e.config.Symbol,
		MarketType: e.config.MarketType,
		Timeframe:  e.config.Timeframe,
		From:       from,
		To:         latest.OpenTime,
	}, func(bar models.Candle) error {
		e.feed(bar)
		seen++
		return nil
	})
	if err != nil {
		return fmt.Errorf("signal: replay history: %w", err)
	}

	if ready, why := e.Ready(); !ready {
		e.log.WarnContext(ctx, "the strategy is not warm after replaying what is stored",
			"replayed", seen,
			"reason", why,
			"note", "it will warm up on live bars as they close")
		return nil
	}

	e.announced = true
	e.log.InfoContext(ctx, "the strategy is warm and evaluating live bars",
		"strategy", e.config.Strategy.Name(),
		"version", e.config.Strategy.Version(),
		"timeframe", e.config.Timeframe.String(),
		"replayed", seen,
		"through", latest.OpenTime.UTC().Format(time.RFC3339),
	)
	return nil
}

// Ready reports whether the strategy may decide, and why not when it may not.
//
// It is computed from what has actually been fed rather than from a flag set
// during warm-up, so it is true of the evaluator now: history replayed at
// start-up and bars that closed since count the same, because to an indicator
// they are the same. Silence therefore always has a reason attached, and the
// reason carries the count so an operator can see it shrinking.
func (e *evaluator) Ready() (bool, string) {
	if !e.set.Ready() {
		return false, fmt.Sprintf("the indicators have seen %d %s bars and converge at %d",
			e.fed, e.config.Timeframe, e.set.WarmupPeriod())
	}
	if need := e.config.Strategy.WarmupPeriod(); e.fed < need {
		return false, fmt.Sprintf("the strategy has seen %d %s bars and needs %d before it may decide",
			e.fed, e.config.Timeframe, need)
	}
	return true, ""
}

// feed advances the indicators and the strategy by one bar, discarding what
// the strategy says.
//
// The indicators are stateful and the strategy usually is too, so every bar
// must pass through even when nothing may be emitted from it. Skipping one
// would leave both permanently out of step with the series — the same rule the
// backtest engine follows for bars it does not score.
func (e *evaluator) feed(bar models.Candle) ([]strategy.Intent, models.IndicatorSnapshot) {
	snapshot, ready := e.set.Update(bar)
	e.lastBar = bar.OpenTime
	e.fed++

	if !ready {
		// The strategy is still shown the bar, so its own warm-up advances in
		// step with the indicators'. What it says is discarded: a decision
		// taken on values that have not converged is not a decision.
		e.config.Strategy.OnBar(strategy.BarContext{
			Candle:     bar,
			Indicators: snapshot,
			Position:   strategy.Position{Direction: constants.DirectionFlat},
		})
		return nil, snapshot
	}

	return e.config.Strategy.OnBar(strategy.BarContext{
		Candle:     bar,
		Indicators: snapshot,
		// Flat, always. This path holds no position: it records what the
		// strategy said, and a strategy that suppresses entries while holding
		// one would go silent forever here waiting for an exit that no order
		// was ever placed to need.
		Position: strategy.Position{Direction: constants.DirectionFlat},
	}), snapshot
}

// OnClosedCandle evaluates one closed candle and records what it produced.
//
// # Why an out-of-order bar is refused rather than fed
//
// A reconnect backfills the bars missed while disconnected, and the stream
// then delivers some of them again. Feeding a bar the indicators have already
// seen would double-count it, and feeding one older than the last would leave
// every value afterwards subtly wrong with nothing to show for it. The
// duplicate is dropped; the unique constraint on signals is the second line of
// defence rather than the first.
func (e *evaluator) OnClosedCandle(ctx context.Context, bar models.Candle) (models.Signal, bool, error) {
	if !bar.IsClosed {
		// The rule this system is built on. It is enforced again in the
		// usecase below, and stated here so the reason is legible at the point
		// the bar arrives.
		return models.Signal{}, false, nil
	}
	if bar.Timeframe != e.config.Timeframe {
		return models.Signal{}, false, nil
	}
	if !e.lastBar.IsZero() && !bar.OpenTime.After(e.lastBar) {
		e.log.DebugContext(ctx, "a bar the strategy has already seen was delivered again",
			"open_time", bar.OpenTime.UTC().Format(time.RFC3339),
			"last_seen", e.lastBar.UTC().Format(time.RFC3339))
		return models.Signal{}, false, nil
	}

	intents, snapshot := e.feed(bar)
	if ready, why := e.Ready(); !ready {
		e.log.InfoContext(ctx, "the bar was not evaluated",
			"open_time", bar.OpenTime.UTC().Format(time.RFC3339), "reason", why)
		return models.Signal{}, false, nil
	}
	if !e.announced {
		e.announced = true
		e.log.InfoContext(ctx, "the strategy is warm and evaluating live bars",
			"strategy", e.config.Strategy.Name(),
			"version", e.config.Strategy.Version(),
			"timeframe", e.config.Timeframe.String(),
			"fed", e.fed,
		)
	}

	intent, direction, ok := firstEntry(intents)
	if !ok {
		return models.Signal{}, false, nil
	}

	recorded, err := e.record(ctx, bar, snapshot, intent, direction)
	if errors.Is(err, constants.ErrDuplicateSignal) {
		// Already recorded for this bar, by an earlier process or an earlier
		// delivery of the same candle. Not a failure: the constraint did its
		// job, and the owner is not notified twice.
		e.log.DebugContext(ctx, "a signal for this bar was already recorded",
			"signal_time", bar.CloseTime.UTC().Format(time.RFC3339))
		return models.Signal{}, false, nil
	}
	if err != nil {
		return models.Signal{}, false, err
	}
	return recorded, true, nil
}

// firstEntry picks the entry intent out of what a strategy returned.
//
// One at most. The strategies here emit a single entry, and taking the first
// keeps a strategy that emitted two from recording two signals for one bar —
// which the unique constraint would refuse anyway, as an error rather than as
// the deliberate choice it should be.
func firstEntry(intents []strategy.Intent) (strategy.Intent, constants.Direction, bool) {
	for _, intent := range intents {
		switch intent.Kind {
		case strategy.IntentEnterLong:
			return intent, constants.DirectionLong, true
		case strategy.IntentEnterShort:
			return intent, constants.DirectionShort, true
		}
	}
	return strategy.Intent{}, constants.DirectionFlat, false
}

// record writes the signal for one entry intent.
//
// entry_price is deliberately left unset. A decision taken on a bar's close
// cannot fill on that close, so what a position would have opened at is the
// next bar's open plus slippage — a number this bar does not yet know. It is
// filled in once that bar closes. signal_price carries the close the strategy
// actually saw, which is known now and is what a notification quotes.
func (e *evaluator) record(
	ctx context.Context,
	bar models.Candle,
	snapshot models.IndicatorSnapshot,
	intent strategy.Intent,
	direction constants.Direction,
) (models.Signal, error) {
	reason, err := signal.BuildReason(
		intent.Reason, bar, snapshot,
		"", levelString(intent.Stop), levelString(intent.Target),
		signal.StrategyReason{
			Name:    e.config.Strategy.Name(),
			Version: e.config.Strategy.Version(),
			Params:  signal.SortedParams(e.config.Params),
		},
	).Encode()
	if err != nil {
		return models.Signal{}, fmt.Errorf("signal: %w", err)
	}

	return e.signals.CreateSignal(ctx, models.Signal{
		Symbol:     e.config.Symbol,
		MarketType: e.config.MarketType,
		Timeframe:  e.config.Timeframe,
		SignalTime: bar.CloseTime,
		Direction:  direction,

		// None of these strategies reports a confidence — they emit a decision
		// or nothing. The column is NOT NULL, so it carries the value that
		// means "not reported" rather than an invented one, and nothing should
		// average it. What actually happened is in the reason.
		Strength: decimal.NewFromInt(constants.SignalStrengthNotReported),

		SignalPrice: decimal.NullDecimal{Decimal: bar.Close, Valid: true},
		StopLoss:    nullLevel(intent.Stop),
		TakeProfit:  nullLevel(intent.Target),

		StrategyName:    e.config.Strategy.Name(),
		StrategyVersion: e.config.Strategy.Version(),
		Reason:          reason,
	}, bar)
}

// nullLevel renders an advisory level, absent when the strategy set none.
func nullLevel(level decimal.Decimal) decimal.NullDecimal {
	if !level.IsPositive() {
		return decimal.NullDecimal{}
	}
	return decimal.NullDecimal{Decimal: level, Valid: true}
}

// levelString renders a level for the audit record.
func levelString(level decimal.Decimal) string {
	if !level.IsPositive() {
		return ""
	}
	return level.String()
}
