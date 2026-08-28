package routes_test

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/spioneracorei8/btcusd-trading-platform/server/middleware"
	"github.com/spioneracorei8/btcusd-trading-platform/server/routes"
)

// newRouterWithLog builds the real middleware stack over a capturing logger.
func newRouterWithLog(t *testing.T) (chi.Router, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))

	router := chi.NewRouter()
	_ = routes.NewRoute(router, middleware.InitMiddleware(log))
	return router, &buf
}

// TestPanicIsStillAccessLogged pins the middleware order down.
//
// RequestLogger writes its record after the inner handler returns, so if
// Recoverer were registered outside it, a panicking request would unwind past
// the logger and vanish from the access log — exactly the request an operator
// most needs to see.
func TestPanicIsStillAccessLogged(t *testing.T) {
	router, logs := newRouterWithLog(t)
	router.Get("/boom", func(http.ResponseWriter, *http.Request) { panic("boom") })

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/boom", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}

	got := logs.String()
	if !strings.Contains(got, `msg="http request"`) {
		t.Errorf("a panicking request produced no access-log record:\n%s", got)
	}
	if !strings.Contains(got, "status=500") {
		t.Errorf("the access-log record does not report status 500:\n%s", got)
	}
	if !strings.Contains(got, `msg="panic recovered in handler"`) {
		t.Errorf("the panic itself was not logged:\n%s", got)
	}
}

// TestRequestIsAccessLoggedWithRequestID covers the ordinary path: one record
// per request carrying method, path, status, duration and request id.
func TestRequestIsAccessLoggedWithRequestID(t *testing.T) {
	router, logs := newRouterWithLog(t)
	router.Get("/ok", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ok", nil))

	got := logs.String()
	for _, want := range []string{
		`msg="http request"`, "method=GET", "path=/ok", "status=200", "duration=", "request_id=",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("access log is missing %q:\n%s", want, got)
		}
	}
}

// TestSilentHandlerIsLoggedAsOK covers the handler that returns without
// writing anything: net/http still sends 200, so the access log must say 200
// rather than the wrapper's untouched zero.
func TestSilentHandlerIsLoggedAsOK(t *testing.T) {
	router, logs := newRouterWithLog(t)
	router.Get("/silent", func(http.ResponseWriter, *http.Request) {})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/silent", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("client received %d, want %d", rec.Code, http.StatusOK)
	}
	got := logs.String()
	if strings.Contains(got, "status=0") {
		t.Errorf("access log reports status=0 while the client received 200:\n%s", got)
	}
	if !strings.Contains(got, "status=200") {
		t.Errorf("access log does not report status 200:\n%s", got)
	}
}

// stub answers every handler method with a marker, so a test can tell which
// route a request reached rather than asserting on a real response body.
type stub struct{ mark string }

func (s stub) write(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"reached":"` + s.mark + `"}`))
}

func (s stub) Candles(w http.ResponseWriter, _ *http.Request)        { s.write(w) }
func (s stub) Indicators(w http.ResponseWriter, _ *http.Request)     { s.write(w) }
func (s stub) Signals(w http.ResponseWriter, _ *http.Request)        { s.write(w) }
func (s stub) Signal(w http.ResponseWriter, _ *http.Request)         { s.write(w) }
func (s stub) Outcomes(w http.ResponseWriter, _ *http.Request)       { s.write(w) }
func (s stub) Performance(w http.ResponseWriter, _ *http.Request)    { s.write(w) }
func (s stub) Reconciliation(w http.ResponseWriter, _ *http.Request) { s.write(w) }
func (s stub) Status(w http.ResponseWriter, _ *http.Request)         { s.write(w) }
func (s stub) Stream(w http.ResponseWriter, _ *http.Request)         { s.write(w) }
func (s stub) RegisterDevice(w http.ResponseWriter, _ *http.Request) { s.write(w) }
func (s stub) Device(w http.ResponseWriter, _ *http.Request)         { s.write(w) }
func (s stub) ForgetDevice(w http.ResponseWriter, _ *http.Request)   { s.write(w) }

// app stands in for the built web app: it answers everything with the entry
// document, exactly as the real handler does for a path it does not recognise.
type app struct{}

func (app) App(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("<!doctype html><title>app</title>"))
}

// routerServingBoth wires the API and the app onto one router, in the order
// server.go does it: the API first, the app underneath as the catch-all.
func routerServingBoth(t *testing.T) chi.Router {
	t.Helper()

	router := chi.NewRouter()
	log := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	route := routes.NewRoute(router, middleware.InitMiddleware(log))

	s := stub{mark: "api"}
	route.RegisterAPI(routes.APIHandlers{
		Candles: s, Indicators: s, Signals: s, Outcomes: s,
		Status: s, Stream: s, Devices: s,
	})
	route.RegisterApp(app{})

	return router
}

/*
TestAMistypedEndpointIsJSONAndNotTheApp.

# What this prevents

The app is mounted as the router's NotFound handler, because it owns every path
the API did not claim — /signals/{id} is a screen and a notification tap
cold-loads it.

chi hands a sub-router the parent's NotFound unless it sets its own, so without
the one in RegisterAPI a mistyped endpoint under /api/v1 would answer with the
entry document and a 200. The client then parses HTML as JSON and reports a
syntax error that has nothing to do with the mistake.
*/
func TestAMistypedEndpointIsJSONAndNotTheApp(t *testing.T) {
	router := routerServingBoth(t)

	for _, path := range []string{
		"/api/v1/candle", // singular, a plausible typo
		"/api/v1/nonsense",
		"/api/v1/signals/3f2504e0/extra",
	} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))

		if rec.Code != http.StatusNotFound {
			t.Errorf("%s answered %d; want 404", path, rec.Code)
		}
		if got := rec.Body.String(); !strings.Contains(got, `"not_found"`) {
			t.Errorf("%s answered %q; want a JSON error", path, got)
		}
		if got := rec.Header().Get("Content-Type"); !strings.Contains(got, "application/json") {
			t.Errorf("%s answered with Content-Type %q", path, got)
		}
	}
}

/*
TestTheWrongMethodOnARealEndpointSaysSo.

/device is the one endpoint with more than one method, and a GET to a
POST-only path answering with the app would be the same class of confusion as
the case above.
*/
func TestTheWrongMethodOnARealEndpointSaysSo(t *testing.T) {
	router := routerServingBoth(t)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/api/v1/candles", nil))

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("PUT /api/v1/candles answered %d; want 405", rec.Code)
	}
	if got := rec.Body.String(); !strings.Contains(got, "not allowed") {
		t.Fatalf("answered %q", got)
	}
}

/*
TestAScreenPathReachesTheApp.

The other side of the same rule: everything the API did not claim belongs to
the app, including paths that were never exported.
*/
func TestAScreenPathReachesTheApp(t *testing.T) {
	router := routerServingBoth(t)

	for _, path := range []string{"/", "/signals", "/signals/3f2504e0-4f89-11d3-9a0c-0305e82c3301"} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))

		if rec.Code != http.StatusOK {
			t.Errorf("%s answered %d; want 200 from the app", path, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "<!doctype html>") {
			t.Errorf("%s did not reach the app: %q", path, rec.Body.String())
		}
	}
}

/*
TestTheAPIStillAnswersWithTheAppMounted.

The app is a catch-all. A catch-all that shadows a real endpoint is the failure
mode of mounting one, and it would be invisible until a screen stopped loading
data.
*/
func TestTheAPIStillAnswersWithTheAppMounted(t *testing.T) {
	router := routerServingBoth(t)

	for _, path := range []string{
		"/api/v1/candles", "/api/v1/signals", "/api/v1/status", "/api/v1/device",
		"/api/v1/signals/3f2504e0-4f89-11d3-9a0c-0305e82c3301",
	} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))

		if rec.Code != http.StatusOK {
			t.Errorf("%s answered %d; want 200", path, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), `"reached":"api"`) {
			t.Errorf("%s was shadowed by the app: %q", path, rec.Body.String())
		}
	}
}
