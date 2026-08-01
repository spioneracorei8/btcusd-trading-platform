// Package health declares the contracts behind the liveness and readiness
// endpoints. It is the one service with an HTTP handler in phase 01, and it
// shows the shape every later service follows: handler -> usecase ->
// repository, each depending only on the interface below it.
package health

import "context"

// HealthRepository reports whether the infrastructure the API depends on is
// reachable.
type HealthRepository interface {
	// PingDatabase returns nil when the database answers.
	PingDatabase(ctx context.Context) error
}
