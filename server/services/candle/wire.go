package candle

import (
	"time"

	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
)

// CandleResponse is how a candle appears on the wire.
//
// # Why it lives here rather than in the handler
//
// Two endpoints send candles: GET /api/v1/candles and the websocket. If each
// rendered its own, the same bar would reach the app under two sets of field
// names, and the one field that matters — is_closed — would be the easiest to
// diverge on. One shape, one renderer, both callers.
type CandleResponse struct {
	OpenTime  time.Time `json:"open_time"`
	CloseTime time.Time `json:"close_time"`

	// Prices are strings. A phone parsing 0.1 + 0.2 is the same hazard as a
	// server doing it, and a float64 cannot hold every numeric(20,8).
	Open   string `json:"open"`
	High   string `json:"high"`
	Low    string `json:"low"`
	Close  string `json:"close"`
	Volume string `json:"volume"`

	// IsClosed is always true on the REST endpoint — only closed candles are
	// stored — and may be false on the stream, which is the only place in the
	// system permitted to send a bar that has not closed (CLAUDE.md §3.1).
	//
	// It is carried on both so a client reads one field in one place. A
	// consumer that ignores it is charting a price that can still change,
	// which is legitimate for display and for nothing else.
	IsClosed bool `json:"is_closed"`
}

// ToCandleResponse renders one candle for the API and the stream alike.
func ToCandleResponse(c models.Candle) CandleResponse {
	return CandleResponse{
		OpenTime:  c.OpenTime.UTC(),
		CloseTime: c.CloseTime.UTC(),
		Open:      c.Open.String(),
		High:      c.High.String(),
		Low:       c.Low.String(),
		Close:     c.Close.String(),
		Volume:    c.Volume.String(),
		IsClosed:  c.IsClosed,
	}
}

// ToCandleResponses renders a page of candles.
func ToCandleResponses(candles []models.Candle) []CandleResponse {
	out := make([]CandleResponse, 0, len(candles))
	for _, c := range candles {
		out = append(out, ToCandleResponse(c))
	}
	return out
}
