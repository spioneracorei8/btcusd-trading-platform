package storage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/spioneracorei8/btcusd-trading-platform/backend/internal/domain"
	"github.com/spioneracorei8/btcusd-trading-platform/backend/internal/storage/db"
)

// pgUniqueViolation is the SQLSTATE PostgreSQL raises for a unique constraint.
const pgUniqueViolation = "23505"

// Store is the repository the rest of the system talks to. It translates
// between domain types and the sqlc-generated query layer.
type Store struct {
	pool    *pgxpool.Pool
	queries *db.Queries
}

// New builds a Store on top of an existing pgx pool.
func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, queries: db.New(pool)}
}

// Pool exposes the underlying pool for callers that need it (health checks,
// migrations in tests). It is not a general escape hatch for ad-hoc SQL.
func (s *Store) Pool() *pgxpool.Pool { return s.pool }

// Ping verifies that the database answers. It is what /ready reports on.
func (s *Store) Ping(ctx context.Context) error {
	if err := s.pool.Ping(ctx); err != nil {
		return fmt.Errorf("ping database: %w", err)
	}
	return nil
}

// UpsertCandle writes one closed candle, replacing any existing bar with the
// same (symbol, market_type, timeframe, open_time).
//
// Writing the same candle twice is expected — a WebSocket reconnect and a
// REST backfill routinely overlap — and must never produce a second row.
func (s *Store) UpsertCandle(ctx context.Context, c domain.Candle) error {
	if !c.IsClosed {
		return fmt.Errorf("storage: refusing to store an unclosed candle for %s %s %s at %s",
			c.Symbol, c.MarketType, c.Timeframe, c.OpenTime.UTC().Format(time.RFC3339))
	}

	err := s.queries.UpsertCandle(ctx, db.UpsertCandleParams{
		Symbol:      c.Symbol,
		MarketType:  c.MarketType.String(),
		Timeframe:   c.Timeframe.String(),
		OpenTime:    timestamptzFromTime(c.OpenTime),
		CloseTime:   timestamptzFromTime(c.CloseTime),
		Open:        numericFromDecimal(c.Open),
		High:        numericFromDecimal(c.High),
		Low:         numericFromDecimal(c.Low),
		Close:       numericFromDecimal(c.Close),
		Volume:      numericFromDecimal(c.Volume),
		QuoteVolume: numericFromDecimal(c.QuoteVolume),
		TradeCount:  c.TradeCount,
		IsClosed:    c.IsClosed,
	})
	if err != nil {
		return fmt.Errorf("upsert candle: %w", err)
	}
	return nil
}

// GetCandlesParams selects a closed window of candles.
type GetCandlesParams struct {
	Symbol     string
	MarketType domain.MarketType
	Timeframe  domain.Timeframe
	// From and To bound open_time inclusively.
	From time.Time
	To   time.Time
}

// GetCandles returns the stored candles of a window, oldest first.
func (s *Store) GetCandles(ctx context.Context, p GetCandlesParams) ([]domain.Candle, error) {
	rows, err := s.queries.GetCandles(ctx, db.GetCandlesParams{
		Symbol:     p.Symbol,
		MarketType: p.MarketType.String(),
		Timeframe:  p.Timeframe.String(),
		FromTime:   timestamptzFromTime(p.From),
		ToTime:     timestamptzFromTime(p.To),
	})
	if err != nil {
		return nil, fmt.Errorf("get candles: %w", err)
	}

	out := make([]domain.Candle, 0, len(rows))
	for _, row := range rows {
		candle, err := candleFromRow(row)
		if err != nil {
			return nil, fmt.Errorf("get candles: %w", err)
		}
		out = append(out, candle)
	}
	return out, nil
}

// GetLatestCandle returns the newest stored candle for a series.
// It returns ErrNotFound when the series is still empty.
func (s *Store) GetLatestCandle(ctx context.Context, symbol string, marketType domain.MarketType, timeframe domain.Timeframe) (domain.Candle, error) {
	row, err := s.queries.GetLatestCandle(ctx, db.GetLatestCandleParams{
		Symbol:     symbol,
		MarketType: marketType.String(),
		Timeframe:  timeframe.String(),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Candle{}, ErrNotFound
	}
	if err != nil {
		return domain.Candle{}, fmt.Errorf("get latest candle: %w", err)
	}

	candle, err := candleFromRow(row)
	if err != nil {
		return domain.Candle{}, fmt.Errorf("get latest candle: %w", err)
	}
	return candle, nil
}

// CountCandles returns how many candles are stored for a series.
func (s *Store) CountCandles(ctx context.Context, symbol string, marketType domain.MarketType, timeframe domain.Timeframe) (int64, error) {
	n, err := s.queries.CountCandles(ctx, db.CountCandlesParams{
		Symbol:     symbol,
		MarketType: marketType.String(),
		Timeframe:  timeframe.String(),
	})
	if err != nil {
		return 0, fmt.Errorf("count candles: %w", err)
	}
	return n, nil
}

// InsertSignal stores one strategy decision and returns the persisted row.
//
// It returns ErrDuplicateSignal when the same strategy version already
// emitted a signal for that candle, which is how repeat notifications are
// prevented.
func (s *Store) InsertSignal(ctx context.Context, sig domain.Signal) (domain.Signal, error) {
	reason := sig.Reason
	if len(reason) == 0 {
		reason = []byte("{}")
	}

	row, err := s.queries.InsertSignal(ctx, db.InsertSignalParams{
		Symbol:          sig.Symbol,
		MarketType:      sig.MarketType.String(),
		Timeframe:       sig.Timeframe.String(),
		SignalTime:      timestamptzFromTime(sig.SignalTime),
		Direction:       sig.Direction.String(),
		Strength:        numericFromDecimal(sig.Strength),
		EntryPrice:      nullNumericFromDecimal(sig.EntryPrice),
		StopLoss:        nullNumericFromDecimal(sig.StopLoss),
		TakeProfit:      nullNumericFromDecimal(sig.TakeProfit),
		StrategyName:    sig.StrategyName,
		StrategyVersion: sig.StrategyVersion,
		Reason:          reason,
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation {
			return domain.Signal{}, ErrDuplicateSignal
		}
		return domain.Signal{}, fmt.Errorf("insert signal: %w", err)
	}

	out, err := signalFromRow(row)
	if err != nil {
		return domain.Signal{}, fmt.Errorf("insert signal: %w", err)
	}
	return out, nil
}

// InsertGap records a detected hole in a candle series and returns the row.
func (s *Store) InsertGap(ctx context.Context, gap domain.DataGap) (domain.DataGap, error) {
	row, err := s.queries.InsertGap(ctx, db.InsertGapParams{
		Symbol:     gap.Symbol,
		MarketType: gap.MarketType.String(),
		Timeframe:  gap.Timeframe.String(),
		GapStart:   timestamptzFromTime(gap.GapStart),
		GapEnd:     timestamptzFromTime(gap.GapEnd),
		Note:       gap.Note,
	})
	if err != nil {
		return domain.DataGap{}, fmt.Errorf("insert gap: %w", err)
	}

	out, err := gapFromRow(row)
	if err != nil {
		return domain.DataGap{}, fmt.Errorf("insert gap: %w", err)
	}
	return out, nil
}

// candleFromRow maps a generated row onto the domain type.
func candleFromRow(row db.Candle) (domain.Candle, error) {
	marketType, err := domain.ParseMarketType(row.MarketType)
	if err != nil {
		return domain.Candle{}, fmt.Errorf("candle %s %s: %w", row.Symbol, row.Timeframe, err)
	}
	timeframe, err := domain.ParseTimeframe(row.Timeframe)
	if err != nil {
		return domain.Candle{}, fmt.Errorf("candle %s: %w", row.Symbol, err)
	}

	candle := domain.Candle{
		Symbol:     row.Symbol,
		MarketType: marketType,
		Timeframe:  timeframe,
		OpenTime:   timeFromTimestamptz(row.OpenTime),
		CloseTime:  timeFromTimestamptz(row.CloseTime),
		TradeCount: row.TradeCount,
		IsClosed:   row.IsClosed,
	}

	// candleErr keeps the failing column and bar identifiable in the log.
	candleErr := func(column string, err error) error {
		return fmt.Errorf("candle %s %s %s %s: %s: %w",
			row.Symbol, row.MarketType, row.Timeframe,
			candle.OpenTime.Format(time.RFC3339), column, err)
	}

	for _, field := range []struct {
		column string
		src    pgtype.Numeric
		dst    *decimal.Decimal
	}{
		{"open", row.Open, &candle.Open},
		{"high", row.High, &candle.High},
		{"low", row.Low, &candle.Low},
		{"close", row.Close, &candle.Close},
		{"volume", row.Volume, &candle.Volume},
		{"quote_volume", row.QuoteVolume, &candle.QuoteVolume},
	} {
		value, err := decimalFromNumeric(field.src)
		if err != nil {
			return domain.Candle{}, candleErr(field.column, err)
		}
		*field.dst = value
	}
	return candle, nil
}

// signalFromRow maps a generated row onto the domain type.
func signalFromRow(row db.Signal) (domain.Signal, error) {
	id, err := uuidFromPgtype(row.ID)
	if err != nil {
		return domain.Signal{}, fmt.Errorf("signal id: %w", err)
	}
	marketType, err := domain.ParseMarketType(row.MarketType)
	if err != nil {
		return domain.Signal{}, fmt.Errorf("signal %s: %w", id, err)
	}
	timeframe, err := domain.ParseTimeframe(row.Timeframe)
	if err != nil {
		return domain.Signal{}, fmt.Errorf("signal %s: %w", id, err)
	}
	direction, err := domain.ParseDirection(row.Direction)
	if err != nil {
		return domain.Signal{}, fmt.Errorf("signal %s: %w", id, err)
	}
	strength, err := decimalFromNumeric(row.Strength)
	if err != nil {
		return domain.Signal{}, fmt.Errorf("signal %s: strength: %w", id, err)
	}
	entry, err := nullDecimalFromNumeric(row.EntryPrice)
	if err != nil {
		return domain.Signal{}, fmt.Errorf("signal %s: entry_price: %w", id, err)
	}
	stop, err := nullDecimalFromNumeric(row.StopLoss)
	if err != nil {
		return domain.Signal{}, fmt.Errorf("signal %s: stop_loss: %w", id, err)
	}
	target, err := nullDecimalFromNumeric(row.TakeProfit)
	if err != nil {
		return domain.Signal{}, fmt.Errorf("signal %s: take_profit: %w", id, err)
	}

	return domain.Signal{
		ID:              id,
		Symbol:          row.Symbol,
		MarketType:      marketType,
		Timeframe:       timeframe,
		SignalTime:      timeFromTimestamptz(row.SignalTime),
		Direction:       direction,
		Strength:        strength,
		EntryPrice:      entry,
		StopLoss:        stop,
		TakeProfit:      target,
		StrategyName:    row.StrategyName,
		StrategyVersion: row.StrategyVersion,
		Reason:          row.Reason,
		CreatedAt:       timeFromTimestamptz(row.CreatedAt),
	}, nil
}

// gapFromRow maps a generated row onto the domain type.
func gapFromRow(row db.DataGap) (domain.DataGap, error) {
	marketType, err := domain.ParseMarketType(row.MarketType)
	if err != nil {
		return domain.DataGap{}, fmt.Errorf("data gap %d: %w", row.ID, err)
	}
	timeframe, err := domain.ParseTimeframe(row.Timeframe)
	if err != nil {
		return domain.DataGap{}, fmt.Errorf("data gap %d: %w", row.ID, err)
	}

	return domain.DataGap{
		ID:         row.ID,
		Symbol:     row.Symbol,
		MarketType: marketType,
		Timeframe:  timeframe,
		GapStart:   timeFromTimestamptz(row.GapStart),
		GapEnd:     timeFromTimestamptz(row.GapEnd),
		DetectedAt: timeFromTimestamptz(row.DetectedAt),
		FilledAt:   timePtrFromTimestamptz(row.FilledAt),
		Note:       row.Note,
	}, nil
}
