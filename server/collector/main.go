// Command collector will ingest Binance market data into TimescaleDB.
//
// Phase 01 scope: this binary only loads and reports its configuration so the
// container image, the compose service and the config wiring can be verified.
// Connecting to Binance (WebSocket, REST backfill, gap detection) is phase 02
// and is deliberately not implemented here yet.
package main

import (
	"log/slog"
	"os"

	"github.com/spioneracorei8/btcusd-trading-platform/server/config"
	"github.com/spioneracorei8/btcusd-trading-platform/server/logger"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("invalid configuration", "error", err)
		os.Exit(1)
	}

	log := logger.New(os.Stdout, logger.Options{
		Level:  cfg.App.LogLevel,
		Format: logger.FormatForEnv(cfg.App.Env),
	})
	slog.SetDefault(log)

	timeframes := make([]string, 0, len(cfg.Market.Timeframes))
	for _, tf := range cfg.Market.Timeframes {
		timeframes = append(timeframes, tf.String())
	}

	log.Info("collector configured",
		"env", cfg.App.Env.String(),
		"symbol", cfg.Market.Symbol,
		"market_type", cfg.Market.Type.String(),
		"timeframes", timeframes,
	)
	log.Info("collector has no work in phase 01, exiting; market data ingestion arrives in phase 02")
}
