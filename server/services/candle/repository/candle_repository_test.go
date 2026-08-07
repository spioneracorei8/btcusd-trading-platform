package repository_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/candle"
	_candle_repo "github.com/spioneracorei8/btcusd-trading-platform/server/services/candle/repository"
	"github.com/spioneracorei8/btcusd-trading-platform/server/testhelper"
)

// testCandle builds a closed candle on a symbol reserved for tests, so a run
// can never disturb real market data.
func testCandle(symbol string, openTime time.Time) models.Candle {
	return models.Candle{
		Symbol:      symbol,
		MarketType:  constants.MarketTypeSpot,
		Timeframe:   constants.Timeframe1m,
		OpenTime:    openTime,
		CloseTime:   openTime.Add(constants.Timeframe1m.Duration()),
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

// TestUpsertCandleIsIdempotent is the phase 01 acceptance check: writing the
// same bar twice must leave exactly one row. A reconnect plus a REST backfill
// routinely deliver the same candle more than once.
func TestUpsertCandleIsIdempotent(t *testing.T) {
	pool := testhelper.NewTestPool(t)
	const symbol = "TESTIDEMPOTENT"
	testhelper.CleanupSymbol(t, pool, symbol)

	repo := _candle_repo.NewCandleRepoImpl(pool)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	openTime := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	c := testCandle(symbol, openTime)

	if err := repo.UpsertCandle(ctx, c); err != nil {
		t.Fatalf("first UpsertCandle() returned error: %v", err)
	}
	if err := repo.UpsertCandle(ctx, c); err != nil {
		t.Fatalf("second UpsertCandle() returned error: %v", err)
	}

	count, err := repo.CountCandles(ctx, symbol, constants.MarketTypeSpot, constants.Timeframe1m)
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
	pool := testhelper.NewTestPool(t)
	const symbol = "TESTUPDATE"
	testhelper.CleanupSymbol(t, pool, symbol)

	repo := _candle_repo.NewCandleRepoImpl(pool)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	openTime := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	c := testCandle(symbol, openTime)
	if err := repo.UpsertCandle(ctx, c); err != nil {
		t.Fatalf("first UpsertCandle() returned error: %v", err)
	}

	corrected := c
	corrected.Close = decimal.RequireFromString("64111.99000000")
	corrected.Volume = decimal.RequireFromString("15.00000000")
	corrected.TradeCount = 512
	if err := repo.UpsertCandle(ctx, corrected); err != nil {
		t.Fatalf("corrected UpsertCandle() returned error: %v", err)
	}

	count, err := repo.CountCandles(ctx, symbol, constants.MarketTypeSpot, constants.Timeframe1m)
	if err != nil {
		t.Fatalf("CountCandles() returned error: %v", err)
	}
	if count != 1 {
		t.Fatalf("after a correction there are %d rows, want 1", count)
	}

	got, err := repo.FetchLatestCandle(ctx, symbol, constants.MarketTypeSpot, constants.Timeframe1m)
	if err != nil {
		t.Fatalf("FetchLatestCandle() returned error: %v", err)
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

// TestFetchCandlesReturnsWindowInOrder checks the range query the backtest
// engine will feed from.
func TestFetchCandlesReturnsWindowInOrder(t *testing.T) {
	pool := testhelper.NewTestPool(t)
	const symbol = "TESTWINDOW"
	testhelper.CleanupSymbol(t, pool, symbol)

	repo := _candle_repo.NewCandleRepoImpl(pool)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	for i := range 5 {
		if err := repo.UpsertCandle(ctx, testCandle(symbol, start.Add(time.Duration(i)*time.Minute))); err != nil {
			t.Fatalf("UpsertCandle() %d returned error: %v", i, err)
		}
	}

	got, err := repo.FetchCandles(ctx, candle.FetchCandlesParams{
		Symbol:     symbol,
		MarketType: constants.MarketTypeSpot,
		Timeframe:  constants.Timeframe1m,
		From:       start.Add(time.Minute),
		To:         start.Add(3 * time.Minute),
	})
	if err != nil {
		t.Fatalf("FetchCandles() returned error: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("FetchCandles() returned %d candles, want 3", len(got))
	}
	for i := 1; i < len(got); i++ {
		if !got[i].OpenTime.After(got[i-1].OpenTime) {
			t.Errorf("candles are not ordered by open_time: %s then %s", got[i-1].OpenTime, got[i].OpenTime)
		}
	}
}

// TestFetchLatestCandleWithoutData asserts the sentinel an empty series returns.
func TestFetchLatestCandleWithoutData(t *testing.T) {
	pool := testhelper.NewTestPool(t)

	repo := _candle_repo.NewCandleRepoImpl(pool)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err := repo.FetchLatestCandle(ctx, "TESTNOSUCHSYMBOL", constants.MarketTypeSpot, constants.Timeframe1m)
	if !errors.Is(err, constants.ErrNotFound) {
		t.Fatalf("FetchLatestCandle() on an empty series returned %v, want ErrNotFound", err)
	}
}

// TestCandlesIsHypertable verifies the TimescaleDB conversion actually
// happened, rather than trusting that the migration ran.
func TestCandlesIsHypertable(t *testing.T) {
	pool := testhelper.NewTestPool(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var count int
	err := pool.QueryRow(ctx,
		`SELECT count(*) FROM timescaledb_information.hypertables
		 WHERE hypertable_name = 'candles'`).Scan(&count)
	if err != nil {
		t.Fatalf("query timescaledb_information.hypertables: %v", err)
	}
	if count != 1 {
		t.Errorf("candles is not a hypertable (found %d entries)", count)
	}
}

// TestUpsertCandlesIsIdempotent is the batch counterpart of the phase 01
// acceptance check: re-running a backfill over stored data must change
// nothing, because a restart mid-backfill re-fetches ranges it already has.
func TestUpsertCandlesIsIdempotent(t *testing.T) {
	pool := testhelper.NewTestPool(t)
	const symbol = "TESTBATCH"
	testhelper.CleanupSymbol(t, pool, symbol)

	repo := _candle_repo.NewCandleRepoImpl(pool)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// More than one chunk, so the chunking boundary is exercised too.
	const count = constants.UpsertBatchSize + 250
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)

	batch := make([]models.Candle, 0, count)
	for i := range count {
		batch = append(batch, testCandle(symbol, start.Add(time.Duration(i)*time.Minute)))
	}

	if err := repo.UpsertCandles(ctx, batch); err != nil {
		t.Fatalf("first UpsertCandles() returned error: %v", err)
	}
	if err := repo.UpsertCandles(ctx, batch); err != nil {
		t.Fatalf("second UpsertCandles() returned error: %v", err)
	}

	stored, err := repo.CountCandles(ctx, symbol, constants.MarketTypeSpot, constants.Timeframe1m)
	if err != nil {
		t.Fatalf("CountCandles() returned error: %v", err)
	}
	if stored != int64(count) {
		t.Fatalf("after two identical batches there are %d rows, want %d", stored, count)
	}

	// Values must match too, not merely the row count.
	got, err := repo.FetchLatestCandle(ctx, symbol, constants.MarketTypeSpot, constants.Timeframe1m)
	if err != nil {
		t.Fatalf("FetchLatestCandle() returned error: %v", err)
	}
	want := batch[len(batch)-1]
	if !got.Close.Equal(want.Close) || !got.OpenTime.Equal(want.OpenTime) {
		t.Errorf("last candle = %s at %s, want %s at %s",
			got.Close, got.OpenTime, want.Close, want.OpenTime)
	}
}

// TestFindGapsDetectsHolesAtEveryPosition covers the three placements that
// break naive implementations: the start, the middle and the end of a series.
func TestFindGapsDetectsHolesAtEveryPosition(t *testing.T) {
	pool := testhelper.NewTestPool(t)
	const symbol = "TESTGAPSCAN"
	testhelper.CleanupSymbol(t, pool, symbol)

	repo := _candle_repo.NewCandleRepoImpl(pool)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)

	// Minutes 0..29 with three holes deliberately punched out.
	missing := map[int]bool{
		3:  true,                     // near the start
		15: true, 16: true, 17: true, // a run in the middle
		28: true, // near the end
	}

	batch := make([]models.Candle, 0, 30)
	for i := range 30 {
		if missing[i] {
			continue
		}
		batch = append(batch, testCandle(symbol, start.Add(time.Duration(i)*time.Minute)))
	}
	if err := repo.UpsertCandles(ctx, batch); err != nil {
		t.Fatalf("UpsertCandles() returned error: %v", err)
	}

	gaps, err := repo.FindGaps(ctx, symbol, constants.MarketTypeSpot, constants.Timeframe1m)
	if err != nil {
		t.Fatalf("FindGaps() returned error: %v", err)
	}
	if len(gaps) != 3 {
		t.Fatalf("FindGaps() found %d gaps, want 3: %+v", len(gaps), gaps)
	}

	want := []candle.Gap{
		{Start: start.Add(3 * time.Minute), End: start.Add(4 * time.Minute)},
		{Start: start.Add(15 * time.Minute), End: start.Add(18 * time.Minute)},
		{Start: start.Add(28 * time.Minute), End: start.Add(29 * time.Minute)},
	}
	for i, w := range want {
		if !gaps[i].Start.Equal(w.Start) || !gaps[i].End.Equal(w.End) {
			t.Errorf("gap %d = [%s, %s), want [%s, %s)",
				i, gaps[i].Start, gaps[i].End, w.Start, w.End)
		}
	}
}

// TestFindGapsOnCompleteSeriesFindsNothing is the other half: a series with no
// holes must report none, or every scan would chase phantom ranges for ever.
func TestFindGapsOnCompleteSeriesFindsNothing(t *testing.T) {
	pool := testhelper.NewTestPool(t)
	const symbol = "TESTNOGAPS"
	testhelper.CleanupSymbol(t, pool, symbol)

	repo := _candle_repo.NewCandleRepoImpl(pool)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	batch := make([]models.Candle, 0, 60)
	for i := range 60 {
		batch = append(batch, testCandle(symbol, start.Add(time.Duration(i)*time.Minute)))
	}
	if err := repo.UpsertCandles(ctx, batch); err != nil {
		t.Fatalf("UpsertCandles() returned error: %v", err)
	}

	gaps, err := repo.FindGaps(ctx, symbol, constants.MarketTypeSpot, constants.Timeframe1m)
	if err != nil {
		t.Fatalf("FindGaps() returned error: %v", err)
	}
	if len(gaps) != 0 {
		t.Errorf("FindGaps() found %d gaps in a complete series: %+v", len(gaps), gaps)
	}
}

func TestFetchEarliestCandle(t *testing.T) {
	pool := testhelper.NewTestPool(t)
	const symbol = "TESTEARLIEST"
	testhelper.CleanupSymbol(t, pool, symbol)

	repo := _candle_repo.NewCandleRepoImpl(pool)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if _, err := repo.FetchEarliestCandle(ctx, symbol, constants.MarketTypeSpot, constants.Timeframe1m); !errors.Is(err, constants.ErrNotFound) {
		t.Fatalf("FetchEarliestCandle() on an empty series returned %v, want ErrNotFound", err)
	}

	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	batch := []models.Candle{
		testCandle(symbol, start.Add(2*time.Minute)),
		testCandle(symbol, start),
		testCandle(symbol, start.Add(time.Minute)),
	}
	if err := repo.UpsertCandles(ctx, batch); err != nil {
		t.Fatalf("UpsertCandles() returned error: %v", err)
	}

	got, err := repo.FetchEarliestCandle(ctx, symbol, constants.MarketTypeSpot, constants.Timeframe1m)
	if err != nil {
		t.Fatalf("FetchEarliestCandle() returned error: %v", err)
	}
	if !got.OpenTime.Equal(start) {
		t.Errorf("earliest OpenTime = %s, want %s", got.OpenTime, start)
	}
}
