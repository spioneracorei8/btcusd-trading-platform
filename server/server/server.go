// Package server bootstraps the API process: it opens the database pool,
// wires repositories into usecases into handlers, and runs the HTTP server
// until a shutdown signal arrives.
//
// This is the only place that knows which implementation satisfies which
// interface. Every other package depends on interfaces alone.
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/go-chi/chi/v5"
	"github.com/shopspring/decimal"

	"github.com/spioneracorei8/btcusd-trading-platform/server/config"
	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/database"
	"github.com/spioneracorei8/btcusd-trading-platform/server/middleware"
	"github.com/spioneracorei8/btcusd-trading-platform/server/routes"

	_backtest_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/backtest/usecase"
	_candle_handler "github.com/spioneracorei8/btcusd-trading-platform/server/services/candle/handler"
	_candle_repo "github.com/spioneracorei8/btcusd-trading-platform/server/services/candle/repository"
	_candle_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/candle/usecase"
	_datagap_repo "github.com/spioneracorei8/btcusd-trading-platform/server/services/datagap/repository"
	_datagap_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/datagap/usecase"
	_health_handler "github.com/spioneracorei8/btcusd-trading-platform/server/services/health/handler"
	_health_repo "github.com/spioneracorei8/btcusd-trading-platform/server/services/health/repository"
	_health_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/health/usecase"
	_indicator_handler "github.com/spioneracorei8/btcusd-trading-platform/server/services/indicator/handler"
	_indicator_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/indicator/usecase"
	_market_handler "github.com/spioneracorei8/btcusd-trading-platform/server/services/market/handler"
	_market_repo "github.com/spioneracorei8/btcusd-trading-platform/server/services/market/repository"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/market/repository/binance"
	_market_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/market/usecase"
	_notify_handler "github.com/spioneracorei8/btcusd-trading-platform/server/services/notify/handler"
	_notify_repo "github.com/spioneracorei8/btcusd-trading-platform/server/services/notify/repository"
	_notify_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/notify/usecase"
	_outcome_handler "github.com/spioneracorei8/btcusd-trading-platform/server/services/outcome/handler"
	_outcome_repo "github.com/spioneracorei8/btcusd-trading-platform/server/services/outcome/repository"
	_outcome_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/outcome/usecase"
	_pipeline_handler "github.com/spioneracorei8/btcusd-trading-platform/server/services/pipeline/handler"
	_pipeline_repo "github.com/spioneracorei8/btcusd-trading-platform/server/services/pipeline/repository"
	_pipeline_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/pipeline/usecase"
	_signal_handler "github.com/spioneracorei8/btcusd-trading-platform/server/services/signal/handler"
	_signal_repo "github.com/spioneracorei8/btcusd-trading-platform/server/services/signal/repository"
	_signal_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/signal/usecase"
	_stream_handler "github.com/spioneracorei8/btcusd-trading-platform/server/services/stream/handler"
	_stream_repo "github.com/spioneracorei8/btcusd-trading-platform/server/services/stream/repository"
	_stream_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/stream/usecase"
	_web_handler "github.com/spioneracorei8/btcusd-trading-platform/server/services/web/handler"
)

// Server holds everything the API process needs to start.
type Server struct {
	Config *config.Config
	Logger *slog.Logger
}

// Start runs the API until SIGINT or SIGTERM, then shuts down gracefully.
func (s *Server) Start() error {
	// ctx is cancelled on SIGTERM/SIGINT; every goroutine below observes it.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := database.NewPool(ctx, database.PoolOptions{
		DSN:            s.Config.Database.URL,
		MaxConns:       s.Config.Database.MaxConns,
		ConnectTimeout: s.Config.Database.ConnectTimeout,
	})
	if err != nil {
		return err // database.NewPool already names the operation
	}
	defer pool.Close()

	var (
		router = chi.NewRouter()
		middl  = middleware.InitMiddleware(s.Logger)
	)

	//==============================================================
	// # REPOSITORIES
	//==============================================================
	healthRepo := _health_repo.NewHealthRepoImpl(pool)
	candleRepo := _candle_repo.NewCandleRepoImpl(pool)
	dataGapRepo := _datagap_repo.NewDataGapRepoImpl(pool)
	collectorStatusRepo := _market_repo.NewCollectorStatusRepoImpl(pool)

	// The api reads market data status but never ingests: only the collector
	// opens a stream. This client exists so the usecase is complete, and its
	// REST half is unused here.
	marketDataRepo := binance.NewMarketDataRepoImpl(binance.Options{
		RESTBaseURL: s.Config.Market.RESTBaseURL,
		WSBaseURL:   s.Config.Market.WSBaseURL,
	})

	//==============================================================
	// # USECASES
	//==============================================================
	healthUs := _health_us.NewHealthUsecaseImpl(healthRepo)
	candleUs := _candle_us.NewCandleUsecaseImpl(candleRepo)
	dataGapUs := _datagap_us.NewDataGapUsecaseImpl(dataGapRepo)
	marketUs := _market_us.NewMarketUsecaseImpl(
		_market_us.Config{
			Symbol:            s.Config.Market.Symbol,
			MarketType:        s.Config.Market.Type,
			Timeframes:        s.Config.Market.Timeframes,
			BackfillFrom:      s.Config.Market.BackfillFrom,
			GapcheckInterval:  s.Config.Market.GapcheckInterval,
			HeartbeatInterval: s.Config.Market.HeartbeatInterval,
		},
		s.Logger, marketDataRepo, collectorStatusRepo, candleUs, dataGapUs,
	)

	signalUs := _signal_us.NewSignalUsecaseImpl(_signal_repo.NewSignalRepoImpl(pool))

	// The engine is what makes the reconciliation a comparison rather than a
	// report: it replays the same strategy and parameters over the same
	// period the live signals came from.
	engine := _backtest_us.NewBacktestUsecaseImpl(
		s.Logger, candleUs, dataGapUs, _indicator_us.DefaultSetConfig(),
	)

	outcomeUs, err := _outcome_us.NewOutcomeUsecaseImpl(
		_outcome_repo.NewOutcomeRepoImpl(pool), s.Logger, signalUs, candleUs, dataGapUs,
		_outcome_us.Config{
			Symbol:     s.Config.Market.Symbol,
			MarketType: s.Config.Market.Type,
			Costs:      s.Config.BacktestCosts(),
			ExpiryBars: s.Config.Outcome.ExpiryBars,
			Interval:   s.Config.Outcome.Interval,
		},
	)
	if err != nil {
		return err
	}

	pipelineUs, err := _pipeline_us.NewPipelineUsecaseImpl(
		_pipeline_repo.NewPipelineRepoImpl(pool), marketUs,
		_pipeline_us.Config{
			Symbol:            s.Config.Market.Symbol,
			MarketType:        s.Config.Market.Type,
			SignalMode:        s.Config.Notify.SignalMode,
			HeartbeatInterval: s.Config.Market.HeartbeatInterval,
		},
	)
	if err != nil {
		return err
	}

	reconcileUs, err := _outcome_us.NewReconcileUsecaseImpl(
		_outcome_repo.NewReconcileRepoImpl(pool), s.Logger,
		_outcome_us.ReconcileConfig{
			Backtest: _outcome_us.EngineComparer{
				Engine:     engine,
				Timeframe:  s.Config.Strategy.Timeframe,
				Costs:      s.Config.BacktestCosts(),
				Equity:     decimal.RequireFromString(constants.DefaultInitialEquity),
				MarketType: s.Config.Market.Type,
			},
		},
	)
	if err != nil {
		return err
	}

	//==============================================================
	// # HANDLERS
	//==============================================================
	healthHandler := _health_handler.NewHealthHandlerImpl(healthUs, s.Logger)
	marketHandler := _market_handler.NewMarketHandlerImpl(marketUs, s.Logger)
	outcomeHandler := _outcome_handler.NewOutcomeHandlerImpl(
		reconcileUs, outcomeUs, s.Logger, s.Config.Market.Symbol, s.Config.Market.Type)

	// The live push side. Its market feed is read-only by construction: the
	// feed type holds a market data client and nothing else, so there is
	// nothing there that could store a forming bar even by mistake.
	hub := _stream_us.NewHub(s.Logger)
	// The api registers devices and never delivers; the collector delivers and
	// never registers. Each gets the interface it uses and not the other.
	deviceUs, err := _notify_us.NewDeviceUsecaseImpl(
		_notify_repo.NewDeviceRepoImpl(pool), s.Logger)
	if err != nil {
		return fmt.Errorf("build the device usecase: %w", err)
	}

	apiHandlers := routes.APIHandlers{
		Candles: _candle_handler.NewCandleHandlerImpl(
			candleUs, s.Logger, s.Config.Market.Symbol, s.Config.Market.Type,
			s.Config.Market.Timeframes),
		Indicators: _indicator_handler.NewIndicatorHandlerImpl(
			candleUs, _indicator_us.DefaultSetConfig(), s.Logger,
			s.Config.Market.Symbol, s.Config.Market.Type, s.Config.Market.Timeframes),
		Signals: _signal_handler.NewSignalHandlerImpl(
			signalUs, s.Logger, s.Config.Market.Symbol, s.Config.Market.Type),
		Outcomes: outcomeHandler,
		Status:   _pipeline_handler.NewStatusHandlerImpl(pipelineUs, s.Logger),
		Stream: _stream_handler.NewStreamHandlerImpl(
			hub, s.Logger, s.Config.App.StreamOrigins),
		Devices: _notify_handler.NewDeviceHandlerImpl(
			deviceUs, s.Logger, s.Config.Notify.SignalMode,
			s.Config.Notify.VAPIDPublicKey),
	}

	//==============================================================
	// # API
	//==============================================================
	route := routes.NewRoute(router, middl)
	route.RegisterHealthHandler(healthHandler)
	route.RegisterMarketHandler(marketHandler)
	route.RegisterOutcomeHandler(outcomeHandler)
	route.RegisterAPI(apiHandlers)

	// The app, underneath everything above. Mounted last because it claims
	// every path the API did not, and only when there is something to serve:
	// with WEB_ROOT unset this process is the API alone, which is what the
	// collector and every test run want.
	if s.Config.App.WebRoot != "" {
		route.RegisterApp(_web_handler.NewAppHandlerImpl(s.Config.App.WebRoot, s.Logger))
		s.Logger.Info("serving the web app", "root", s.Config.App.WebRoot)
	}

	// The sources that feed the stream. They run for the life of the process
	// and stop with it; a failure in one is logged and does not take the API
	// down, because a chart that stops updating must not also stop /status
	// from answering why.
	streamCtx, stopStream := context.WithCancel(ctx)
	defer stopStream()
	go hub.Run(streamCtx,
		_stream_us.CandleFeed{
			Source:     _stream_repo.NewCandleFeedImpl(marketDataRepo, s.Logger),
			Symbol:     s.Config.Market.Symbol,
			MarketType: s.Config.Market.Type,
			Timeframes: s.Config.Market.Timeframes,
		},
		&_stream_us.SignalPoller{
			Signals: signalUs,
			Symbol:  s.Config.Market.Symbol, MarketType: s.Config.Market.Type,
		},
		&_stream_us.OutcomePoller{
			Outcomes: outcomeUs,
			Symbol:   s.Config.Market.Symbol, MarketType: s.Config.Market.Type,
		},
		_stream_us.StatusTicker{Pipeline: pipelineUs},
	)

	// A database that is not up yet must not stop the API from starting:
	// /ready is what reports the truth about it.
	pingCtx, cancelPing := context.WithTimeout(ctx, s.Config.Database.ConnectTimeout)
	if err := healthRepo.PingDatabase(pingCtx); err != nil {
		s.Logger.Warn("database not reachable at startup, serving anyway", "error", err)
	} else {
		s.Logger.Info("database connection established")
	}
	cancelPing()

	return s.listen(ctx, router)
}

// listen runs the HTTP server and performs the graceful shutdown.
func (s *Server) listen(ctx context.Context, handler http.Handler) error {
	httpServer := &http.Server{
		Addr:              s.Config.HTTPAddr(),
		Handler:           handler,
		ReadHeaderTimeout: constants.HTTPReadHeaderTimeout,
		ReadTimeout:       constants.HTTPReadTimeout,
		WriteTimeout:      constants.HTTPWriteTimeout,
		IdleTimeout:       constants.HTTPIdleTimeout,
		ErrorLog:          slog.NewLogLogger(s.Logger.Handler(), slog.LevelError),
	}

	serveErr := make(chan error, 1)
	go func() {
		s.Logger.Info("api listening",
			"addr", httpServer.Addr,
			"env", s.Config.App.Env.String(),
			"symbol", s.Config.Market.Symbol,
			"market_type", s.Config.Market.Type.String(),
		)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
			return
		}
		serveErr <- nil
	}()

	select {
	case err := <-serveErr:
		if err != nil {
			return fmt.Errorf("http server: %w", err)
		}
		return nil
	case <-ctx.Done():
		s.Logger.Info("shutdown signal received", "timeout", constants.ShutdownTimeout.String())
	}

	// Detach from the cancelled signal context so shutdown gets its full grace
	// period instead of being cancelled immediately.
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), constants.ShutdownTimeout)
	defer cancel()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}

	s.Logger.Info("api stopped cleanly")
	return nil
}
