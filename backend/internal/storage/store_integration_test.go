package storage_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/spioneracorei8/btcusd-trading-platform/backend/internal/domain"
	"github.com/spioneracorei8/btcusd-trading-platform/backend/internal/storage"
)

// These tests need a real PostgreSQL/TimescaleDB with the migrations already
// applied. Point TEST_DATABASE_URL at one — `make test-integration` starts the
// compose database, migrates it and sets the variable for you.
//
// Without TEST_DATABASE_URL the tests skip, so `go test ./...` stays green on
// a machine that has no Docker.
const testDatabaseURLEnv = "TEST_DATABASE_URL"

// newTestStore connects to the test database or skips the calling test.
func newTestStore(t *testing.T) *storage.Store {
	t.Helper()

	dsn := os.Getenv(testDatabaseURLEnv)
	if dsn == "" {
		t.Skipf("%s is not set; skipping integration test", testDatabaseURLEnv)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := storage.NewPool(ctx, storage.PoolOptions{DSN: dsn, MaxConns: 4})
	if err != nil {
		t.Fatalf("connect to %s: %v", testDatabaseURLEnv, err)
	}
	t.Cleanup(pool.Close)

	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("ping test database: %v", err)
	}

	store := storage.New(pool)
	if _, err := pool.Exec(ctx, "SELECT 1 FROM candles LIMIT 0"); err != nil {
		t.Fatalf("test database has no candles table, run `make migrate-up` first: %v", err)
	}
	return store
}

// testCandle builds a closed candle on a symbol reserved for tests, so a run
// can never disturb real market data.
func testCandle(symbol string, openTime time.Time) domain.Candle {
	return domain.Candle{
		Symbol:      symbol,
		MarketType:  domain.MarketTypeSpot,
		Timeframe:   domain.Timeframe1m,
		OpenTime:    openTime,
		CloseTime:   openTime.Add(domain.Timeframe1m.Duration()),
		Open:        decimal.RequireFromString("64000.10000000"),
		High:        decimal.RequireFromString("64100.55000000"),
		Low:         decimal.RequireFromString("63950.00000000"),
		Close:       decimal.RequireFromString("64080.25000000"),
		Volume:      decimal.RequireFromString("12.34567890"),
		QuoteVolume: decimal.RequireFromString("790123.45678900"),
		TradeCount:  431,
		IsClosed:    true,
	}
}

// cleanupSymbol removes every row this test wrote.
func cleanupSymbol(t *testing.T, store *storage.Store, symbol string) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if _, err := store.Pool().Exec(ctx, "DELETE FROM candles WHERE symbol = $1", symbol); err != nil {
			t.Errorf("cleanup candles for %s: %v", symbol, err)
		}
		if _, err := store.Pool().Exec(ctx, "DELETE FROM signals WHERE symbol = $1", symbol); err != nil {
			t.Errorf("cleanup signals for %s: %v", symbol, err)
		}
		if _, err := store.Pool().Exec(ctx, "DELETE FROM data_gaps WHERE symbol = $1", symbol); err != nil {
			t.Errorf("cleanup data gaps for %s: %v", symbol, err)
		}
	})
}

// TestUpsertCandleIsIdempotent is the phase 01 acceptance check: writing the
// same bar twice must leave exactly one row. A reconnect plus a REST backfill
// routinely deliver the same candle more than once.
func TestUpsertCandleIsIdempotent(t *testing.T) {
	store := newTestStore(t)
	const symbol = "TESTIDEMPOTENT"
	cleanupSymbol(t, store, symbol)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	openTime := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	candle := testCandle(symbol, openTime)

	if err := store.UpsertCandle(ctx, candle); err != nil {
		t.Fatalf("first UpsertCandle() returned error: %v", err)
	}
	if err := store.UpsertCandle(ctx, candle); err != nil {
		t.Fatalf("second UpsertCandle() returned error: %v", err)
	}

	count, err := store.CountCandles(ctx, symbol, domain.MarketTypeSpot, domain.Timeframe1m)
	if err != nil {
		t.Fatalf("CountCandles() returned error: %v", err)
	}
	if count != 1 {
		t.Fatalf("after two identical upserts there are %d rows, want 1", count)
	}
}

// TestUpsertCandleUpdatesInPlace covers the correction case: Binance restating
// a bar must overwrite it, not add a second one.
func TestUpsertCandleUpdatesInPlace(t *testing.T) {
	store := newTestStore(t)
	const symbol = "TESTUPDATE"
	cleanupSymbol(t, store, symbol)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	openTime := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	candle := testCandle(symbol, openTime)
	if err := store.UpsertCandle(ctx, candle); err != nil {
		t.Fatalf("first UpsertCandle() returned error: %v", err)
	}

	corrected := candle
	corrected.Close = decimal.RequireFromString("64111.99000000")
	corrected.Volume = decimal.RequireFromString("15.00000000")
	corrected.TradeCount = 512
	if err := store.UpsertCandle(ctx, corrected); err != nil {
		t.Fatalf("corrected UpsertCandle() returned error: %v", err)
	}

	count, err := store.CountCandles(ctx, symbol, domain.MarketTypeSpot, domain.Timeframe1m)
	if err != nil {
		t.Fatalf("CountCandles() returned error: %v", err)
	}
	if count != 1 {
		t.Fatalf("after a correction there are %d rows, want 1", count)
	}

	got, err := store.GetLatestCandle(ctx, symbol, domain.MarketTypeSpot, domain.Timeframe1m)
	if err != nil {
		t.Fatalf("GetLatestCandle() returned error: %v", err)
	}
	if !got.Close.Equal(corrected.Close) {
		t.Errorf("Close = %s, want %s", got.Close, corrected.Close)
	}
	if !got.Volume.Equal(corrected.Volume) {
		t.Errorf("Volume = %s, want %s", got.Volume, corrected.Volume)
	}
	if got.TradeCount != corrected.TradeCount {
		t.Errorf("TradeCount = %d, want %d", got.TradeCount, corrected.TradeCount)
	}
	if !got.OpenTime.Equal(openTime) {
		t.Errorf("OpenTime = %s, want %s", got.OpenTime, openTime)
	}
	if got.OpenTime.Location() != time.UTC {
		t.Errorf("OpenTime location = %v, want UTC", got.OpenTime.Location())
	}
}

// TestUpsertCandleRejectsUnclosedCandle guards the rule that a bar which is
// still forming must never reach the table the strategies read from.
func TestUpsertCandleRejectsUnclosedCandle(t *testing.T) {
	store := newTestStore(t)
	const symbol = "TESTUNCLOSED"
	cleanupSymbol(t, store, symbol)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	candle := testCandle(symbol, time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC))
	candle.IsClosed = false

	if err := store.UpsertCandle(ctx, candle); err == nil {
		t.Fatal("UpsertCandle() accepted an unclosed candle")
	}
}

// TestGetCandlesReturnsWindowInOrder checks the range query the backtest
// engine will feed from.
func TestGetCandlesReturnsWindowInOrder(t *testing.T) {
	store := newTestStore(t)
	const symbol = "TESTWINDOW"
	cleanupSymbol(t, store, symbol)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	for i := range 5 {
		candle := testCandle(symbol, start.Add(time.Duration(i)*time.Minute))
		if err := store.UpsertCandle(ctx, candle); err != nil {
			t.Fatalf("UpsertCandle() %d returned error: %v", i, err)
		}
	}

	got, err := store.GetCandles(ctx, storage.GetCandlesParams{
		Symbol:     symbol,
		MarketType: domain.MarketTypeSpot,
		Timeframe:  domain.Timeframe1m,
		From:       start.Add(time.Minute),
		To:         start.Add(3 * time.Minute),
	})
	if err != nil {
		t.Fatalf("GetCandles() returned error: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("GetCandles() returned %d candles, want 3", len(got))
	}
	for i := 1; i < len(got); i++ {
		if !got[i].OpenTime.After(got[i-1].OpenTime) {
			t.Errorf("candles are not ordered by open_time: %s then %s", got[i-1].OpenTime, got[i].OpenTime)
		}
	}
}

// TestGetLatestCandleWithoutData asserts the sentinel an empty series returns.
func TestGetLatestCandleWithoutData(t *testing.T) {
	store := newTestStore(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err := store.GetLatestCandle(ctx, "TESTNOSUCHSYMBOL", domain.MarketTypeSpot, domain.Timeframe1m)
	if !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("GetLatestCandle() on an empty series returned %v, want ErrNotFound", err)
	}
}

// TestInsertSignalRejectsDuplicates proves the unique constraint stops the
// owner being notified twice for the same candle.
func TestInsertSignalRejectsDuplicates(t *testing.T) {
	store := newTestStore(t)
	const symbol = "TESTSIGNAL"
	cleanupSymbol(t, store, symbol)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	signal := domain.Signal{
		Symbol:          symbol,
		MarketType:      domain.MarketTypeSpot,
		Timeframe:       domain.Timeframe5m,
		SignalTime:      time.Date(2026, 8, 1, 0, 5, 0, 0, time.UTC),
		Direction:       domain.DirectionLong,
		Strength:        decimal.RequireFromString("72.50"),
		EntryPrice:      decimal.NullDecimal{Decimal: decimal.RequireFromString("64080.25000000"), Valid: true},
		StopLoss:        decimal.NullDecimal{Decimal: decimal.RequireFromString("63950.00000000"), Valid: true},
		StrategyName:    "phase01-placeholder",
		StrategyVersion: "v0",
		Reason:          []byte(`{"note":"integration test"}`),
	}

	stored, err := store.InsertSignal(ctx, signal)
	if err != nil {
		t.Fatalf("InsertSignal() returned error: %v", err)
	}
	if stored.ID.String() == "" || stored.CreatedAt.IsZero() {
		t.Errorf("InsertSignal() returned an incomplete row: %+v", stored)
	}
	if !stored.Strength.Equal(signal.Strength) {
		t.Errorf("Strength = %s, want %s", stored.Strength, signal.Strength)
	}
	if !stored.EntryPrice.Valid || !stored.EntryPrice.Decimal.Equal(signal.EntryPrice.Decimal) {
		t.Errorf("EntryPrice = %+v, want %s", stored.EntryPrice, signal.EntryPrice.Decimal)
	}
	if stored.TakeProfit.Valid {
		t.Errorf("TakeProfit = %+v, want NULL", stored.TakeProfit)
	}

	if _, err := store.InsertSignal(ctx, signal); !errors.Is(err, storage.ErrDuplicateSignal) {
		t.Fatalf("second InsertSignal() returned %v, want ErrDuplicateSignal", err)
	}
}

// TestInsertGap covers the row the backfill worker will write.
func TestInsertGap(t *testing.T) {
	store := newTestStore(t)
	const symbol = "TESTGAP"
	cleanupSymbol(t, store, symbol)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	gap, err := store.InsertGap(ctx, domain.DataGap{
		Symbol:     symbol,
		MarketType: domain.MarketTypeSpot,
		Timeframe:  domain.Timeframe1m,
		GapStart:   start,
		GapEnd:     start.Add(5 * time.Minute),
		Note:       "websocket disconnect",
	})
	if err != nil {
		t.Fatalf("InsertGap() returned error: %v", err)
	}
	if gap.ID == 0 {
		t.Error("InsertGap() returned no id")
	}
	if gap.FilledAt != nil {
		t.Errorf("FilledAt = %v, want nil for a fresh gap", gap.FilledAt)
	}
	if gap.DetectedAt.IsZero() {
		t.Error("DetectedAt was not set")
	}
}

// TestCandlesIsHypertable verifies the TimescaleDB conversion actually
// happened, rather than trusting that the migration ran.
func TestCandlesIsHypertable(t *testing.T) {
	store := newTestStore(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var count int
	err := store.Pool().QueryRow(ctx,
		`SELECT count(*) FROM timescaledb_information.hypertables
		 WHERE hypertable_name = 'candles'`).Scan(&count)
	if err != nil {
		t.Fatalf("query timescaledb_information.hypertables: %v", err)
	}
	if count != 1 {
		t.Errorf("candles is not a hypertable (found %d entries)", count)
	}
}
