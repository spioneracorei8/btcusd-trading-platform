package handler

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/helper"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/signal"
)

type signalHandler struct {
	usecase    signal.SignalUsecase
	logger     *slog.Logger
	symbol     string
	marketType constants.MarketType
}

// NewSignalHandlerImpl builds the signal read handler.
func NewSignalHandlerImpl(
	usecase signal.SignalUsecase, logger *slog.Logger,
	symbol string, marketType constants.MarketType,
) signal.SignalHandler {
	return &signalHandler{usecase: usecase, logger: logger, symbol: symbol, marketType: marketType}
}

// Signals answers GET /api/v1/signals.
func (h *signalHandler) Signals(w http.ResponseWriter, r *http.Request) {
	limit, err := helper.QueryLimit(r, "limit", constants.APIPageLimitDefault, constants.APIPageLimit)
	if err != nil {
		helper.WriteAPIError(w, h.logger, http.StatusBadRequest, limitCode(err), err.Error())
		return
	}
	offset, err := helper.QueryOffset(r, "offset")
	if err != nil {
		helper.WriteAPIError(w, h.logger, http.StatusBadRequest,
			constants.APIErrInvalidParameter, err.Error())
		return
	}

	var direction constants.Direction
	if raw := r.URL.Query().Get("direction"); raw != "" {
		parsed, err := constants.ParseDirection(raw)
		if err != nil {
			helper.WriteAPIError(w, h.logger, http.StatusBadRequest,
				constants.APIErrInvalidParameter, err.Error())
			return
		}
		direction = parsed
	}

	signals, total, err := h.usecase.ListSignals(r.Context(), signal.ListParams{
		Symbol: h.symbol, MarketType: h.marketType, Direction: direction,
		Limit: int32(limit), Offset: int32(offset),
	})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "could not list signals", "error", err)
		helper.WriteAPIError(w, h.logger, http.StatusInternalServerError,
			constants.APIErrInternal, "the signals could not be read")
		return
	}

	items := make([]signal.SignalResponse, 0, len(signals))
	for _, s := range signals {
		items = append(items, signal.ToSignalResponse(s, false))
	}

	helper.WriteAPIJSON(w, h.logger, http.StatusOK, signalsResponse{
		Symbol: h.symbol, MarketType: h.marketType.String(),
		Count: len(items), Total: total, Limit: limit, Offset: offset,
		Signals: items,
	})
}

// Signal answers GET /api/v1/signals/{id}.
//
// This one carries the full reason: the indicator snapshot, the trend state
// and the resolved parameter set. It is what makes a signal reviewable months
// later, when the values behind it are otherwise gone — indicators are never
// stored, so they cannot be recomputed against the warm-up state the live
// process actually had.
func (h *signalHandler) Signal(w http.ResponseWriter, r *http.Request) {
	raw := chi.URLParam(r, "id")

	id, err := uuid.Parse(raw)
	if err != nil {
		helper.WriteAPIError(w, h.logger, http.StatusBadRequest,
			constants.APIErrInvalidParameter, "id="+raw+" is not a uuid")
		return
	}

	found, err := h.usecase.FetchSignalById(r.Context(), id)
	if errors.Is(err, constants.ErrNotFound) {
		helper.WriteAPIError(w, h.logger, http.StatusNotFound,
			constants.APIErrNotFound, "no signal has that id")
		return
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "could not read a signal", "error", err, "id", raw)
		helper.WriteAPIError(w, h.logger, http.StatusInternalServerError,
			constants.APIErrInternal, "the signal could not be read")
		return
	}

	helper.WriteAPIJSON(w, h.logger, http.StatusOK, signal.ToSignalResponse(found, true))
}

type signalsResponse struct {
	Symbol     string `json:"symbol"`
	MarketType string `json:"market_type"`

	// Total is the size of the collection this page came from, so a client
	// can tell a short page from the last page without a second request.
	Count  int   `json:"count"`
	Total  int64 `json:"total"`
	Limit  int   `json:"limit"`
	Offset int   `json:"offset"`

	Signals []signal.SignalResponse `json:"signals"`
}

// limitCode separates "too many" from "not a number".
func limitCode(err error) constants.APIErrorCode {
	if err != nil && strings.Contains(err.Error(), "above the maximum") {
		return constants.APIErrLimitExceeded
	}
	return constants.APIErrInvalidParameter
}
