package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/database"
	"github.com/spioneracorei8/btcusd-trading-platform/server/database/db"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/outcome"
)

type outcomeRepository struct {
	queries *db.Queries
}

// NewOutcomeRepoImpl builds the outcome repository on a pgx pool.
func NewOutcomeRepoImpl(pool *pgxpool.Pool) outcome.OutcomeRepository {
	return &outcomeRepository{queries: db.New(pool)}
}

// EnsureOutcomes opens a row for every signal that has none.
func (r *outcomeRepository) EnsureOutcomes(
	ctx context.Context, symbol string, marketType constants.MarketType, limit int32,
) ([]models.SignalOutcome, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("ensure outcomes: limit %d is not positive", limit)
	}

	rows, err := r.queries.EnsureSignalOutcomes(ctx, db.EnsureSignalOutcomesParams{
		Symbol:     symbol,
		MarketType: marketType.String(),
		RowLimit:   limit,
	})
	if err != nil {
		return nil, fmt.Errorf("ensure outcomes: %w", err)
	}

	out := make([]models.SignalOutcome, 0, len(rows))
	for _, row := range rows {
		o, err := toOutcomeModel(row)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, nil
}

// FetchOpen returns signals still being followed.
func (r *outcomeRepository) FetchOpen(
	ctx context.Context, symbol string, marketType constants.MarketType, limit int32,
) ([]models.SignalOutcome, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("fetch open outcomes: limit %d is not positive", limit)
	}

	rows, err := r.queries.FetchOpenSignalOutcomes(ctx, db.FetchOpenSignalOutcomesParams{
		Symbol:     symbol,
		MarketType: marketType.String(),
		RowLimit:   limit,
	})
	if err != nil {
		return nil, fmt.Errorf("fetch open outcomes: %w", err)
	}

	out := make([]models.SignalOutcome, 0, len(rows))
	for _, row := range rows {
		o, err := toOutcomeModel(row)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, nil
}

// SaveOutcome records progress or a resolution.
func (r *outcomeRepository) SaveOutcome(
	ctx context.Context, o models.SignalOutcome,
) (models.SignalOutcome, error) {
	row, err := r.queries.UpdateSignalOutcome(ctx, db.UpdateSignalOutcomeParams{
		SignalID:          database.PgtypeFromUUID(o.SignalId),
		Status:            o.Status.String(),
		ResolvedAt:        database.NullTimestamptzFromTimePtr(o.ResolvedAt),
		ResolvedPrice:     database.NullNumericFromDecimal(o.ResolvedPrice),
		Mae:               database.NullNumericFromDecimal(o.MAE),
		Mfe:               database.NullNumericFromDecimal(o.MFE),
		BarsHeld:          o.BarsHeld,
		BacktestWouldHave: o.BacktestWouldHave,
		DivergenceNote:    o.DivergenceNote,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// The UPDATE matched nothing, so the caller believes it just recorded
		// an outcome that nothing recorded.
		return models.SignalOutcome{}, fmt.Errorf("save outcome: no outcome for signal %s", o.SignalId)
	}
	if err != nil {
		return models.SignalOutcome{}, fmt.Errorf("save outcome %s: %w", o.SignalId, err)
	}
	return toOutcomeModel(row)
}

// FetchOutcome returns one outcome.
func (r *outcomeRepository) FetchOutcome(
	ctx context.Context, signalId uuid.UUID,
) (models.SignalOutcome, error) {
	row, err := r.queries.FetchSignalOutcome(ctx, database.PgtypeFromUUID(signalId))
	if errors.Is(err, pgx.ErrNoRows) {
		return models.SignalOutcome{}, constants.ErrNotFound
	}
	if err != nil {
		return models.SignalOutcome{}, fmt.Errorf("fetch outcome %s: %w", signalId, err)
	}
	return toOutcomeModel(row)
}

// ListOutcomes returns a page of outcomes with their signals.
func (r *outcomeRepository) ListOutcomes(
	ctx context.Context, params outcome.ListParams,
) ([]models.SignalOutcome, int64, error) {
	status := pgtype.Text{}
	if params.Status != "" {
		status = pgtype.Text{String: params.Status.String(), Valid: true}
	}

	rows, err := r.queries.ListSignalOutcomes(ctx, db.ListSignalOutcomesParams{
		Symbol:     params.Symbol,
		MarketType: params.MarketType.String(),
		Status:     status,
		FromTime:   database.TimestamptzFromTime(params.From),
		ToTime:     database.TimestamptzFromTime(params.To),
		RowLimit:   params.Limit,
		RowOffset:  params.Offset,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("list outcomes: %w", err)
	}

	total, err := r.queries.CountSignalOutcomes(ctx, db.CountSignalOutcomesParams{
		Symbol:     params.Symbol,
		MarketType: params.MarketType.String(),
		Status:     status,
		FromTime:   database.TimestamptzFromTime(params.From),
		ToTime:     database.TimestamptzFromTime(params.To),
	})
	if err != nil {
		return nil, 0, fmt.Errorf("count outcomes: %w", err)
	}

	out := make([]models.SignalOutcome, 0, len(rows))
	for _, row := range rows {
		o, err := toOutcomeModel(row)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, o)
	}
	return out, total, nil
}

// toOutcomeModel converts one row, refusing a status the enum does not know
// rather than carrying it up as a string nothing will match on.
func toOutcomeModel(row db.SignalOutcome) (models.SignalOutcome, error) {
	signalId, err := database.UUIDFromPgtype(row.SignalID)
	if err != nil {
		return models.SignalOutcome{}, fmt.Errorf("outcome: signal_id: %w", err)
	}
	status, err := constants.ParseOutcomeStatus(row.Status)
	if err != nil {
		return models.SignalOutcome{}, fmt.Errorf("outcome %s: %w", signalId, err)
	}

	resolvedPrice, err := database.NullDecimalFromNumeric(row.ResolvedPrice)
	if err != nil {
		return models.SignalOutcome{}, fmt.Errorf("outcome %s: resolved_price: %w", signalId, err)
	}
	mae, err := database.NullDecimalFromNumeric(row.Mae)
	if err != nil {
		return models.SignalOutcome{}, fmt.Errorf("outcome %s: mae: %w", signalId, err)
	}
	mfe, err := database.NullDecimalFromNumeric(row.Mfe)
	if err != nil {
		return models.SignalOutcome{}, fmt.Errorf("outcome %s: mfe: %w", signalId, err)
	}

	return models.SignalOutcome{
		SignalId:          signalId,
		Status:            status,
		ResolvedAt:        database.TimePtrFromTimestamptz(row.ResolvedAt),
		ResolvedPrice:     resolvedPrice,
		MAE:               mae,
		MFE:               mfe,
		BarsHeld:          row.BarsHeld,
		BacktestWouldHave: row.BacktestWouldHave,
		DivergenceNote:    row.DivergenceNote,
		CreatedAt:         database.TimeFromTimestamptz(row.CreatedAt),
		UpdatedAt:         database.TimeFromTimestamptz(row.UpdatedAt),
	}, nil
}
