// Command api serves the REST API of the BTC/USDT analysis platform.
//
// The platform never places orders: it observes the market, produces signals
// with reasons, and notifies the owner. This binary exposes only read and
// health endpoints.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spioneracorei8/btcusd-trading-platform/backend/internal/config"
	"github.com/spioneracorei8/btcusd-trading-platform/backend/internal/logging"
	"github.com/spioneracorei8/btcusd-trading-platform/backend/internal/storage"
)

// shutdownTimeout bounds how long in-flight requests may finish after a
// SIGTERM or SIGINT before the process exits anyway.
const shutdownTimeout = 10 * time.Second

func main() {
	if err := run(); err != nil {
		slog.Error("api stopped with error", "error", err)
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

	// ctx is cancelled on SIGTERM/SIGINT; every goroutine below observes it.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := storage.NewPool(ctx, storage.PoolOptions{
		DSN:            cfg.Database.URL,
		MaxConns:       cfg.Database.MaxConns,
		ConnectTimeout: cfg.Database.ConnectTimeout,
	})
	if err != nil {
		return fmt.Errorf("create database pool: %w", err)
	}
	defer pool.Close()

	store := storage.New(pool)

	// A database that is not up yet must not stop the API from starting:
	// /ready is what reports the truth about it.
	pingCtx, cancelPing := context.WithTimeout(ctx, cfg.Database.ConnectTimeout)
	if err := store.Ping(pingCtx); err != nil {
		logger.Warn("database not reachable at startup, serving anyway", "error", err)
	} else {
		logger.Info("database connection established")
	}
	cancelPing()

	srv := &http.Server{
		Addr:              cfg.HTTPAddr(),
		Handler:           newRouter(cfg, logger, store),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}

	serveErr := make(chan error, 1)
	go func() {
		logger.Info("api listening",
			"addr", srv.Addr,
			"env", cfg.App.Env.String(),
			"symbol", cfg.Market.Symbol,
			"market_type", cfg.Market.Type.String(),
		)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
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
		logger.Info("shutdown signal received", "timeout", shutdownTimeout.String())
	}

	// Detach from the cancelled signal context so shutdown gets its full grace
	// period instead of being cancelled immediately.
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}

	logger.Info("api stopped cleanly")
	return nil
}
