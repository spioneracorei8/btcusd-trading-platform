package indicator

import "net/http"

// IndicatorHandler serves indicator values recomputed from stored candles.
type IndicatorHandler interface {
	// Indicators answers GET /api/v1/indicators.
	Indicators(w http.ResponseWriter, r *http.Request)
}
