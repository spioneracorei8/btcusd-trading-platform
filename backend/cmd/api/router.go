package main

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/spioneracorei8/btcusd-trading-platform/backend/internal/config"
	"github.com/spioneracorei8/btcusd-trading-platform/backend/internal/storage"
)

// pinger is the only thing the handlers need from storage. Keeping it an
// interface lets the readiness handler be tested without a database.
type pinger interface {
	Ping(ctx context.Context) error
}

// api holds everything the handlers need.
type api struct {
	cfg    *config.Config
	logger *slog.Logger
	store  pinger
}

// Compile-time check that the real store satisfies the handler's needs.
var _ pinger = (*storage.Store)(nil)

// newRouter wires the HTTP routes and middleware.
func newRouter(cfg *config.Config, logger *slog.Logger, store pinger) http.Handler {
	a := &api{cfg: cfg, logger: logger, store: store}

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(recoverer(logger))
	r.Use(requestLogger(logger))

	r.Get("/health", a.handleHealth)
	r.Get("/ready", a.handleReady)

	return r
}
