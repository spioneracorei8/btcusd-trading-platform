package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/spioneracorei8/btcusd-trading-platform/backend/internal/config"
	"github.com/spioneracorei8/btcusd-trading-platform/backend/internal/domain"
)

// stubPinger stands in for the store so the handlers can be exercised without
// a database.
type stubPinger struct {
	err error
}

func (s stubPinger) Ping(context.Context) error { return s.err }

// testRouter builds the real router with a stub store and a silent logger.
func testRouter(store pinger) http.Handler {
	cfg := &config.Config{
		App: config.App{Env: config.EnvDev, LogLevel: slog.LevelInfo, HTTPPort: 8080},
		Market: config.Market{
			Symbol:     "BTCUSDT",
			Type:       domain.MarketTypeSpot,
			Timeframes: []domain.Timeframe{domain.Timeframe1m},
		},
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return newRouter(cfg, logger, store)
}

func TestHandleHealth(t *testing.T) {
	// /health must stay 200 even when the database is unreachable: a liveness
	// probe should not restart a working API because PostgreSQL blipped.
	router := testRouter(stubPinger{err: errors.New("database down")})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}

	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not JSON: %v (%s)", err, rec.Body.String())
	}
	if body["status"] != "ok" {
		t.Errorf(`body = %v, want {"status":"ok"}`, body)
	}
}

func TestHandleReadyWithHealthyDatabase(t *testing.T) {
	router := testRouter(stubPinger{})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ready", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not JSON: %v (%s)", err, rec.Body.String())
	}
	if body["status"] != "ready" {
		t.Errorf(`body = %v, want {"status":"ready"}`, body)
	}
}

func TestHandleReadyWithUnreachableDatabase(t *testing.T) {
	router := testRouter(stubPinger{err: errors.New("connection refused")})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ready", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}

	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not JSON: %v (%s)", err, rec.Body.String())
	}
	if body["error"] == "" {
		t.Errorf("body = %v, want an error message", body)
	}
}

func TestRecovererTurnsPanicIntoInternalServerError(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := recoverer(logger)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestUnknownRouteReturnsNotFound(t *testing.T) {
	router := testRouter(stubPinger{})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/orders", nil))

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}
