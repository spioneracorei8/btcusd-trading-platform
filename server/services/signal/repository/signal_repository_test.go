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

// TestInsertSignalSeparatesMarketTypes guards the other half of that rule:
// BTCUSDT spot and BTCUSDT futures are different instruments, so the same bar
// on each must produce two signals rather than one collision.
func TestInsertSignalSeparatesMarketTypes(t *testing.T) {
	pool := testhelper.NewTestPool(t)
	const symbol = "TESTMARKETSPLIT"
	testhelper.CleanupSymbol(t, pool, symbol)

	repo := _signal_repo.NewSignalRepoImpl(pool)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	spot := models.Signal{
		Symbol:          symbol,
		MarketType:      constants.MarketTypeSpot,
		Timeframe:       constants.Timeframe5m,
		SignalTime:      time.Date(2026, 8, 1, 0, 5, 0, 0, time.UTC),
		Direction:       constants.DirectionLong,
		Strength:        decimal.RequireFromString("50.00"),
		StrategyName:    "phase01-placeholder",
		StrategyVersion: "v0",
	}
	if _, err := repo.InsertSignal(ctx, spot); err != nil {
		t.Fatalf("spot InsertSignal() returned error: %v", err)
	}

	futures := spot
	futures.MarketType = constants.MarketTypeFutures
	stored, err := repo.InsertSignal(ctx, futures)
	if err != nil {
		if errors.Is(err, constants.ErrDuplicateSignal) {
			t.Fatal("a futures signal was rejected as a duplicate of the spot signal for the same bar")
		}
		t.Fatalf("futures InsertSignal() returned error: %v", err)
	}
	if stored.MarketType != constants.MarketTypeFutures {
		t.Errorf("MarketType = %q, want %q", stored.MarketType, constants.MarketTypeFutures)
	}
}

// TestSignalPriceSurvivesTheRoundTrip.
//
// The whole point of the column is that it holds a different number from
// entry_price. A conversion that dropped it, or one that filled entry_price
// from it, would leave every other test passing and make the phase 07
// reconciliation compare a close against a next-bar open while calling the
// difference slippage.
func TestSignalPriceSurvivesTheRoundTrip(t *testing.T) {
	pool := testhelper.NewTestPool(t)
	const symbol = "TESTSIGNALPRICE"
	testhelper.CleanupSymbol(t, pool, symbol)

	repo := _signal_repo.NewSignalRepoImpl(pool)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// What a live signal looks like the moment it is written: the close is
	// known, the entry is not.
	decided := decimal.RequireFromString("64123.45000000")
	stored, err := repo.InsertSignal(ctx, models.Signal{
		Symbol:          symbol,
		MarketType:      constants.MarketTypeSpot,
		Timeframe:       constants.Timeframe4h,
		SignalTime:      time.Date(2026, 8, 1, 4, 0, 0, 0, time.UTC),
		Direction:       constants.DirectionLong,
		Strength:        decimal.NewFromInt(constants.SignalStrengthNotReported),
		SignalPrice:     decimal.NullDecimal{Decimal: decided, Valid: true},
		StopLoss:        decimal.NullDecimal{Decimal: decimal.RequireFromString("63900.00000000"), Valid: true},
		TakeProfit:      decimal.NullDecimal{Decimal: decimal.RequireFromString("64600.00000000"), Valid: true},
		StrategyName:    "ema_crossover",
		StrategyVersion: "v1",
		Reason:          []byte(`{"note":"integration test"}`),
	})
	if err != nil {
		t.Fatalf("InsertSignal() returned error: %v", err)
	}

	if !stored.SignalPrice.Valid {
		t.Fatal("signal_price came back NULL after being written")
	}
	if !stored.SignalPrice.Decimal.Equal(decided) {
		t.Errorf("SignalPrice = %s, want %s", stored.SignalPrice.Decimal, decided)
	}
	if stored.EntryPrice.Valid {
		t.Errorf("EntryPrice = %s; it is not knowable until the next bar opens",
			stored.EntryPrice.Decimal)
	}
}
