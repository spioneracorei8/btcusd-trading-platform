package repository_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	_signal_repo "github.com/spioneracorei8/btcusd-trading-platform/server/services/signal/repository"
	"github.com/spioneracorei8/btcusd-trading-platform/server/testhelper"
)

// TestInsertSignalRejectsDuplicates proves the unique constraint stops the
// owner being notified twice for the same candle.
func TestInsertSignalRejectsDuplicates(t *testing.T) {
	pool := testhelper.NewTestPool(t)
	const symbol = "TESTSIGNAL"
	testhelper.CleanupSymbol(t, pool, symbol)

	repo := _signal_repo.NewSignalRepoImpl(pool)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	s := models.Signal{
		Symbol:          symbol,
		MarketType:      constants.MarketTypeSpot,
		Timeframe:       constants.Timeframe5m,
		SignalTime:      time.Date(2026, 8, 1, 0, 5, 0, 0, time.UTC),
		Direction:       constants.DirectionLong,
		Strength:        decimal.RequireFromString("72.50"),
		EntryPrice:      decimal.NullDecimal{Decimal: decimal.RequireFromString("64080.25000000"), Valid: true},
		StopLoss:        decimal.NullDecimal{Decimal: decimal.RequireFromString("63950.00000000"), Valid: true},
		StrategyName:    "phase01-placeholder",
		StrategyVersion: "v0",
		Reason:          []byte(`{"note":"integration test"}`),
	}

	stored, err := repo.InsertSignal(ctx, s)
	if err != nil {
		t.Fatalf("InsertSignal() returned error: %v", err)
	}
	if stored.Id.String() == "" || stored.CreatedAt.IsZero() {
		t.Errorf("InsertSignal() returned an incomplete row: %+v", stored)
	}
	if !stored.Strength.Equal(s.Strength) {
		t.Errorf("Strength = %s, want %s", stored.Strength, s.Strength)
	}
	if !stored.EntryPrice.Valid || !stored.EntryPrice.Decimal.Equal(s.EntryPrice.Decimal) {
		t.Errorf("EntryPrice = %+v, want %s", stored.EntryPrice, s.EntryPrice.Decimal)
	}
	if stored.TakeProfit.Valid {
		t.Errorf("TakeProfit = %+v, want NULL", stored.TakeProfit)
	}

	if _, err := repo.InsertSignal(ctx, s); !errors.Is(err, constants.ErrDuplicateSignal) {
		t.Fatalf("second InsertSignal() returned %v, want ErrDuplicateSignal", err)
	}
}
