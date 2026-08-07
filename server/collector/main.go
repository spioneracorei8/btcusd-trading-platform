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
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/spioneracorei8/btcusd-trading-platform/server/config"
	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/database"
	"github.com/spioneracorei8/btcusd-trading-platform/server/logger"

	_candle_repo "github.com/spioneracorei8/btcusd-trading-platform/server/services/candle/repository"
	_candle_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/candle/usecase"
	_datagap_repo "github.com/spioneracorei8/btcusd-trading-platform/server/services/datagap/repository"
	_datagap_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/datagap/usecase"
	_market_repo "github.com/spioneracorei8/btcusd-trading-platform/server/services/market/repository"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/market/repository/binance"
	_market_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/market/usecase"
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
		return fmt.Errorf("create database pool: %w", err)
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
	marketUs := _market_us.NewMarketUsecaseImpl(
		_market_us.Config{
			Symbol:            cfg.Market.Symbol,
			MarketType:        cfg.Market.Type,
			Timeframes:        cfg.Market.Timeframes,
			BackfillFrom:      cfg.Market.BackfillFrom,
			GapcheckInterval:  cfg.Market.GapcheckInterval,
			HeartbeatInterval: cfg.Market.HeartbeatInterval,
		},
		log, marketDataRepo, collectorStatusRepo, candleUs, dataGapUs,
	)

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

	if err := marketUs.Run(ctx); err != nil {
		return fmt.Errorf("run ingestion: %w", err)
	}
	return nil
}

func timeframeNames(cfg *config.Config) []string {
	names := make([]string, 0, len(cfg.Market.Timeframes))
	for _, tf := range cfg.Market.Timeframes {
		names = append(names, tf.String())
	}
	return names
}
