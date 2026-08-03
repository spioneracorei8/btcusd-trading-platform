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
