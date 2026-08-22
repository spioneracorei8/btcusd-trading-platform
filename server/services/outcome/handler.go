package outcome

import "net/http"

// OutcomeHandler serves the reconciliation report.
//
// It sits under /internal because it is operational detail rather than
// anything a client should depend on, and because it is expensive: the
// backtest half replays history.
type OutcomeHandler interface {
	// Reconciliation answers GET /internal/signals/reconciliation.
	Reconciliation(w http.ResponseWriter, r *http.Request)
}
