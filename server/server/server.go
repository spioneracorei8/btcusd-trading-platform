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

	"github.com/spioneracorei8/btcusd-trading-platform/server/config"
	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/database"
	"github.com/spioneracorei8/btcusd-trading-platform/server/middleware"
	"github.com/spioneracorei8/btcusd-trading-platform/server/routes"

	_health_handler "github.com/spioneracorei8/btcusd-trading-platform/server/services/health/handler"
	_health_repo "github.com/spioneracorei8/btcusd-trading-platform/server/services/health/repository"
	_health_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/health/usecase"
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
		return fmt.Errorf("create database pool: %w", err)
	}
	defer pool.Close()

	var (
		router = chi.NewRouter()
		middl  = middleware.InitMiddleware(s.Logger)
	)

	//==============================================================
	// # REPOSITORIES
	//==============================================================
	// The API serves health only in phase 01. The candle, signal and data gap
	// repositories exist and are tested, but nothing here consumes them yet:
	// the collector wires them in phase 02. Constructing them now and throwing
	// the result away would be dead code, not architecture.
	healthRepo := _health_repo.NewHealthRepoImpl(pool)

	//==============================================================
	// # USECASES
	//==============================================================
	healthUs := _health_us.NewHealthUsecaseImpl(healthRepo)

	//==============================================================
	// # HANDLERS
	//==============================================================
	healthHandler := _health_handler.NewHealthHandlerImpl(healthUs, s.Logger)

	//==============================================================
	// # API
	//==============================================================
	route := routes.NewRoute(router, middl)
	route.RegisterHealthHandler(healthHandler)

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
