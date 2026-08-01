package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/middleware"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	"github.com/spioneracorei8/btcusd-trading-platform/server/routes"
	_health_handler "github.com/spioneracorei8/btcusd-trading-platform/server/services/health/handler"
	_health_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/health/usecase"
)

// stubHealthRepository stands in for the database so the handler can be
// exercised without one.
type stubHealthRepository struct {
	err error
}

func (s stubHealthRepository) PingDatabase(context.Context) error { return s.err }

// testRouter builds the real router, usecase and handler over a stub
// repository, so the test covers the whole vertical slice.
func testRouter(pingErr error) http.Handler {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	healthUs := _health_us.NewHealthUsecaseImpl(stubHealthRepository{err: pingErr})
	healthHandler := _health_handler.NewHealthHandlerImpl(healthUs, log)

	router := chi.NewRouter()
	route := routes.NewRoute(router, middleware.InitMiddleware(log))
	route.RegisterHealthHandler(healthHandler)
	return router
}

func decodeHealth(t *testing.T, body []byte) models.Health {
	t.Helper()
	var got models.Health
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("response is not JSON: %v (%s)", err, body)
	}
	return got
}

func TestLivenessStaysUpWhenDatabaseIsDown(t *testing.T) {
	// /health must stay 200 even when the database is unreachable: a liveness
	// probe should not restart a working API because PostgreSQL blipped.
	router := testRouter(errors.New("database down"))

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := decodeHealth(t, rec.Body.Bytes()); got.Status != constants.StatusOK {
		t.Errorf("status = %q, want %q", got.Status, constants.StatusOK)
	}
}

func TestReadinessWithHealthyDatabase(t *testing.T) {
	router := testRouter(nil)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ready", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := decodeHealth(t, rec.Body.Bytes()); got.Status != constants.StatusReady {
		t.Errorf("status = %q, want %q", got.Status, constants.StatusReady)
	}
}

func TestReadinessWithUnreachableDatabase(t *testing.T) {
	router := testRouter(errors.New("connection refused"))

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ready", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}

	got := decodeHealth(t, rec.Body.Bytes())
	if got.Status != constants.StatusUnavailable {
		t.Errorf("status = %q, want %q", got.Status, constants.StatusUnavailable)
	}
	if got.Error == "" {
		t.Error("body carries no error message")
	}
}

func TestUnknownRouteReturnsNotFound(t *testing.T) {
	router := testRouter(nil)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/orders", nil))

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestRecovererTurnsPanicIntoInternalServerError(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	middl := middleware.InitMiddleware(log)

	handler := middl.Recoverer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
	var body models.ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("panic response is not JSON: %v (%s)", err, rec.Body.String())
	}
	if body.Error != constants.MsgInternalServerError {
		t.Errorf("error = %q, want %q", body.Error, constants.MsgInternalServerError)
	}
}
