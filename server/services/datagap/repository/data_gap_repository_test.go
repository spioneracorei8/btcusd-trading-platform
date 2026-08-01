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
