package repository

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/database"
	"github.com/spioneracorei8/btcusd-trading-platform/server/database/db"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/pipeline"
)

type pipelineRepository struct {
	queries *db.Queries
}

// NewPipelineRepoImpl builds the pipeline reader on a pgx pool.
func NewPipelineRepoImpl(pool *pgxpool.Pool) pipeline.PipelineRepository {
	return &pipelineRepository{queries: db.New(pool)}
}

// SignalActivity reports what the signal and outcome stages have been doing.
func (r *pipelineRepository) SignalActivity(
	ctx context.Context, symbol string, marketType constants.MarketType,
) (pipeline.SignalActivity, error) {
	row, err := r.queries.PipelineSignalActivity(ctx, db.PipelineSignalActivityParams{
		Symbol: symbol, MarketType: marketType.String(),
	})
	if err != nil {
		return pipeline.SignalActivity{}, fmt.Errorf("pipeline signal activity: %w", err)
	}

	return pipeline.SignalActivity{
		LastSignalAt:       database.TimeFromTimestamptz(row.LastSignalAt),
		SignalsTotal:       row.SignalsTotal,
		OutcomesOpen:       row.OutcomesOpen,
		OldestOpenSignalAt: database.TimeFromTimestamptz(row.OldestOpenSignalAt),
		OutcomesMissing:    row.OutcomesMissing,
	}, nil
}

// DeliveryActivity reports the delivery queue by state.
func (r *pipelineRepository) DeliveryActivity(
	ctx context.Context, symbol string, marketType constants.MarketType,
) (pipeline.DeliveryActivity, error) {
	row, err := r.queries.PipelineDeliveryActivity(ctx, db.PipelineDeliveryActivityParams{
		Symbol: symbol, MarketType: marketType.String(),
	})
	if err != nil {
		return pipeline.DeliveryActivity{}, fmt.Errorf("pipeline delivery activity: %w", err)
	}

	return pipeline.DeliveryActivity{
		Pending:           row.Pending,
		Sent:              row.Sent,
		Failed:            row.Failed,
		LastSentAt:        database.TimeFromTimestamptz(row.LastSentAt),
		DevicesRegistered: row.DevicesRegistered,
	}, nil
}
