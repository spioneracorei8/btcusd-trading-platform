package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/health"
)

type healthHandler struct {
	healthUsecase health.HealthUsecase
	logger        *slog.Logger
}

// NewHealthHandlerImpl builds the health handler on top of a usecase.
func NewHealthHandlerImpl(healthUsecase health.HealthUsecase, logger *slog.Logger) health.HealthHandler {
	return &healthHandler{
		healthUsecase: healthUsecase,
		logger:        logger,
	}
}

// Liveness answers GET /health with 200 as long as the process is running.
func (h *healthHandler) Liveness(w http.ResponseWriter, _ *http.Request) {
	WriteJSON(w, h.logger, http.StatusOK, h.healthUsecase.Liveness())
}

// Readiness answers GET /ready, returning 503 when the database is unreachable.
func (h *healthHandler) Readiness(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), constants.ReadyCheckTimeout)
	defer cancel()

	result, err := h.healthUsecase.Readiness(ctx)
	if err != nil {
		h.logger.WarnContext(ctx, "readiness check failed", "error", err)
		WriteJSON(w, h.logger, http.StatusServiceUnavailable, result)
		return
	}

	WriteJSON(w, h.logger, http.StatusOK, result)
}

// WriteJSON encodes body as JSON with the given status code.
//
// The response is marshalled before the header is written so an encoding
// failure can still produce a clean 500 instead of a truncated body.
func WriteJSON(w http.ResponseWriter, logger *slog.Logger, status int, body any) {
	payload, err := json.Marshal(body)
	if err != nil {
		logger.Error("encode response body", "error", err)
		http.Error(w, `{"error":"`+constants.MsgInternalServerError+`"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if _, err := w.Write(payload); err != nil {
		logger.Error("write response body", "error", err)
	}
}
