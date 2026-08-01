package repository

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/spioneracorei8/btcusd-trading-platform/server/services/health"
)

type healthRepository struct {
	pool *pgxpool.Pool
}

// NewHealthRepoImpl builds the health repository on a pgx pool.
func NewHealthRepoImpl(pool *pgxpool.Pool) health.HealthRepository {
	return &healthRepository{
		pool: pool,
	}
}

func (r *healthRepository) PingDatabase(ctx context.Context) error {
	if err := r.pool.Ping(ctx); err != nil {
		return fmt.Errorf("ping database: %w", err)
	}
	return nil
}
