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
