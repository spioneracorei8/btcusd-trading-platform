package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/database"
	"github.com/spioneracorei8/btcusd-trading-platform/server/database/db"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/signal"
)

type signalRepository struct {
	queries *db.Queries
}

// NewSignalRepoImpl builds the signal repository on a pgx pool.
func NewSignalRepoImpl(pool *pgxpool.Pool) signal.SignalRepository {
	return &signalRepository{
		queries: db.New(pool),
	}
}

// InsertSignal stores one strategy decision.
//
// A unique violation is translated into constants.ErrDuplicateSignal rather
// than surfaced as a database error: hitting it is expected behaviour on a
// restart or a replay, not a fault.
func (r *signalRepository) InsertSignal(ctx context.Context, s models.Signal) (models.Signal, error) {
	reason := s.Reason
	if len(reason) == 0 {
		reason = []byte("{}")
	}

	row, err := r.queries.InsertSignal(ctx, db.InsertSignalParams{
		Symbol:          s.Symbol,
		MarketType:      s.MarketType.String(),
		Timeframe:       s.Timeframe.String(),
		SignalTime:      database.TimestamptzFromTime(s.SignalTime),
		Direction:       s.Direction.String(),
		Strength:        database.NumericFromDecimal(s.Strength),
		SignalPrice:     database.NullNumericFromDecimal(s.SignalPrice),
		EntryPrice:      database.NullNumericFromDecimal(s.EntryPrice),
		StopLoss:        database.NullNumericFromDecimal(s.StopLoss),
		TakeProfit:      database.NullNumericFromDecimal(s.TakeProfit),
		StrategyName:    s.StrategyName,
		StrategyVersion: s.StrategyVersion,
		Reason:          reason,
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == constants.PgUniqueViolation {
			return models.Signal{}, constants.ErrDuplicateSignal
		}
		return models.Signal{}, fmt.Errorf("insert signal: %w", err)
	}

	out, err := toSignalModel(row)
	if err != nil {
		return models.Signal{}, fmt.Errorf("insert signal: %w", err)
	}
	return out, nil
}

// FetchSignalById returns one signal.
func (r *signalRepository) FetchSignalById(
	ctx context.Context, id uuid.UUID,
) (models.Signal, error) {
	row, err := r.queries.FetchSignalById(ctx, database.PgtypeFromUUID(id))
	if errors.Is(err, pgx.ErrNoRows) {
		return models.Signal{}, constants.ErrNotFound
	}
	if err != nil {
		return models.Signal{}, fmt.Errorf("fetch signal %s: %w", id, err)
	}
	return toSignalModel(row)
}

// SetEntryPrice fills in the entry price, only from null.
func (r *signalRepository) SetEntryPrice(
	ctx context.Context, id uuid.UUID, entry decimal.Decimal,
) (models.Signal, error) {
	row, err := r.queries.SetSignalEntryPrice(ctx, db.SetSignalEntryPriceParams{
		ID:         database.PgtypeFromUUID(id),
		EntryPrice: database.NumericFromDecimal(entry),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// Either there is no such signal, or it already has an entry price.
		// The caller decides which of those matters to it.
		return models.Signal{}, constants.ErrNotFound
	}
	if err != nil {
		return models.Signal{}, fmt.Errorf("set entry price on signal %s: %w", id, err)
	}
	return toSignalModel(row)
}

// ListSignals returns a page of the signal history with its total.
func (r *signalRepository) ListSignals(
	ctx context.Context, params signal.ListParams,
) ([]models.Signal, int64, error) {
	direction := pgtype.Text{}
	if params.Direction != "" {
		direction = pgtype.Text{String: params.Direction.String(), Valid: true}
	}

	rows, err := r.queries.ListSignals(ctx, db.ListSignalsParams{
		Symbol:     params.Symbol,
		MarketType: params.MarketType.String(),
		Direction:  direction,
		RowLimit:   params.Limit,
		RowOffset:  params.Offset,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("list signals: %w", err)
	}

	total, err := r.queries.CountSignals(ctx, db.CountSignalsParams{
		Symbol:     params.Symbol,
		MarketType: params.MarketType.String(),
		Direction:  direction,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("count signals: %w", err)
	}

	out := make([]models.Signal, 0, len(rows))
	for _, row := range rows {
		s, err := toSignalModel(row)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, s)
	}
	return out, total, nil
}

// toSignalModel maps a generated row onto the model.
func toSignalModel(row db.Signal) (models.Signal, error) {
	id, err := database.UUIDFromPgtype(row.ID)
	if err != nil {
		return models.Signal{}, fmt.Errorf("signal id: %w", err)
	}
	marketType, err := constants.ParseMarketType(row.MarketType)
	if err != nil {
		return models.Signal{}, fmt.Errorf("signal %s: %w", id, err)
	}
	timeframe, err := constants.ParseTimeframe(row.Timeframe)
	if err != nil {
		return models.Signal{}, fmt.Errorf("signal %s: %w", id, err)
	}
	direction, err := constants.ParseDirection(row.Direction)
	if err != nil {
		return models.Signal{}, fmt.Errorf("signal %s: %w", id, err)
	}
	strength, err := database.DecimalFromNumeric(row.Strength)
	if err != nil {
		return models.Signal{}, fmt.Errorf("signal %s: strength: %w", id, err)
	}
	signalPrice, err := database.NullDecimalFromNumeric(row.SignalPrice)
	if err != nil {
		return models.Signal{}, fmt.Errorf("signal %s: signal_price: %w", id, err)
	}
	entry, err := database.NullDecimalFromNumeric(row.EntryPrice)
	if err != nil {
		return models.Signal{}, fmt.Errorf("signal %s: entry_price: %w", id, err)
	}
	stop, err := database.NullDecimalFromNumeric(row.StopLoss)
	if err != nil {
		return models.Signal{}, fmt.Errorf("signal %s: stop_loss: %w", id, err)
	}
	target, err := database.NullDecimalFromNumeric(row.TakeProfit)
	if err != nil {
		return models.Signal{}, fmt.Errorf("signal %s: take_profit: %w", id, err)
	}

	return models.Signal{
		Id:              id,
		Symbol:          row.Symbol,
		MarketType:      marketType,
		Timeframe:       timeframe,
		SignalTime:      database.TimeFromTimestamptz(row.SignalTime),
		Direction:       direction,
		SignalPrice:     signalPrice,
		Strength:        strength,
		EntryPrice:      entry,
		StopLoss:        stop,
		TakeProfit:      target,
		StrategyName:    row.StrategyName,
		StrategyVersion: row.StrategyVersion,
		Reason:          row.Reason,
		CreatedAt:       database.TimeFromTimestamptz(row.CreatedAt),
	}, nil
}
