package candle

import "net/http"

// CandleHandler serves stored candles.
type CandleHandler interface {
	// Candles answers GET /api/v1/candles.
	Candles(w http.ResponseWriter, r *http.Request)
}
