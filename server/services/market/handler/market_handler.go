package handler

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/helper"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/market"
	_health_handler "github.com/spioneracorei8/btcusd-trading-platform/server/services/health/handler"
)

type marketHandler struct {
	marketUsecase market.MarketUsecase
	logger        *slog.Logger
	now           func() time.Time
}

// NewMarketHandlerImpl builds the market status handler.
func NewMarketHandlerImpl(marketUsecase market.MarketUsecase, logger *slog.Logger) market.MarketHandler {
	return &marketHandler{
		marketUsecase: marketUsecase,
		logger:        logger,
		now:           func() time.Time { return time.Now().UTC() },
	}
}

// statusResponse is the JSON shape of GET /internal/market/status.
//
// A response type separate from the model keeps the wire format stable while
// the internals move, and lets the durations be rendered as plain seconds
// rather than Go's duration encoding.
type statusResponse struct {
	Symbol     string `json:"symbol"`
	MarketType string `json:"market_type"`

	Collector collectorResponse  `json:"collector"`
	Timeframes []timeframeResponse `json:"timeframes"`

	// Stale is the combination worth paging on: connected but not advancing.
	Stale bool `json:"stale"`
}

type collectorResponse struct {
	Running     bool   `json:"running"`
	WSConnected bool   `json:"ws_connected"`

	// UptimeSeconds counts from the process start, HeartbeatAgeSeconds from
	// the last published beat. Together they separate a collector that has
	// been up for days from one that is restarting every few seconds, and
	// both from one that has died while still claiming to be connected.
	UptimeSeconds       *int64 `json:"uptime_seconds,omitempty"`
	HeartbeatAgeSeconds *int64 `json:"heartbeat_age_seconds,omitempty"`

	StartedAt          *time.Time `json:"started_at,omitempty"`
	LastConnectedAt    *time.Time `json:"last_connected_at,omitempty"`
	LastDisconnectedAt *time.Time `json:"last_disconnected_at,omitempty"`
	LastDisconnectNote string     `json:"last_disconnect_note,omitempty"`
	ReconnectCount     int32      `json:"reconnect_count"`
}

type timeframeResponse struct {
	Timeframe        string     `json:"timeframe"`
	LatestOpenTime   *time.Time `json:"latest_open_time,omitempty"`
	LatestAgeSeconds *int64     `json:"latest_age_seconds,omitempty"`
	UnfilledGaps     int64      `json:"unfilled_gaps"`
}

// Status reports whether the market data is healthy right now.
func (h *marketHandler) Status(w http.ResponseWriter, r *http.Request) {
	now := h.now()

	status, err := h.marketUsecase.Status(r.Context(), now)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "could not assemble market status", "error", err)
		_health_handler.WriteJSON(w, h.logger, http.StatusInternalServerError,
			models.ErrorResponse{Error: constants.MsgInternalServerError})
		return
	}

	_health_handler.WriteJSON(w, h.logger, http.StatusOK, toStatusResponse(status, now))
}

// toStatusResponse renders the model for the wire.
func toStatusResponse(status models.MarketStatus, now time.Time) statusResponse {
	resp := statusResponse{
		Symbol:     status.Symbol,
		MarketType: status.MarketType.String(),
		Stale:      status.Stale,
		Timeframes: make([]timeframeResponse, 0, len(status.Timeframes)),
		Collector: collectorResponse{
			WSConnected:        status.Collector.WSConnected,
			LastDisconnectNote: status.Collector.LastDisconnectNote,
			ReconnectCount:     status.Collector.ReconnectCount,
			LastConnectedAt:    status.Collector.LastConnectedAt,
			LastDisconnectedAt: status.Collector.LastDisconnectedAt,
		},
	}

	// A zero StartedAt means no collector has ever registered, which is
	// different from one that started and then stopped reporting.
	if !status.Collector.StartedAt.IsZero() {
		startedAt := helper.UTC(status.Collector.StartedAt)
		uptime := int64(status.Collector.Uptime(now) / time.Second)
		age := int64(status.Collector.HeartbeatAge(now) / time.Second)

		resp.Collector.Running = true
		resp.Collector.StartedAt = &startedAt
		resp.Collector.UptimeSeconds = &uptime
		resp.Collector.HeartbeatAgeSeconds = &age
	}

	for _, timeframe := range status.Timeframes {
		resp.Timeframes = append(resp.Timeframes, timeframeResponse{
			Timeframe:        timeframe.Timeframe.String(),
			LatestOpenTime:   timeframe.LatestOpenTime,
			LatestAgeSeconds: timeframe.LatestAgeSeconds,
			UnfilledGaps:     timeframe.UnfilledGaps,
		})
	}
	return resp
}
