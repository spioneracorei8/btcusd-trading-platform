// Command collector ingests Binance market data into TimescaleDB.
//
// It keeps the candles table complete for every configured timeframe:
// backfilling on start and after every reconnect, storing closed candles as
// they arrive, and recording any gap it cannot fill so a later backtest knows
// which ranges to distrust.
//
// It reads public market data only. No order, trade, account or withdrawal
// endpoint is called, and no API key is used.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sync/errgroup"

	"github.com/spioneracorei8/btcusd-trading-platform/server/config"
	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/database"
	"github.com/spioneracorei8/btcusd-trading-platform/server/helper"
	"github.com/spioneracorei8/btcusd-trading-platform/server/logger"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"

	"github.com/spioneracorei8/btcusd-trading-platform/server/services/candle"
	_candle_repo "github.com/spioneracorei8/btcusd-trading-platform/server/services/candle/repository"
	_candle_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/candle/usecase"
	_datagap_repo "github.com/spioneracorei8/btcusd-trading-platform/server/services/datagap/repository"
	_datagap_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/datagap/usecase"
	_indicator_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/indicator/usecase"
	_market_repo "github.com/spioneracorei8/btcusd-trading-platform/server/services/market/repository"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/market/repository/binance"
	_market_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/market/usecase"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/notify"
	_notify_repo "github.com/spioneracorei8/btcusd-trading-platform/server/services/notify/repository"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/notify/repository/webpush"
	_notify_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/notify/usecase"
	_outcome_repo "github.com/spioneracorei8/btcusd-trading-platform/server/services/outcome/repository"
	_outcome_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/outcome/usecase"
	_signal "github.com/spioneracorei8/btcusd-trading-platform/server/services/signal"
	_signal_repo "github.com/spioneracorei8/btcusd-trading-platform/server/services/signal/repository"
	_signal_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/signal/usecase"
	_strategy_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/strategy/usecase"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		// Every missing or invalid variable is named in err.
		slog.Error("invalid configuration", "error", err)
		os.Exit(1)
	}

	log := logger.New(os.Stdout, logger.Options{
		Level:  cfg.App.LogLevel,
		Format: logger.FormatForEnv(cfg.App.Env),
	})
	slog.SetDefault(log)

	if err := run(cfg, log); err != nil {
		log.Error("collector stopped with error", "error", err)
		os.Exit(1)
	}
	log.Info("collector stopped cleanly")
}

func run(cfg *config.Config, log *slog.Logger) error {
	// ctx is cancelled on SIGTERM/SIGINT. Every goroutine below observes it,
	// so a shutdown finishes in-flight writes rather than abandoning them.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := database.NewPool(ctx, database.PoolOptions{
		DSN:            cfg.Database.URL,
		MaxConns:       cfg.Database.MaxConns,
		ConnectTimeout: cfg.Database.ConnectTimeout,
	})
	if err != nil {
		return err // database.NewPool already names the operation
	}
	defer pool.Close()

	//==============================================================
	// # REPOSITORIES
	//==============================================================
	candleRepo := _candle_repo.NewCandleRepoImpl(pool)
	dataGapRepo := _datagap_repo.NewDataGapRepoImpl(pool)
	collectorStatusRepo := _market_repo.NewCollectorStatusRepoImpl(pool)
	marketDataRepo := binance.NewMarketDataRepoImpl(binance.Options{
		RESTBaseURL: cfg.Market.RESTBaseURL,
		WSBaseURL:   cfg.Market.WSBaseURL,
	})

	//==============================================================
	// # USECASES
	//==============================================================
	candleUs := _candle_us.NewCandleUsecaseImpl(candleRepo)
	dataGapUs := _datagap_us.NewDataGapUsecaseImpl(dataGapRepo)

	//==============================================================
	// # LIVE SIGNALS
	//==============================================================
	signalUs := _signal_us.NewSignalUsecaseImpl(_signal_repo.NewSignalRepoImpl(pool))
	evaluator, err := buildEvaluator(cfg, log, candleUs, signalUs)
	if err != nil {
		return err
	}

	notifyUs, err := buildNotify(ctx, cfg, log, pool, signalUs)
	if err != nil {
		return err
	}

	//==============================================================
	// # OUTCOMES
	//==============================================================
	outcomeUs, err := _outcome_us.NewOutcomeUsecaseImpl(
		_outcome_repo.NewOutcomeRepoImpl(pool), log, signalUs, candleUs, dataGapUs,
		_outcome_us.Config{
			Symbol:     cfg.Market.Symbol,
			MarketType: cfg.Market.Type,
			Costs:      cfg.BacktestCosts(),
			ExpiryBars: cfg.Outcome.ExpiryBars,
			Interval:   cfg.Outcome.Interval,
		},
	)
	if err != nil {
		return err
	}

	marketUs := _market_us.NewMarketUsecaseImpl(
		_market_us.Config{
			Symbol:            cfg.Market.Symbol,
			MarketType:        cfg.Market.Type,
			Timeframes:        cfg.Market.Timeframes,
			BackfillFrom:      cfg.Market.BackfillFrom,
			GapcheckInterval:  cfg.Market.GapcheckInterval,
			HeartbeatInterval: cfg.Market.HeartbeatInterval,
			OnClosedCandle:    closedCandleObserver(log, evaluator, notifyUs),
			// Published on every heartbeat so /api/v1/status can say whether
			// the signal path is deciding. The api is a different process
			// and cannot see this any other way; the phase 07 audit found
			// that nothing could answer it at all.
			EvaluatorState: evaluatorProbe(cfg, evaluator),
		},
		log, marketDataRepo, collectorStatusRepo, candleUs, dataGapUs,
	)

	if evaluator != nil {
		// Warm-up before the stream opens, so the first live bar is decided on
		// converged values rather than being spent converging them.
		if err := evaluator.Warmup(ctx); err != nil {
			return err
		}
	}

	//==============================================================
	// # INGESTION
	//==============================================================
	log.Info("collector starting",
		"symbol", cfg.Market.Symbol,
		"market_type", cfg.Market.Type.String(),
		"timeframes", timeframeNames(cfg),
		"backfill_from", cfg.Market.BackfillFrom.Format("2006-01-02T15:04:05Z"),
		"gapcheck_interval", cfg.Market.GapcheckInterval.String(),
		"shutdown_timeout", constants.ShutdownTimeout.String(),
	)

	// Ingestion and delivery run together. Delivery is a separate loop from
	// the candle stream on purpose: a Firebase outage must never park the
	// goroutine that stores candles, and a queue that built up while the
	// process was down must drain without waiting for the next bar.
	group, groupCtx := errgroup.WithContext(ctx)
	group.Go(func() error {
		if err := marketUs.Run(groupCtx); err != nil {
			return fmt.Errorf("run ingestion: %w", err)
		}
		return nil
	})
	group.Go(func() error {
		if err := notifyUs.Run(groupCtx); err != nil {
			return fmt.Errorf("run delivery: %w", err)
		}
		return nil
	})
	group.Go(func() error {
		if err := outcomeUs.Run(groupCtx); err != nil {
			return fmt.Errorf("run outcome follower: %w", err)
		}
		return nil
	})

	if err := group.Wait(); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

// buildNotify constructs the delivery path for the configured mode.
//
// In silent mode nothing is built beyond the queue: no credentials are read,
// no token source is created, and there is no client that could send. Silence
// is the absence of a sender rather than a check performed by one.
func buildNotify(
	ctx context.Context,
	cfg *config.Config,
	log *slog.Logger,
	pool *pgxpool.Pool,
	signalUs _signal.SignalUsecase,
) (notify.NotifyUsecase, error) {
	notifyCfg := _notify_us.Config{
		Mode:    cfg.Notify.SignalMode,
		Signals: signalUs,
	}

	if cfg.Notify.Delivers() {
		// A way to look one up, not a device: the phone registers itself
		// through the api after the app is installed, and a collector that
		// refused to start without a registration could never reach the state
		// where one exists. Whether anything is registered is reported by
		// GET /api/v1/status, not enforced here. See ADR 0026.
		devices, err := _notify_us.NewDeviceUsecaseImpl(
			_notify_repo.NewDeviceRepoImpl(pool), log)
		if err != nil {
			return nil, err
		}
		notifyCfg.Devices = devices

		// At start-up, so a malformed VAPID key refuses to start rather than
		// surfacing on the first signal — which could be days later and would
		// look like the strategy being quiet.
		sender, err := webpush.NewSenderImpl(webpush.Options{
			PublicKey:  cfg.Notify.VAPIDPublicKey,
			PrivateKey: cfg.Notify.VAPIDPrivateKey,
			Subject:    cfg.Notify.VAPIDSubject,
			Logger:     log,
		})
		if err != nil {
			return nil, err
		}
		notifyCfg.Sender = sender
	}

	notifyUs, err := _notify_us.NewNotifyUsecaseImpl(
		_notify_repo.NewNotifyRepoImpl(pool), log, notifyCfg)
	if err != nil {
		return nil, err
	}

	log.Info("signal delivery",
		"mode", cfg.Notify.SignalMode.String(),
		"delivers", notifyUs.Delivers())
	return notifyUs, nil
}

// buildEvaluator constructs the live signal path, or nothing when no strategy
// is configured.
func buildEvaluator(
	cfg *config.Config,
	log *slog.Logger,
	candleUs candle.CandleUsecase,
	signalUs _signal.SignalUsecase,
) (_signal.SignalEvaluator, error) {
	if !cfg.Strategy.Enabled() {
		log.Info("no strategy is configured, so no signals will be evaluated",
			"hint", "set STRATEGY_NAME to one of "+strings.Join(_strategy_us.Names(), ", "))
		return nil, nil
	}

	// Live filtering needs an aligner that can follow a series forward
	// indefinitely, and the one that exists reads a bounded range for a
	// backtest. Refusing is the honest answer: running unfiltered while the
	// configuration asks for a filter would produce signals that the backtest
	// it is compared against would never have emitted.
	if cfg.Strategy.TrendFilter != "" {
		return nil, fmt.Errorf(
			"STRATEGY_TREND_FILTER is %q, and live trend filtering is not built yet. "+
				"Leave it empty to run unfiltered, which is a configuration the backtest "+
				"also supports (--no-trend-filter)", cfg.Strategy.TrendFilter)
	}

	if !slices.Contains(cfg.Market.Timeframes, cfg.Strategy.Timeframe) {
		return nil, fmt.Errorf(
			"STRATEGY_TIMEFRAME is %s but MARKET_TIMEFRAMES does not collect it, "+
				"so the bars it decides on would never arrive",
			cfg.Strategy.Timeframe)
	}

	entry, err := _strategy_us.Lookup(cfg.Strategy.Name)
	if err != nil {
		return nil, err
	}

	// The same construction path the backtest uses, so a live run and the
	// backtest that predicted it cannot be configured differently.
	strat, resolved, err := entry.BuildWith(
		cfg.Strategy.Params,
		roundTripCostPct(cfg),
		cfg.Market.Type == constants.MarketTypeSpot,
	)
	if err != nil {
		return nil, err
	}

	values, err := helper.ParamValues(resolved)
	if err != nil {
		return nil, err
	}

	evaluator, err := _signal_us.NewSignalEvaluatorImpl(
		_signal_us.EvaluatorConfig{
			Symbol:     cfg.Market.Symbol,
			MarketType: cfg.Market.Type,
			Timeframe:  cfg.Strategy.Timeframe,
			Strategy:   strat,
			Params:     values,
			Indicators: _indicator_us.DefaultSetConfig(),
		},
		log, candleUs, signalUs,
	)
	if err != nil {
		return nil, err
	}
	return evaluator, nil
}

// roundTripCostPct is what one entry and one exit cost, in percent.
//
// The same figure the backtest validates a configuration against, so a
// strategy that the backtest refused to build cannot be started live.
func roundTripCostPct(cfg *config.Config) float64 {
	taker, _ := cfg.Market.FeeTakerPct.Float64()
	return taker * 2
}

// closedCandleObserver hands each stored candle to the evaluator.
//
// # Why a failure here is logged and not returned
//
// The candle is the durable artefact and the signal is derived from it. A
// signal that could not be written is recoverable — the bar is stored and can
// be replayed — while a candle that was not written because a signal failed is
// a hole in the series that only the next gap scan will find. The priority is
// not close.
func closedCandleObserver(
	log *slog.Logger, evaluator _signal.SignalEvaluator, notifyUs notify.NotifyUsecase,
) func(context.Context, models.Candle) {
	if evaluator == nil {
		return nil
	}

	return func(ctx context.Context, bar models.Candle) {
		recorded, ok, err := evaluator.OnClosedCandle(ctx, bar)
		if err != nil {
			log.ErrorContext(ctx, "could not record a signal; the candle is stored",
				"error", err,
				"timeframe", bar.Timeframe.String(),
				"close_time", bar.CloseTime.UTC().Format(time.RFC3339))
			return
		}
		if !ok {
			return
		}

		log.InfoContext(ctx, "signal recorded",
			"id", recorded.Id.String(),
			"direction", recorded.Direction.String(),
			"signal_time", recorded.SignalTime.UTC().Format(time.RFC3339),
			"signal_price", recorded.SignalPrice.Decimal.String(),
			"strategy", recorded.StrategyName,
		)

		// Only after the signal is committed. A queue row is worth having only
		// because the signal behind it exists, and this ordering is what makes
		// "delivery failure must never cost a signal" true rather than
		// intended.
		queueForDelivery(ctx, log, notifyUs, recorded)
	}
}

// queueForDelivery offers a recorded signal to the delivery queue.
//
// A failure here is logged and dropped. The signal is already stored, and
// taking the collector down — or worse, unwinding a committed signal —
// because a second row could not be written would trade the artefact for the
// convenience.
func queueForDelivery(
	ctx context.Context, log *slog.Logger, notifyUs notify.NotifyUsecase, signal models.Signal,
) {
	queued, ok, err := notifyUs.QueueSignal(ctx, signal)
	switch {
	case err != nil:
		log.ErrorContext(ctx, "could not queue a signal for delivery; the signal is stored",
			"error", err, "signal_id", signal.Id.String())
	case ok:
		log.InfoContext(ctx, "signal queued for delivery",
			"notification_id", queued.Id, "signal_id", signal.Id.String())
	}
}

func timeframeNames(cfg *config.Config) []string {
	names := make([]string, 0, len(cfg.Market.Timeframes))
	for _, tf := range cfg.Market.Timeframes {
		names = append(names, tf.String())
	}
	return names
}

// evaluatorProbe reports what the live signal path is doing.
//
// Nothing configured is reported as nothing configured, not as "not ready": a
// pipeline that was never switched on and one stuck warming up produce
// identical silence, and only the second is a fault.
func evaluatorProbe(cfg *config.Config, evaluator _signal.SignalEvaluator) func() models.EvaluatorState {
	if evaluator == nil {
		return func() models.EvaluatorState { return models.EvaluatorState{} }
	}

	return func() models.EvaluatorState {
		ready, reason := evaluator.Ready()
		return models.EvaluatorState{
			Strategy:  cfg.Strategy.Name,
			Timeframe: cfg.Strategy.Timeframe,
			Ready:     ready,
			Reason:    reason,
		}
	}
}
