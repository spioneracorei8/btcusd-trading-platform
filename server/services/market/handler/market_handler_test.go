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
	"time"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	_market_handler "github.com/spioneracorei8/btcusd-trading-platform/server/services/market/handler"
)

// stubMarketUsecase serves a canned status. Only Status is exercised here;
// the ingestion methods exist to satisfy the interface.
type stubMarketUsecase struct {
	status models.MarketStatus
	err    error
}

func (s *stubMarketUsecase) Run(context.Context) error      { return nil }
func (s *stubMarketUsecase) Backfill(context.Context) error { return nil }

func (s *stubMarketUsecase) LatestOpenCandle(constants.Timeframe) (models.Candle, bool) {
	return models.Candle{}, false
}

func (s *stubMarketUsecase) Status(context.Context, time.Time) (models.MarketStatus, error) {
	return s.status, s.err
}

func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// callStatus serves one request and returns the code and the decoded body.
func callStatus(t *testing.T, usecase *stubMarketUsecase) (int, map[string]any) {
	t.Helper()

	handler := _market_handler.NewMarketHandlerImpl(usecase, silentLogger())
	recorder := httptest.NewRecorder()
	handler.Status(recorder, httptest.NewRequest(http.MethodGet, "/internal/market/status", nil))

	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not JSON: %v (%s)", err, recorder.Body.String())
	}
	return recorder.Code, body
}

// TestStatusAnswersWhenNoCollectorHasRun is the wire half of fix 2. The
// endpoint exists so a dead collector can be diagnosed without opening the
// container logs, which a 500 defeats entirely.
//
// The distinction it has to preserve: nothing was measured, as opposed to
// measured and found to be zero. "ws_connected: false, reconnect_count: 0"
// reads like a healthy idle process rather than an absent one, so every
// measured field is null instead.
func TestStatusAnswersWhenNoCollectorHasRun(t *testing.T) {
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	latest := now.Add(-time.Minute)

	usecase := &stubMarketUsecase{status: models.MarketStatus{
		Symbol:     "BTCUSDT",
		MarketType: constants.MarketTypeSpot,
		Collector: models.CollectorStatus{
			Symbol:     "BTCUSDT",
			MarketType: constants.MarketTypeSpot,
			State:      constants.CollectorNeverStarted,
		},
		Timeframes: []models.TimeframeStatus{{
			Timeframe:      constants.Timeframe1m,
			LatestOpenTime: &latest,
			BackfillFrom:   now.Add(-24 * time.Hour),
			BackfillTo:     now,
		}},
	}}

	code, body := callStatus(t, usecase)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want %d: an absent collector is a valid answer", code, http.StatusOK)
	}

	collector, ok := body["collector"].(map[string]any)
	if !ok {
		t.Fatalf("collector is missing from the payload: %v", body)
	}
	if collector["state"] != constants.CollectorNeverStarted.String() {
		t.Errorf("state = %v, want %q", collector["state"], constants.CollectorNeverStarted)
	}
	if collector["running"] != false {
		t.Errorf("running = %v, want false", collector["running"])
	}

	// Present as keys, null as values: a consumer can rely on the shape while
	// still seeing that nothing was measured.
	for _, field := range []string{
		"ws_connected", "reconnect_count", "started_at",
		"uptime_seconds", "heartbeat_age_seconds", "state_changed_at",
	} {
		value, present := collector[field]
		if !present {
			t.Errorf("%s is absent from the payload; it must be present and null", field)
			continue
		}
		if value != nil {
			t.Errorf("%s = %v, want null: no collector ran, so nothing was measured", field, value)
		}
	}

	// The candles table does not depend on the collector being alive, so the
	// per-timeframe data must still be there to read.
	timeframes, ok := body["timeframes"].([]any)
	if !ok || len(timeframes) != 1 {
		t.Fatalf("timeframes = %v, want one entry", body["timeframes"])
	}
	timeframe, _ := timeframes[0].(map[string]any)
	if timeframe["latest_open_time"] == nil {
		t.Error("latest_open_time was dropped even though a candle is stored")
	}
	if timeframe["backfill_from"] == nil || timeframe["backfill_to"] == nil {
		t.Errorf("the backfill window is missing: %v", timeframe)
	}
}

// TestStatusRendersStaleAsNullOutsideLive keeps the third answer on the wire.
// Encoding "the check did not run" as false would be indistinguishable from a
// genuine all-clear.
func TestStatusRendersStaleAsNullOutsideLive(t *testing.T) {
	usecase := &stubMarketUsecase{status: models.MarketStatus{
		Symbol:     "BTCUSDT",
		MarketType: constants.MarketTypeSpot,
		Collector: models.CollectorStatus{
			State:     constants.CollectorBackfilling,
			StartedAt: time.Now().UTC(),
		},
		Stale: nil,
	}}

	code, body := callStatus(t, usecase)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want %d", code, http.StatusOK)
	}

	value, present := body["stale"]
	if !present {
		t.Fatal("stale is absent from the payload; it must be present and null")
	}
	if value != nil {
		t.Errorf("stale = %v, want null while backfilling", value)
	}
}

// TestStatusStillFailsOnDatabaseError checks that making the absent row a
// valid state did not swallow the failures 500 is actually for.
func TestStatusStillFailsOnDatabaseError(t *testing.T) {
	usecase := &stubMarketUsecase{err: errors.New("connection refused")}

	code, body := callStatus(t, usecase)
	if code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d for an unreachable database", code, http.StatusInternalServerError)
	}
	if body["error"] == nil {
		t.Errorf("the failure response carries no error message: %v", body)
	}
}
