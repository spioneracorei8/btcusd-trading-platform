package health

import (
	"context"

	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
)

// HealthUsecase answers the two questions an orchestrator asks.
type HealthUsecase interface {
	// Liveness reports whether the process itself is running. It must not
	// touch the database: a liveness probe should not restart a working API
	// because PostgreSQL blipped.
	Liveness() models.Health

	// Readiness reports whether the API can actually serve traffic, which
	// includes reaching the database.
	Readiness(ctx context.Context) (models.Health, error)
}
