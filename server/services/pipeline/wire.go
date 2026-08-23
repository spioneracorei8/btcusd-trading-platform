package pipeline

import (
	"time"
)

// The JSON shape of the pipeline status.
//
// It lives here rather than in the handler because two transports send it:
// GET /api/v1/status and the websocket's status topic. Rendered in one place,
// a client parses one shape however it arrived.

type StatusResponse struct {
	Symbol     string    `json:"symbol"`
	MarketType string    `json:"market_type"`
	ObservedAt time.Time `json:"observed_at"`

	Collector StatusCollector `json:"collector"`
	Evaluator StatusEvaluator `json:"evaluator"`
	Outcomes  StatusOutcomes  `json:"outcomes"`
	Delivery  StatusDelivery  `json:"delivery"`

	// Concerns is empty rather than absent when there is nothing wrong: a
	// missing field reads as a check that did not run.
	Concerns []StatusConcern `json:"concerns"`

	// Note says what this endpoint can and cannot tell you, because the
	// commonest misreading is treating silence as failure.
	Note string `json:"note"`
}

type StatusCollector struct {
	Reachable   bool   `json:"reachable"`
	State       string `json:"state"`
	WSConnected bool   `json:"ws_connected"`

	StartedAt          *time.Time `json:"started_at"`
	UpdatedAt          *time.Time `json:"updated_at"`
	HeartbeatAgeSecond *float64   `json:"heartbeat_age_seconds"`

	ReconnectCount int32  `json:"reconnect_count"`
	LastDisconnect string `json:"last_disconnect_note"`
}

type StatusEvaluator struct {
	// Configured false is a configuration, not a fault. It is the difference
	// between a pipeline switched off and one stuck warming up, which produce
	// identical silence.
	Configured bool   `json:"configured"`
	Strategy   string `json:"strategy"`
	Timeframe  string `json:"timeframe"`

	Ready  bool   `json:"ready"`
	Reason string `json:"reason"`

	LastSignalAt        *time.Time `json:"last_signal_at"`
	LastSignalAgeSecond *float64   `json:"last_signal_age_seconds"`
	SignalsTotal        int64      `json:"signals_total"`
}

type StatusOutcomes struct {
	Open int64 `json:"open"`

	OldestOpenAt        *time.Time `json:"oldest_open_at"`
	OldestOpenAgeSecond *float64   `json:"oldest_open_age_seconds"`

	Missing int64 `json:"missing_outcome_rows"`
}

type StatusDelivery struct {
	Mode    string `json:"mode"`
	Pending int64  `json:"pending"`
	Sent    int64  `json:"sent"`
	Failed  int64  `json:"failed"`

	LastSentAt *time.Time `json:"last_sent_at"`
}

type StatusConcern struct {
	Component string `json:"component"`
	Detail    string `json:"detail"`
}

// ToStatusResponse renders the status.
func ToStatusResponse(s Status) StatusResponse {
	concerns := make([]StatusConcern, 0, len(s.Concerns))
	for _, c := range s.Concerns {
		concerns = append(concerns, StatusConcern{Component: c.Component, Detail: c.Detail})
	}

	return StatusResponse{
		Symbol:     s.Symbol,
		MarketType: s.MarketType,
		ObservedAt: s.ObservedAt.UTC(),
		Collector: StatusCollector{
			Reachable:          s.Collector.Reachable,
			State:              s.Collector.State,
			WSConnected:        s.Collector.WSConnected,
			StartedAt:          s.Collector.StartedAt,
			UpdatedAt:          s.Collector.UpdatedAt,
			HeartbeatAgeSecond: statusSeconds(s.Collector.HeartbeatAge),
			ReconnectCount:     s.Collector.ReconnectCount,
			LastDisconnect:     s.Collector.LastDisconnect,
		},
		Evaluator: StatusEvaluator{
			Configured:          s.Evaluator.Configured,
			Strategy:            s.Evaluator.Strategy,
			Timeframe:           s.Evaluator.Timeframe,
			Ready:               s.Evaluator.Ready,
			Reason:              s.Evaluator.Reason,
			LastSignalAt:        s.Evaluator.LastSignalAt,
			LastSignalAgeSecond: statusSeconds(s.Evaluator.LastSignalAge),
			SignalsTotal:        s.Evaluator.SignalsTotal,
		},
		Outcomes: StatusOutcomes{
			Open:                s.Outcomes.Open,
			OldestOpenAt:        s.Outcomes.OldestOpenAt,
			OldestOpenAgeSecond: statusSeconds(s.Outcomes.OldestOpenAge),
			Missing:             s.Outcomes.Missing,
		},
		Delivery: StatusDelivery{
			Mode:       s.Delivery.Mode,
			Pending:    s.Delivery.Pending,
			Sent:       s.Delivery.Sent,
			Failed:     s.Delivery.Failed,
			LastSentAt: s.Delivery.LastSentAt,
		},
		Concerns: concerns,
		Note: "Silence is the normal output of this pipeline. A strategy at a tenth of a " +
			"signal a day is quiet for weeks by design, so an old last_signal_at is not " +
			"itself a fault — read it beside evaluator.ready and evaluator.reason, which " +
			"say whether the strategy is deciding at all.",
	}
}

// seconds renders a duration for a client that should not parse Go's format.
func statusSeconds(d *time.Duration) *float64 {
	if d == nil {
		return nil
	}
	value := d.Seconds()
	return &value
}
