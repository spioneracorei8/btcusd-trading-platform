package repository

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/database"
	"github.com/spioneracorei8/btcusd-trading-platform/server/database/db"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/datagap"
)

type dataGapRepository struct {
	queries *db.Queries
}

// NewDataGapRepoImpl builds the data gap repository on a pgx pool.
func NewDataGapRepoImpl(pool *pgxpool.Pool) datagap.DataGapRepository {
	return &dataGapRepository{
		queries: db.New(pool),
	}
}

func (r *dataGapRepository) InsertGap(ctx context.Context, gap models.DataGap) (models.DataGap, error) {
	row, err := r.queries.InsertGap(ctx, db.InsertGapParams{
		Symbol:     gap.Symbol,
		MarketType: gap.MarketType.String(),
		Timeframe:  gap.Timeframe.String(),
		GapStart:   database.TimestamptzFromTime(gap.GapStart),
		GapEnd:     database.TimestamptzFromTime(gap.GapEnd),
		Note:       gap.Note,
	})
	if err != nil {
		return models.DataGap{}, fmt.Errorf("insert gap: %w", err)
	}

	out, err := toDataGapModel(row)
	if err != nil {
		return models.DataGap{}, fmt.Errorf("insert gap: %w", err)
	}
	return out, nil
}

// toDataGapModel maps a generated row onto the model.
func toDataGapModel(row db.DataGap) (models.DataGap, error) {
	marketType, err := constants.ParseMarketType(row.MarketType)
	if err != nil {
		return models.DataGap{}, fmt.Errorf("data gap %d: %w", row.ID, err)
	}
	timeframe, err := constants.ParseTimeframe(row.Timeframe)
	if err != nil {
		return models.DataGap{}, fmt.Errorf("data gap %d: %w", row.ID, err)
	}

	return models.DataGap{
		Id:         row.ID,
		Symbol:     row.Symbol,
		MarketType: marketType,
		Timeframe:  timeframe,
		GapStart:   database.TimeFromTimestamptz(row.GapStart),
		GapEnd:     database.TimeFromTimestamptz(row.GapEnd),
		DetectedAt: database.TimeFromTimestamptz(row.DetectedAt),
		FilledAt:   database.TimePtrFromTimestamptz(row.FilledAt),
		Note:       row.Note,
	}, nil
}
