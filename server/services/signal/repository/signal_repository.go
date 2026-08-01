package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

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
