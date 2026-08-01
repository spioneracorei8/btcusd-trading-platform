// Command collector will ingest Binance market data into TimescaleDB.
//
// Phase 01 scope: this binary only loads and reports its configuration so the
// container image, the compose service and the config wiring can be verified.
// Connecting to Binance (WebSocket, REST backfill, gap detection) is phase 02
// and is deliberately not implemented here yet.
package main

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/spioneracorei8/btcusd-trading-platform/backend/internal/config"
	"github.com/spioneracorei8/btcusd-trading-platform/backend/internal/logging"
)

func main() {
	if err := run(); err != nil {
		slog.Error("collector stopped with error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		// main logs this; every missing or invalid variable is named in err.
		return fmt.Errorf("load config: %w", err)
	}

	logger := logging.New(os.Stdout, logging.Options{
		Level:  cfg.App.LogLevel,
		Format: logging.FormatForEnv(cfg.App.Env.String()),
	})
	slog.SetDefault(logger)

	timeframes := make([]string, 0, len(cfg.Market.Timeframes))
	for _, tf := range cfg.Market.Timeframes {
		timeframes = append(timeframes, tf.String())
	}

	logger.Info("collector configured",
		"env", cfg.App.Env.String(),
		"symbol", cfg.Market.Symbol,
		"market_type", cfg.Market.Type.String(),
		"timeframes", timeframes,
	)
	logger.Info("collector has no work in phase 01, exiting; market data ingestion arrives in phase 02")
	return nil
}
