package handler

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/helper"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/candle"
)

type candleHandler struct {
	usecase    candle.CandleUsecase
	logger     *slog.Logger
	symbol     string
	marketType constants.MarketType
	timeframes []constants.Timeframe
	now        func() time.Time
}

// NewCandleHandlerImpl builds the candle read handler.
func NewCandleHandlerImpl(
	usecase candle.CandleUsecase, logger *slog.Logger,
	symbol string, marketType constants.MarketType, timeframes []constants.Timeframe,
) candle.CandleHandler {
	return &candleHandler{
		usecase: usecase, logger: logger,
		symbol: symbol, marketType: marketType, timeframes: timeframes,
		now: func() time.Time { return time.Now().UTC() },
	}
}

// Candles answers GET /api/v1/candles.
//
// Every candle here is closed: only closed candles are stored, and the forming
// bar is available on the stream alone. That is the one place it is legitimate
// and it is flagged there.
func (h *candleHandler) Candles(w http.ResponseWriter, r *http.Request) {
	timeframe, ok := RequireTimeframe(w, h.logger, r, h.timeframes)
	if !ok {
		return
	}

	limit, err := helper.QueryLimit(r, "limit",
		constants.APICandleLimitDefault, constants.APICandleLimit)
	if err != nil {
		code := constants.APIErrInvalidParameter
		if limit == 0 && r.URL.Query().Get("limit") != "" {
			code = limitCode(err)
		}
		helper.WriteAPIError(w, h.logger, http.StatusBadRequest, code, err.Error())
		return
	}

	now := h.now()
	from, err := helper.QueryTime(r, "from", now.Add(-time.Duration(limit)*timeframe.Duration()))
	if err != nil {
		helper.WriteAPIError(w, h.logger, http.StatusBadRequest,
			constants.APIErrInvalidParameter, err.Error())
		return
	}
	to, err := helper.QueryTime(r, "to", now)
	if err != nil {
		helper.WriteAPIError(w, h.logger, http.StatusBadRequest,
			constants.APIErrInvalidParameter, err.Error())
		return
	}
	if to.Before(from) {
		helper.WriteAPIError(w, h.logger, http.StatusBadRequest, constants.APIErrInvalidParameter,
			"to is before from")
		return
	}

	candles, err := h.usecase.FetchCandles(r.Context(), candle.FetchCandlesParams{
		Symbol: h.symbol, MarketType: h.marketType, Timeframe: timeframe,
		From: from, To: to,
	})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "could not read candles", "error", err)
		helper.WriteAPIError(w, h.logger, http.StatusInternalServerError,
			constants.APIErrInternal, "the candles could not be read")
		return
	}

	// Newest-bounded rather than oldest: a window wider than the limit should
	// return the most recent bars, which is what a chart opens on.
	truncated := false
	if len(candles) > limit {
		candles = candles[len(candles)-limit:]
		truncated = true
	}

	helper.WriteAPIJSON(w, h.logger, http.StatusOK, candlesResponse{
		Symbol:     h.symbol,
		MarketType: h.marketType.String(),
		Timeframe:  timeframe.String(),
		From:       from.UTC(),
		To:         to.UTC(),
		Count:      len(candles),
		Limit:      limit,
		Truncated:  truncated,
		Candles:    candle.ToCandleResponses(candles),
	})
}

type candlesResponse struct {
	Symbol     string    `json:"symbol"`
	MarketType string    `json:"market_type"`
	Timeframe  string    `json:"timeframe"`
	From       time.Time `json:"from"`
	To         time.Time `json:"to"`

	// Count is how many are in this response and Limit what was asked for.
	// Truncated says the window held more than the limit, so a chart drawn
	// from this knows it is looking at the newest slice rather than the whole
	// range it requested.
	Count     int  `json:"count"`
	Limit     int  `json:"limit"`
	Truncated bool `json:"truncated"`

	Candles []candle.CandleResponse `json:"candles"`
}

// RequireTimeframe reads and validates the timeframe, writing the error when
// it is missing or not collected.
//
// Shared because three endpoints take one and all three must refuse the same
// values: a timeframe nothing collects would otherwise return an empty list,
// which reads as "no data" rather than "not configured".
func RequireTimeframe(
	w http.ResponseWriter, log *slog.Logger, r *http.Request, collected []constants.Timeframe,
) (constants.Timeframe, bool) {
	raw := r.URL.Query().Get("timeframe")
	if raw == "" {
		helper.WriteAPIError(w, log, http.StatusBadRequest, constants.APIErrInvalidParameter,
			"timeframe is required; "+collectedList(collected))
		return "", false
	}

	timeframe, err := constants.ParseTimeframe(raw)
	if err != nil {
		helper.WriteAPIError(w, log, http.StatusBadRequest, constants.APIErrInvalidParameter,
			err.Error()+"; "+collectedList(collected))
		return "", false
	}

	for _, tf := range collected {
		if tf == timeframe {
			return timeframe, true
		}
	}
	helper.WriteAPIError(w, log, http.StatusBadRequest, constants.APIErrInvalidParameter,
		"timeframe="+raw+" is not collected; "+collectedList(collected))
	return "", false
}

func collectedList(collected []constants.Timeframe) string {
	names := make([]string, 0, len(collected))
	for _, tf := range collected {
		names = append(names, tf.String())
	}
	return "this deployment collects " + strings.Join(names, ", ")
}

// limitCode separates "too many" from "not a number", because the app's
// correct response differs: page, rather than fix the request.
func limitCode(err error) constants.APIErrorCode {
	if err != nil && strings.Contains(err.Error(), "above the maximum") {
		return constants.APIErrLimitExceeded
	}
	return constants.APIErrInvalidParameter
}
