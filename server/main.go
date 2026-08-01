// Command server runs the REST API of the BTC/USDT analysis platform.
//
// The platform never places orders: it observes the market, produces signals
// with reasons, and notifies the owner. This binary exposes only read and
// health endpoints.
package main

import (
	"log/slog"
	"os"

	"github.com/spioneracorei8/btcusd-trading-platform/server/config"
	"github.com/spioneracorei8/btcusd-trading-platform/server/logger"
	"github.com/spioneracorei8/btcusd-trading-platform/server/server"
)

// getMainServer builds the API server from the validated configuration.
func getMainServer(cfg *config.Config, log *slog.Logger) *server.Server {
	return &server.Server{
		Config: cfg,
		Logger: log,
	}
}

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

	s := getMainServer(cfg, log)
	if err := s.Start(); err != nil {
		log.Error("api stopped with error", "error", err)
		os.Exit(1)
	}
}
