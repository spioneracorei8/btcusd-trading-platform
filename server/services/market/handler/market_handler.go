package handler

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/helper"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	_health_handler "github.com/spioneracorei8/btcusd-trading-platform/server/services/health/handler"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/market"
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

	Collector  collectorResponse   `json:"collector"`
	Timeframes []timeframeResponse `json:"timeframes"`

	// Stale is the combination worth paging on: connected but not advancing.
	//
	// Null means the check did not run, which is the honest answer outside the
	// live state: during a backfill an old candle is progress, and a false
	// there would read as an all-clear.
	Stale *bool `json:"stale"`
}

type collectorResponse struct {
	// State is the lifecycle phase, including never_started when no collector
	// has ever registered.
	State string `json:"state"`

	Running bool `json:"running"`

	// Every measured field is a pointer. When no collector has registered,
	// nothing was measured, and a zero would claim otherwise — "connected:
	// false, reconnects: 0" reads like a healthy idle process rather than an
	// absent one.
	WSConnected *bool `json:"ws_connected"`

	// UptimeSeconds counts from the process start, HeartbeatAgeSeconds from
	// the last published beat. Together they separate a collector that has
	// been up for days from one that is restarting every few seconds, and
	// both from one that has died while still claiming to be connected.
	UptimeSeconds       *int64 `json:"uptime_seconds"`
	HeartbeatAgeSeconds *int64 `json:"heartbeat_age_seconds"`

	StartedAt          *time.Time `json:"started_at"`
	StateChangedAt     *time.Time `json:"state_changed_at"`
	LastConnectedAt    *time.Time `json:"last_connected_at"`
	LastDisconnectedAt *time.Time `json:"last_disconnected_at"`
	LastDisconnectNote string     `json:"last_disconnect_note,omitempty"`
	ReconnectCount     *int32     `json:"reconnect_count"`
}

// timeframeResponse keeps a stable shape: a timeframe with no data yet emits
// null rather than dropping the field, so a consumer can rely on the keys
// existing whatever the collector is doing.
type timeframeResponse struct {
	Timeframe string `json:"timeframe"`

	EarliestOpenTime *time.Time `json:"earliest_open_time"`
	LatestOpenTime   *time.Time `json:"latest_open_time"`
	LatestAgeSeconds *int64     `json:"latest_age_seconds"`
	UnfilledGaps     int64      `json:"unfilled_gaps"`

	// The window the collector is working towards, so a three-year backfill
	// can be seen advancing rather than merely being behind.
	BackfillFrom time.Time `json:"backfill_from"`
	BackfillTo   time.Time `json:"backfill_to"`
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
			State: status.Collector.State.String(),
		},
	}

	// never_started is the absence of a collector, not a collector reporting
	// zeroes. Leaving every measured field null keeps "nothing was measured"
	// distinguishable from "measured, and it was zero".
	if status.Collector.State != constants.CollectorNeverStarted {
		wsConnected := status.Collector.WSConnected
		reconnects := status.Collector.ReconnectCount
		startedAt := helper.UTC(status.Collector.StartedAt)
		uptime := int64(status.Collector.Uptime(now) / time.Second)
		age := int64(status.Collector.HeartbeatAge(now) / time.Second)

		resp.Collector.Running = true
		resp.Collector.WSConnected = &wsConnected
		resp.Collector.ReconnectCount = &reconnects
		resp.Collector.StartedAt = &startedAt
		resp.Collector.UptimeSeconds = &uptime
		resp.Collector.HeartbeatAgeSeconds = &age
		resp.Collector.LastConnectedAt = status.Collector.LastConnectedAt
		resp.Collector.LastDisconnectedAt = status.Collector.LastDisconnectedAt
		resp.Collector.LastDisconnectNote = status.Collector.LastDisconnectNote

		if !status.Collector.StateChangedAt.IsZero() {
			changedAt := helper.UTC(status.Collector.StateChangedAt)
			resp.Collector.StateChangedAt = &changedAt
		}
	}

	for _, timeframe := range status.Timeframes {
		resp.Timeframes = append(resp.Timeframes, timeframeResponse{
			Timeframe:        timeframe.Timeframe.String(),
			EarliestOpenTime: timeframe.EarliestOpenTime,
			LatestOpenTime:   timeframe.LatestOpenTime,
			LatestAgeSeconds: timeframe.LatestAgeSeconds,
			UnfilledGaps:     timeframe.UnfilledGaps,
			BackfillFrom:     timeframe.BackfillFrom,
			BackfillTo:       timeframe.BackfillTo,
		})
	}
	return resp
}
