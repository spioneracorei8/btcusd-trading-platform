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
//
// Order matters. RequestLogger must sit outside Recoverer: it writes its
// record after the inner handler returns, so if Recoverer were the outer one
// a panicking request would unwind past the logger and never be logged at
// all. With this order the recovered 500 is still counted and logged like any
// other response.
func NewRoute(router chi.Router, middl middleware.MyMiddleware) *route {
	router.Use(middl.RequestID)
	router.Use(middl.RequestLogger)
	router.Use(middl.Recoverer)

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
