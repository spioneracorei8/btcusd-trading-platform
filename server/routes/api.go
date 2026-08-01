// Package routes maps HTTP paths onto handlers.
//
// It knows the handler interfaces and nothing about their implementations,
// so adding an endpoint never drags a repository or a database driver into
// the routing layer.
package routes

import (
	"github.com/go-chi/chi/v5"

	"github.com/spioneracorei8/btcusd-trading-platform/server/middleware"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/health"
)

type route struct {
	router     chi.Router
	middleware middleware.MyMiddleware
}

// NewRoute builds the router wrapper and installs the shared middleware.
func NewRoute(router chi.Router, middl middleware.MyMiddleware) *route {
	router.Use(middl.RequestID)
	router.Use(middl.Recoverer)
	router.Use(middl.RequestLogger)

	return &route{
		router:     router,
		middleware: middl,
	}
}

// RegisterHealthHandler mounts the liveness and readiness endpoints.
func (r *route) RegisterHealthHandler(handler health.HealthHandler) {
	r.router.Get("/health", handler.Liveness)
	r.router.Get("/ready", handler.Readiness)
}
