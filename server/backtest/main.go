// Command backtest will replay stored candles through a strategy and report
// its performance net of fees and slippage.
//
// Phase 01 scope: this binary only loads and reports its configuration,
// including the trading costs every future report must be net of. The
// backtest engine itself is phase 04 and is deliberately not implemented here
// yet — it comes before any strategy on purpose, because a strategy written
// without a measuring instrument cannot be judged.
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

	log.Info("backtest configured",
		"env", cfg.App.Env.String(),
		"symbol", cfg.Market.Symbol,
		"market_type", cfg.Market.Type.String(),
		"fee_taker_pct", cfg.Market.FeeTakerPct.String(),
		"slippage_ticks", cfg.Market.SlippageTicks,
	)
	log.Info("backtest engine is not implemented in phase 01, exiting; it arrives in phase 04")
}
