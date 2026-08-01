package health

import "net/http"

// HealthHandler serves the health endpoints over HTTP.
type HealthHandler interface {
	Liveness(w http.ResponseWriter, r *http.Request)
	Readiness(w http.ResponseWriter, r *http.Request)
}
