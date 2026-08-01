package models

// Health is the body returned by the liveness and readiness endpoints.
type Health struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// ErrorResponse is the body returned when a request cannot be served.
type ErrorResponse struct {
	Error string `json:"error"`
}
