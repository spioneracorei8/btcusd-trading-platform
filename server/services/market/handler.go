package market

import "net/http"

// MarketHandler serves the market data status endpoint.
type MarketHandler interface {
	// Status answers GET /internal/market/status.
	Status(w http.ResponseWriter, r *http.Request)
}
