package repository_test

import (
	"context"
	"testing"
	"time"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	_datagap_repo "github.com/spioneracorei8/btcusd-trading-platform/server/services/datagap/repository"
	"github.com/spioneracorei8/btcusd-trading-platform/server/testhelper"
)

// TestInsertGap covers the row the backfill worker will write.
func TestInsertGap(t *testing.T) {
	pool := testhelper.NewTestPool(t)
	const symbol = "TESTGAP"
	testhelper.CleanupSymbol(t, pool, symbol)

	repo := _datagap_repo.NewDataGapRepoImpl(pool)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	gap, err := repo.InsertGap(ctx, models.DataGap{
		Symbol:     symbol,
		MarketType: constants.MarketTypeSpot,
		Timeframe:  constants.Timeframe1m,
		GapStart:   start,
		GapEnd:     start.Add(5 * time.Minute),
		Note:       "websocket disconnect",
	})
	if err != nil {
		t.Fatalf("InsertGap() returned error: %v", err)
	}
	if gap.Id == 0 {
		t.Error("InsertGap() returned no id")
	}
	if gap.FilledAt != nil {
		t.Errorf("FilledAt = %v, want nil for a fresh gap", gap.FilledAt)
	}
	if gap.DetectedAt.IsZero() {
		t.Error("DetectedAt was not set")
	}
}

// TestInsertGapIsIdempotent covers the ticker: gap detection re-finds the same
// unfilled hole on every pass, and each pass must update the one row rather
// than add another. Without this, "how many gaps do I have" becomes a count of
// how many times the scan has run.
func TestInsertGapIsIdempotent(t *testing.T) {
	pool := testhelper.NewTestPool(t)
	const symbol = "TESTGAPDUP"
	testhelper.CleanupSymbol(t, pool, symbol)

	repo := _datagap_repo.NewDataGapRepoImpl(pool)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	gap := models.DataGap{
		Symbol:     symbol,
		MarketType: constants.MarketTypeSpot,
		Timeframe:  constants.Timeframe1m,
		GapStart:   start,
		GapEnd:     start.Add(5 * time.Minute),
		Note:       "detected by scan",
	}

	first, err := repo.InsertGap(ctx, gap)
	if err != nil {
		t.Fatalf("first InsertGap() returned error: %v", err)
	}
	second, err := repo.InsertGap(ctx, gap)
	if err != nil {
		t.Fatalf("second InsertGap() returned error: %v", err)
	}

	if first.Id != second.Id {
		t.Errorf("a repeated scan created a second row: id %d then %d", first.Id, second.Id)
	}

	count, err := repo.CountUnfilled(ctx, symbol, constants.MarketTypeSpot, constants.Timeframe1m)
	if err != nil {
		t.Fatalf("CountUnfilled() returned error: %v", err)
	}
	if count != 1 {
		t.Errorf("CountUnfilled() = %d after two scans, want 1", count)
	}
}

// TestGapFillLifecycle walks a gap from detection through failed retries to a
// successful fill.
func TestGapFillLifecycle(t *testing.T) {
	pool := testhelper.NewTestPool(t)
	const symbol = "TESTGAPLIFE"
	testhelper.CleanupSymbol(t, pool, symbol)

	repo := _datagap_repo.NewDataGapRepoImpl(pool)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	stored, err := repo.InsertGap(ctx, models.DataGap{
		Symbol:     symbol,
		MarketType: constants.MarketTypeSpot,
		Timeframe:  constants.Timeframe1m,
		GapStart:   start,
		GapEnd:     start.Add(3 * time.Minute),
	})
	if err != nil {
		t.Fatalf("InsertGap() returned error: %v", err)
	}
	if stored.FillAttempts != 0 {
		t.Errorf("FillAttempts = %d on a fresh gap, want 0", stored.FillAttempts)
	}

	// Exhaust the retry budget; the gap must drop out of the retry list but
	// stay counted as missing.
	for attempt := 1; attempt <= constants.MaxGapFillAttempts; attempt++ {
		updated, err := repo.RecordFillAttempt(ctx, stored.Id, "binance returned no data")
		if err != nil {
			t.Fatalf("RecordFillAttempt() %d returned error: %v", attempt, err)
		}
		if updated.FillAttempts != int32(attempt) {
			t.Errorf("FillAttempts = %d after attempt %d", updated.FillAttempts, attempt)
		}
	}

	retryable, err := repo.ListUnfilled(ctx, symbol, constants.MarketTypeSpot, constants.Timeframe1m, constants.MaxGapFillAttempts)
	if err != nil {
		t.Fatalf("ListUnfilled() returned error: %v", err)
	}
	if len(retryable) != 0 {
		t.Errorf("a gap with a spent budget is still being retried: %+v", retryable)
	}

	count, err := repo.CountUnfilled(ctx, symbol, constants.MarketTypeSpot, constants.Timeframe1m)
	if err != nil {
		t.Fatalf("CountUnfilled() returned error: %v", err)
	}
	if count != 1 {
		t.Errorf("CountUnfilled() = %d, want 1: exhausted retries are still missing data", count)
	}

	// Filling it clears both.
	if err := repo.MarkFilled(ctx, stored.Id); err != nil {
		t.Fatalf("MarkFilled() returned error: %v", err)
	}
	count, err = repo.CountUnfilled(ctx, symbol, constants.MarketTypeSpot, constants.Timeframe1m)
	if err != nil {
		t.Fatalf("CountUnfilled() returned error: %v", err)
	}
	if count != 0 {
		t.Errorf("CountUnfilled() = %d after the gap was filled, want 0", count)
	}
}
