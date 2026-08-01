package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
)

// readyCheckTimeout bounds the database ping behind /ready so a hung database
// cannot make the readiness probe itself hang.
const readyCheckTimeout = 2 * time.Second

// healthResponse is the body of /health and the success body of /ready.
type healthResponse struct {
	Status string `json:"status"`
}

// errorResponse is the body returned when a request cannot be served.
type errorResponse struct {
	Status string `json:"status,omitempty"`
	Error  string `json:"error"`
}

// handleHealth reports that the process itself is alive. It deliberately does
// not touch the database: a liveness probe must not restart a healthy API
// just because PostgreSQL is briefly unavailable.
func (a *api) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, a.logger, http.StatusOK, healthResponse{Status: "ok"})
}

// handleReady reports whether the API can actually serve traffic, which
// includes reaching the database. It returns 503 when the database is down.
func (a *api) handleReady(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), readyCheckTimeout)
	defer cancel()

	if err := a.store.Ping(ctx); err != nil {
		a.logger.WarnContext(ctx, "readiness check failed", "error", err)
		writeJSON(w, a.logger, http.StatusServiceUnavailable, errorResponse{
			Status: "unavailable",
			Error:  "database unreachable",
		})
		return
	}

	writeJSON(w, a.logger, http.StatusOK, healthResponse{Status: "ready"})
}

// writeJSON encodes body as JSON with the given status code.
//
// The response is marshalled before the header is written so an encoding
// failure can still produce a clean 500 instead of a truncated body.
func writeJSON(w http.ResponseWriter, logger *slog.Logger, status int, body any) {
	payload, err := json.Marshal(body)
	if err != nil {
		logger.Error("encode response body", "error", err)
		http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if _, err := w.Write(payload); err != nil {
		logger.Error("write response body", "error", err)
	}
}
