// Package routes maps HTTP paths onto handlers.
//
// It knows the handler interfaces and nothing about their implementations,
// so adding an endpoint never drags a repository or a database driver into
// the routing layer.
package routes

import (
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/helper"
	"github.com/spioneracorei8/btcusd-trading-platform/server/middleware"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/candle"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/health"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/indicator"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/market"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/notify"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/outcome"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/pipeline"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/signal"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/stream"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/web"
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

// RegisterMarketHandler mounts the market data status endpoint.
//
// It sits under /internal because it exposes operational detail rather than
// anything a client should depend on. There is no auth yet: the api listens on
// loopback behind the VPS firewall.
func (r *route) RegisterMarketHandler(handler market.MarketHandler) {
	r.router.Get("/internal/market/status", handler.Status)
}

// RegisterOutcomeHandler mounts the reconciliation endpoint.
//
// Internal for the same reason as the market status, and for one more: it is
// expensive. The backtest half replays history, so this is a page somebody
// opens deliberately rather than something polled.
func (r *route) RegisterOutcomeHandler(handler outcome.OutcomeHandler) {
	r.router.Get("/internal/signals/reconciliation", handler.Reconciliation)
}

// RegisterAPI mounts everything the mobile app consumes.
//
// # Why the path is versioned
//
// A deployed phone cannot be redeployed with the server. Phase 09 is written
// against this shape, and the first time it needs to change, an app in
// somebody's pocket will still be asking for the old one.
//
// # Why there is no authentication
//
// The network is the boundary: the api listens on loopback and the tailnet
// and is unreachable from the public internet. See ADR 0024, which also
// records what would have to change before that stopped being true.
func (r *route) RegisterAPI(api APIHandlers) {
	r.router.Route("/api/"+constants.APIVersion, func(v chi.Router) {
		v.Get("/candles", api.Candles.Candles)
		v.Get("/indicators", api.Indicators.Indicators)

		v.Get("/signals", api.Signals.Signals)
		v.Get("/signals/{id}", api.Signals.Signal)

		v.Get("/outcomes", api.Outcomes.Outcomes)
		v.Get("/performance", api.Outcomes.Performance)

		v.Get("/status", api.Status.Status)
		v.Get("/stream", api.Stream.Stream)

		// The phone registers itself here. The token comes from FCM on the
		// device and is rotated without asking, so it cannot be configuration
		// — see ADR 0026.
		v.Post("/device", api.Devices.RegisterDevice)
		v.Get("/device", api.Devices.Device)
		v.Delete("/device", api.Devices.ForgetDevice)

		// An unknown path under /api/v1 is an error in JSON, not the app.
		//
		// Without this, a chi sub-router inherits the parent's NotFound — and
		// the parent's is the web app, which answers a mistyped endpoint with
		// the entry document and a 200. A client then parses HTML as JSON and
		// reports something that has nothing to do with the mistake.
		v.NotFound(func(w http.ResponseWriter, r *http.Request) {
			helper.WriteAPIError(w, nil, http.StatusNotFound, constants.APIErrNotFound,
				fmt.Sprintf("no endpoint at %s %s", r.Method, r.URL.Path))
		})
		v.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
			helper.WriteAPIError(w, nil, http.StatusMethodNotAllowed,
				constants.APIErrNotFound,
				fmt.Sprintf("%s is not allowed on %s", r.Method, r.URL.Path))
		})
	})
}

// RegisterApp mounts the built web app underneath everything else.
//
// # Why the app is served by this process
//
// So that it and the API share an origin. A page on one origin talking to an
// API on another needs CORS on every endpoint and an origin allowlist on the
// websocket, and when a preflight fails the reflex is to widen the allowlist
// until it stops failing. Same-origin has none of those decisions in it, and
// the websocket's origin check then holds with nothing configured — see
// ADR 0024 and the stream handler.
//
// It is a NotFound handler rather than a route because the app owns every path
// the API has not claimed, including ones that were never exported: /signals/
// {id} is a screen, and a notification tap cold-loads it.
func (r *route) RegisterApp(handler web.AppHandler) {
	r.router.NotFound(handler.App)
}

// APIHandlers is everything the versioned API needs.
//
// Passed as a struct rather than eight arguments so adding an endpoint does
// not silently reorder the existing ones at every call site.
type APIHandlers struct {
	Candles    candle.CandleHandler
	Indicators indicator.IndicatorHandler
	Signals    signal.SignalHandler
	Outcomes   outcome.OutcomeHandler
	Status     pipeline.StatusHandler
	Stream     stream.StreamHandler
	Devices    notify.DeviceHandler
}
