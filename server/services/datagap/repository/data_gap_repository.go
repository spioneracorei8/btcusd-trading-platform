package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

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
		Id:           row.ID,
		Symbol:       row.Symbol,
		MarketType:   marketType,
		Timeframe:    timeframe,
		GapStart:     database.TimeFromTimestamptz(row.GapStart),
		GapEnd:       database.TimeFromTimestamptz(row.GapEnd),
		DetectedAt:   database.TimeFromTimestamptz(row.DetectedAt),
		FilledAt:     database.TimePtrFromTimestamptz(row.FilledAt),
		FillAttempts: row.FillAttempts,
		Note:         row.Note,
	}, nil
}

func (r *dataGapRepository) MarkFilled(ctx context.Context, id int64) error {
	if err := r.queries.MarkGapFilled(ctx, id); err != nil {
		return fmt.Errorf("mark gap %d filled: %w", id, err)
	}
	return nil
}

func (r *dataGapRepository) RecordFillAttempt(ctx context.Context, id int64, note string) (models.DataGap, error) {
	row, err := r.queries.RecordGapFillAttempt(ctx, db.RecordGapFillAttemptParams{
		ID:   id,
		Note: note,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return models.DataGap{}, constants.ErrNotFound
	}
	if err != nil {
		return models.DataGap{}, fmt.Errorf("record fill attempt for gap %d: %w", id, err)
	}

	gap, err := toDataGapModel(row)
	if err != nil {
		return models.DataGap{}, fmt.Errorf("record fill attempt for gap %d: %w", id, err)
	}
	return gap, nil
}

func (r *dataGapRepository) ListUnfilled(ctx context.Context, symbol string, marketType constants.MarketType, timeframe constants.Timeframe, maxAttempts int32) ([]models.DataGap, error) {
	rows, err := r.queries.ListUnfilledGaps(ctx, db.ListUnfilledGapsParams{
		Symbol:      symbol,
		MarketType:  marketType.String(),
		Timeframe:   timeframe.String(),
		MaxAttempts: maxAttempts,
	})
	if err != nil {
		return nil, fmt.Errorf("list unfilled gaps: %w", err)
	}

	gaps := make([]models.DataGap, 0, len(rows))
	for _, row := range rows {
		gap, err := toDataGapModel(row)
		if err != nil {
			return nil, fmt.Errorf("list unfilled gaps: %w", err)
		}
		gaps = append(gaps, gap)
	}
	return gaps, nil
}

func (r *dataGapRepository) CountUnfilled(ctx context.Context, symbol string, marketType constants.MarketType, timeframe constants.Timeframe) (int64, error) {
	n, err := r.queries.CountUnfilledGaps(ctx, db.CountUnfilledGapsParams{
		Symbol:     symbol,
		MarketType: marketType.String(),
		Timeframe:  timeframe.String(),
	})
	if err != nil {
		return 0, fmt.Errorf("count unfilled gaps: %w", err)
	}
	return n, nil
}
