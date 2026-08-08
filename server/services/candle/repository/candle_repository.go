package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/database"
	"github.com/spioneracorei8/btcusd-trading-platform/server/database/db"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/candle"
)

type candleRepository struct {
	queries *db.Queries
}

// NewCandleRepoImpl builds the candle repository on a pgx pool.
func NewCandleRepoImpl(pool *pgxpool.Pool) candle.CandleRepository {
	return &candleRepository{
		queries: db.New(pool),
	}
}

// UpsertCandle writes one candle idempotently.
//
// Writing the same candle twice is expected — a WebSocket reconnect and a
// REST backfill routinely overlap — and must never produce a second row.
func (r *candleRepository) UpsertCandle(ctx context.Context, c models.Candle) error {
	err := r.queries.UpsertCandle(ctx, db.UpsertCandleParams{
		Symbol:      c.Symbol,
		MarketType:  c.MarketType.String(),
		Timeframe:   c.Timeframe.String(),
		OpenTime:    database.TimestamptzFromTime(c.OpenTime),
		CloseTime:   database.TimestamptzFromTime(c.CloseTime),
		Open:        database.NumericFromDecimal(c.Open),
		High:        database.NumericFromDecimal(c.High),
		Low:         database.NumericFromDecimal(c.Low),
		Close:       database.NumericFromDecimal(c.Close),
		Volume:      database.NumericFromDecimal(c.Volume),
		QuoteVolume: database.NumericFromDecimal(c.QuoteVolume),
		TradeCount:  c.TradeCount,
		IsClosed:    c.IsClosed,
	})
	if err != nil {
		return fmt.Errorf("upsert candle: %w", err)
	}
	return nil
}

func (r *candleRepository) FetchCandles(ctx context.Context, params candle.FetchCandlesParams) ([]models.Candle, error) {
	rows, err := r.queries.GetCandles(ctx, db.GetCandlesParams{
		Symbol:     params.Symbol,
		MarketType: params.MarketType.String(),
		Timeframe:  params.Timeframe.String(),
		FromTime:   database.TimestamptzFromTime(params.From),
		ToTime:     database.TimestamptzFromTime(params.To),
	})
	if err != nil {
		return nil, fmt.Errorf("fetch candles: %w", err)
	}

	out := make([]models.Candle, 0, len(rows))
	for _, row := range rows {
		c, err := toCandleModel(row)
		if err != nil {
			return nil, fmt.Errorf("fetch candles: %w", err)
		}
		out = append(out, c)
	}
	return out, nil
}

// FetchCandlePage reads one page of a keyset scan.
func (r *candleRepository) FetchCandlePage(ctx context.Context, params candle.FetchCandlePageParams) ([]models.Candle, error) {
	pageSize := params.PageSize
	if pageSize <= 0 {
		pageSize = constants.CandlePageSize
	}

	rows, err := r.queries.GetCandlesAfter(ctx, db.GetCandlesAfterParams{
		Symbol:     params.Symbol,
		MarketType: params.MarketType.String(),
		Timeframe:  params.Timeframe.String(),
		AfterTime:  database.TimestamptzFromTime(params.After),
		ToTime:     database.TimestamptzFromTime(params.To),
		PageSize:   int32(pageSize),
	})
	if err != nil {
		return nil, fmt.Errorf("fetch candle page: %w", err)
	}

	out := make([]models.Candle, 0, len(rows))
	for _, row := range rows {
		c, err := toCandleModel(row)
		if err != nil {
			return nil, fmt.Errorf("fetch candle page: %w", err)
		}
		out = append(out, c)
	}
	return out, nil
}

func (r *candleRepository) FetchLatestCandle(ctx context.Context, symbol string, marketType constants.MarketType, timeframe constants.Timeframe) (models.Candle, error) {
	row, err := r.queries.GetLatestCandle(ctx, db.GetLatestCandleParams{
		Symbol:     symbol,
		MarketType: marketType.String(),
		Timeframe:  timeframe.String(),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return models.Candle{}, constants.ErrNotFound
	}
	if err != nil {
		return models.Candle{}, fmt.Errorf("fetch latest candle: %w", err)
	}

	c, err := toCandleModel(row)
	if err != nil {
		return models.Candle{}, fmt.Errorf("fetch latest candle: %w", err)
	}
	return c, nil
}

func (r *candleRepository) CountCandles(ctx context.Context, symbol string, marketType constants.MarketType, timeframe constants.Timeframe) (int64, error) {
	n, err := r.queries.CountCandles(ctx, db.CountCandlesParams{
		Symbol:     symbol,
		MarketType: marketType.String(),
		Timeframe:  timeframe.String(),
	})
	if err != nil {
		return 0, fmt.Errorf("count candles: %w", err)
	}
	return n, nil
}

// toCandleModel maps a generated row onto the model.
func toCandleModel(row db.Candle) (models.Candle, error) {
	marketType, err := constants.ParseMarketType(row.MarketType)
	if err != nil {
		return models.Candle{}, fmt.Errorf("candle %s %s: %w", row.Symbol, row.Timeframe, err)
	}
	timeframe, err := constants.ParseTimeframe(row.Timeframe)
	if err != nil {
		return models.Candle{}, fmt.Errorf("candle %s: %w", row.Symbol, err)
	}

	c := models.Candle{
		Symbol:     row.Symbol,
		MarketType: marketType,
		Timeframe:  timeframe,
		OpenTime:   database.TimeFromTimestamptz(row.OpenTime),
		CloseTime:  database.TimeFromTimestamptz(row.CloseTime),
		TradeCount: row.TradeCount,
		IsClosed:   row.IsClosed,
	}

	// candleErr keeps the failing column and bar identifiable in the log.
	candleErr := func(column string, err error) error {
		return fmt.Errorf("candle %s %s %s %s: %s: %w",
			row.Symbol, row.MarketType, row.Timeframe,
			c.OpenTime.Format(time.RFC3339), column, err)
	}

	for _, field := range []struct {
		column string
		src    pgtype.Numeric
		dst    *decimal.Decimal
	}{
		{"open", row.Open, &c.Open},
		{"high", row.High, &c.High},
		{"low", row.Low, &c.Low},
		{"close", row.Close, &c.Close},
		{"volume", row.Volume, &c.Volume},
		{"quote_volume", row.QuoteVolume, &c.QuoteVolume},
	} {
		value, err := database.DecimalFromNumeric(field.src)
		if err != nil {
			return models.Candle{}, candleErr(field.column, err)
		}
		*field.dst = value
	}
	return c, nil
}

// UpsertCandles writes many candles using one batched round trip per chunk.
//
// Backfilling three years of 1m candles is roughly 1.5 million rows; a
// round trip each would take hours and hammer the database for no reason.
func (r *candleRepository) UpsertCandles(ctx context.Context, candles []models.Candle) error {
	for start := 0; start < len(candles); start += constants.UpsertBatchSize {
		end := min(start+constants.UpsertBatchSize, len(candles))

		if err := r.upsertChunk(ctx, candles[start:end]); err != nil {
			return err
		}
	}
	return nil
}

// upsertChunk sends one batch and reports the first failure.
func (r *candleRepository) upsertChunk(ctx context.Context, chunk []models.Candle) error {
	params := make([]db.BatchUpsertCandleParams, 0, len(chunk))
	for _, c := range chunk {
		params = append(params, db.BatchUpsertCandleParams{
			Symbol:      c.Symbol,
			MarketType:  c.MarketType.String(),
			Timeframe:   c.Timeframe.String(),
			OpenTime:    database.TimestamptzFromTime(c.OpenTime),
			CloseTime:   database.TimestamptzFromTime(c.CloseTime),
			Open:        database.NumericFromDecimal(c.Open),
			High:        database.NumericFromDecimal(c.High),
			Low:         database.NumericFromDecimal(c.Low),
			Close:       database.NumericFromDecimal(c.Close),
			Volume:      database.NumericFromDecimal(c.Volume),
			QuoteVolume: database.NumericFromDecimal(c.QuoteVolume),
			TradeCount:  c.TradeCount,
			IsClosed:    c.IsClosed,
		})
	}

	results := r.queries.BatchUpsertCandle(ctx, params)

	// Collect the first error rather than the last: later failures are
	// usually consequences of the first.
	var firstErr error
	results.Exec(func(_ int, err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	})
	if closeErr := results.Close(); closeErr != nil && firstErr == nil {
		firstErr = closeErr
	}

	if firstErr != nil {
		return fmt.Errorf("batch upsert %d candles: %w", len(chunk), firstErr)
	}
	return nil
}

// FindGaps returns the missing ranges in the stored sequence.
func (r *candleRepository) FindGaps(ctx context.Context, symbol string, marketType constants.MarketType, timeframe constants.Timeframe) ([]candle.Gap, error) {
	interval, err := database.IntervalFromDuration(timeframe.Duration())
	if err != nil {
		return nil, fmt.Errorf("find gaps: %w", err)
	}

	rows, err := r.queries.FindCandleGaps(ctx, db.FindCandleGapsParams{
		Symbol:     symbol,
		MarketType: marketType.String(),
		Timeframe:  timeframe.String(),
		Interval:   interval,
	})
	if err != nil {
		return nil, fmt.Errorf("find gaps: %w", err)
	}

	gaps := make([]candle.Gap, 0, len(rows))
	for _, row := range rows {
		gaps = append(gaps, candle.Gap{
			Start: database.TimeFromTimestamptz(row.GapStart),
			End:   database.TimeFromTimestamptz(row.GapEnd),
		})
	}
	return gaps, nil
}

// FetchEarliestCandle returns the oldest stored candle for a series.
func (r *candleRepository) FetchEarliestCandle(ctx context.Context, symbol string, marketType constants.MarketType, timeframe constants.Timeframe) (models.Candle, error) {
	row, err := r.queries.GetEarliestCandle(ctx, db.GetEarliestCandleParams{
		Symbol:     symbol,
		MarketType: marketType.String(),
		Timeframe:  timeframe.String(),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return models.Candle{}, constants.ErrNotFound
	}
	if err != nil {
		return models.Candle{}, fmt.Errorf("fetch earliest candle: %w", err)
	}

	c, err := toCandleModel(row)
	if err != nil {
		return models.Candle{}, fmt.Errorf("fetch earliest candle: %w", err)
	}
	return c, nil
}
