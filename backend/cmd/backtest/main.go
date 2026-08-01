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
	"fmt"
	"log/slog"
	"os"

	"github.com/spioneracorei8/btcusd-trading-platform/backend/internal/config"
	"github.com/spioneracorei8/btcusd-trading-platform/backend/internal/logging"
)

func main() {
	if err := run(); err != nil {
		slog.Error("backtest stopped with error", "error", err)
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

	logger.Info("backtest configured",
		"env", cfg.App.Env.String(),
		"symbol", cfg.Market.Symbol,
		"market_type", cfg.Market.Type.String(),
		"fee_taker_pct", cfg.Market.FeeTakerPct.String(),
		"slippage_ticks", cfg.Market.SlippageTicks,
	)
	logger.Info("backtest engine is not implemented in phase 01, exiting; it arrives in phase 04")
	return nil
}
