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
	Ingestion StatusIngestion `json:"ingestion"`
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

// StatusIngestion is the state of the candle series everything is built from.
type StatusIngestion struct {
	// UnfilledGaps is the total awaiting backfill; Timeframes breaks it down.
	// Both, because a total answers "is the data whole" and the breakdown
	// answers "which series is stuck".
	UnfilledGaps int64                 `json:"unfilled_gaps"`
	Timeframes   []StatusTimeframeGaps `json:"timeframes"`
}

// StatusTimeframeGaps is one series' unfilled gap count.
type StatusTimeframeGaps struct {
	Timeframe    string `json:"timeframe"`
	UnfilledGaps int64  `json:"unfilled_gaps"`
}

type StatusDelivery struct {
	Mode    string `json:"mode"`
	Pending int64  `json:"pending"`
	Sent    int64  `json:"sent"`
	Failed  int64  `json:"failed"`

	LastSentAt *time.Time `json:"last_sent_at"`

	// DevicesRegistered is how many phones can be delivered to: zero or one,
	// since the devices table holds a single row.
	//
	// Zero while mode is notify is the case the concerns list spells out in
	// words. It is not left as a bare number for the reader to interpret:
	// everything looks configured in that state, and the signals are being
	// recorded and queued and going nowhere.
	DevicesRegistered int64 `json:"devices_registered"`
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
		Ingestion: StatusIngestion{
			UnfilledGaps: s.Ingestion.UnfilledGaps,
			Timeframes:   timeframeGaps(s.Ingestion.Timeframes),
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
			Mode:              s.Delivery.Mode,
			Pending:           s.Delivery.Pending,
			Sent:              s.Delivery.Sent,
			Failed:            s.Delivery.Failed,
			LastSentAt:        s.Delivery.LastSentAt,
			DevicesRegistered: s.Delivery.DevicesRegistered,
		},
		Concerns: concerns,
		Note: "Silence is the normal output of this pipeline. A strategy at a tenth of a " +
			"signal a day is quiet for weeks by design, so an old last_signal_at is not " +
			"itself a fault — read it beside evaluator.ready and evaluator.reason, which " +
			"say whether the strategy is deciding at all.",
	}
}

// timeframeGaps renders the per-series breakdown, empty rather than null so a
// client handles one shape.
func timeframeGaps(in []TimeframeGaps) []StatusTimeframeGaps {
	out := make([]StatusTimeframeGaps, 0, len(in))
	for _, tf := range in {
		out = append(out, StatusTimeframeGaps{
			Timeframe: tf.Timeframe, UnfilledGaps: tf.UnfilledGaps,
		})
	}
	return out
}

// statusSeconds renders a duration for a client that should not parse Go's
// format.
func statusSeconds(d *time.Duration) *float64 {
	if d == nil {
		return nil
	}
	value := d.Seconds()
	return &value
}
