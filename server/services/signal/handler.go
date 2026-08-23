package signal

import "net/http"

// SignalHandler serves recorded signals.
type SignalHandler interface {
	// Signals answers GET /api/v1/signals.
	Signals(w http.ResponseWriter, r *http.Request)

	// Signal answers GET /api/v1/signals/{id}, with the full reason payload.
	Signal(w http.ResponseWriter, r *http.Request)
}
