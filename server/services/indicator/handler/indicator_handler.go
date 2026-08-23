package handler

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/helper"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/candle"
	_candle_handler "github.com/spioneracorei8/btcusd-trading-platform/server/services/candle/handler"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/indicator"
	_indicator_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/indicator/usecase"
)

type indicatorHandler struct {
	candles    candle.CandleUsecase
	config     _indicator_us.SetConfig
	logger     *slog.Logger
	symbol     string
	marketType constants.MarketType
	timeframes []constants.Timeframe
	now        func() time.Time
}

// NewIndicatorHandlerImpl builds the indicator read handler.
func NewIndicatorHandlerImpl(
	candles candle.CandleUsecase, config _indicator_us.SetConfig, logger *slog.Logger,
	symbol string, marketType constants.MarketType, timeframes []constants.Timeframe,
) indicator.IndicatorHandler {
	return &indicatorHandler{
		candles: candles, config: config, logger: logger,
		symbol: symbol, marketType: marketType, timeframes: timeframes,
		now: func() time.Time { return time.Now().UTC() },
	}
}

// Indicators answers GET /api/v1/indicators.
//
// # Why these are recomputed rather than read
//
// Phase 03 decided not to store indicator values, and that decision stands.
// Storing them would create a second source of truth that can disagree with
// the candles, and the disagreement would be silent — a stored EMA computed by
// an older version of the code looks exactly like a current one.
//
// The cost is a warm-up read on every request: the values are only meaningful
// once the indicators have converged, so the range is extended backwards by
// the warm-up before the requested window and the extension is discarded. That
// is measured in docs/api.md rather than assumed acceptable.
func (h *indicatorHandler) Indicators(w http.ResponseWriter, r *http.Request) {
	timeframe, ok := _candle_handler.RequireTimeframe(w, h.logger, r, h.timeframes)
	if !ok {
		return
	}

	now := h.now()
	from, err := helper.QueryTime(r, "from",
		now.Add(-constants.APICandleLimitDefault*timeframe.Duration()))
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
		helper.WriteAPIError(w, h.logger, http.StatusBadRequest,
			constants.APIErrInvalidParameter, "to is before from")
		return
	}

	set, err := _indicator_us.NewSet(h.config)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "could not build the indicator set", "error", err)
		helper.WriteAPIError(w, h.logger, http.StatusInternalServerError,
			constants.APIErrInternal, "the indicators could not be built")
		return
	}

	// The window a client asked for, plus the warm-up it needs to mean
	// anything. Serving values from an unconverged set would be serving
	// numbers that are wrong in a way nothing on the wire could show.
	warmup := set.WarmupPeriod()
	if requested := int(to.Sub(from)/timeframe.Duration()) + 1; requested > constants.APICandleLimit {
		helper.WriteAPIError(w, h.logger, http.StatusBadRequest, constants.APIErrLimitExceeded,
			"the window covers more than "+strconv.Itoa(constants.APICandleLimit)+
				" bars; narrow it or ask for a longer timeframe")
		return
	}

	candles, err := h.candles.FetchCandles(r.Context(), candle.FetchCandlesParams{
		Symbol: h.symbol, MarketType: h.marketType, Timeframe: timeframe,
		From: from.Add(-time.Duration(warmup) * timeframe.Duration()),
		To:   to,
	})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "could not read candles", "error", err)
		helper.WriteAPIError(w, h.logger, http.StatusInternalServerError,
			constants.APIErrInternal, "the candles could not be read")
		return
	}

	snapshots := _indicator_us.EvaluateSet(set, candles)

	// Drop the warm-up extension: the client asked for a window and must not
	// be handed bars from before it.
	values := make([]indicatorResponse, 0, len(snapshots))
	for _, s := range snapshots {
		if s.OpenTime.Before(from) {
			continue
		}
		values = append(values, indicatorResponse{
			OpenTime: s.OpenTime.UTC(),
			EMA:      s.EMA, RSI: s.RSI, ATR: s.ATR, VWAP: s.VWAP,
		})
	}

	helper.WriteAPIJSON(w, h.logger, http.StatusOK, indicatorsResponse{
		Symbol:     h.symbol,
		MarketType: h.marketType.String(),
		Timeframe:  timeframe.String(),
		From:       from.UTC(),
		To:         to.UTC(),
		Periods: periodsResponse{
			EMA: h.config.EMAPeriod, RSI: h.config.RSIPeriod, ATR: h.config.ATRPeriod,
		},
		WarmupBars: warmup,
		BarsRead:   len(candles),
		Count:      len(values),
		Values:     values,
	})
}

type indicatorsResponse struct {
	Symbol     string    `json:"symbol"`
	MarketType string    `json:"market_type"`
	Timeframe  string    `json:"timeframe"`
	From       time.Time `json:"from"`
	To         time.Time `json:"to"`

	// Periods are what these values were computed with, so a client is never
	// comparing an EMA(200) against an EMA(50) without knowing it.
	Periods periodsResponse `json:"periods"`

	// WarmupBars is how far before `from` the computation had to start, and
	// BarsRead how many were read in total. Both are here because a short
	// Count on a wide window is otherwise unexplained: it means the series
	// does not go back far enough to converge.
	WarmupBars int `json:"warmup_bars"`
	BarsRead   int `json:"bars_read"`

	Count  int                 `json:"count"`
	Values []indicatorResponse `json:"values"`
}

type periodsResponse struct {
	EMA int `json:"ema"`
	RSI int `json:"rsi"`
	ATR int `json:"atr"`
}

// indicatorResponse carries floats, not strings.
//
// These are the one place float64 is correct: CLAUDE.md §4 reserves decimal
// for money and balances, and an indicator is a derived statistic that is
// never added to a balance. Rendering them as strings would imply a precision
// the computation does not have.
type indicatorResponse struct {
	OpenTime time.Time `json:"open_time"`
	EMA      float64   `json:"ema"`
	RSI      float64   `json:"rsi"`
	ATR      float64   `json:"atr"`
	VWAP     float64   `json:"vwap"`
}
