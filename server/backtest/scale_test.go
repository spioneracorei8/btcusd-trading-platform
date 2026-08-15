package main

import (
	"testing"

	"github.com/shopspring/decimal"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/backtest"
)

// TestTheCostSweepScalesTheSpread is the stress test that matters on a
// floating-spread venue.
//
// A 25 USD spread is a typical quote, not a guaranteed one, and it widens
// exactly when a strategy most wants to trade. A sweep that scaled the
// percentage fees but left the spread at its typical figure would report a
// spread-model run as immune to the one cost that actually moves against it.
func TestTheCostSweepScalesTheSpread(t *testing.T) {
	base := backtest.Costs{
		Model:            constants.CostModelSpread,
		SpreadPoints:     2500,
		PointValue:       decimal.RequireFromString("0.01"),
		CommissionPerLot: decimal.NewFromInt(4),
		SlippageTicks:    1,
		TickSize:         decimal.RequireFromString("0.01"),
	}

	for _, tc := range []struct {
		multiplier       float64
		wantPoints       int
		wantSpreadPrice  string
		wantCommission   string
		wantSlippageTick int
	}{
		{1, 2500, "25", "4", 1},
		{1.5, 3750, "37.5", "6", 2},
		{2, 5000, "50", "8", 2},
	} {
		scaled := scaleCosts(base, tc.multiplier)

		if scaled.SpreadPoints != tc.wantPoints {
			t.Errorf("at %.1fx the spread is %d points, want %d",
				tc.multiplier, scaled.SpreadPoints, tc.wantPoints)
		}
		if got := scaled.SpreadPrice(); !got.Equal(decimal.RequireFromString(tc.wantSpreadPrice)) {
			t.Errorf("at %.1fx the spread is %s of price, want %s",
				tc.multiplier, got, tc.wantSpreadPrice)
		}
		if !scaled.CommissionPerLot.Equal(decimal.RequireFromString(tc.wantCommission)) {
			t.Errorf("at %.1fx commission is %s per lot, want %s",
				tc.multiplier, scaled.CommissionPerLot, tc.wantCommission)
		}
		if scaled.SlippageTicks != tc.wantSlippageTick {
			t.Errorf("at %.1fx slippage is %d ticks, want %d",
				tc.multiplier, scaled.SlippageTicks, tc.wantSlippageTick)
		}

		// Everything the sweep does not stress must survive it. The model
		// itself especially: a sweep that dropped back to percentage would
		// answer a different question at every multiplier after the first.
		if scaled.CostModel() != constants.CostModelSpread {
			t.Errorf("at %.1fx the cost model became %q", tc.multiplier, scaled.CostModel())
		}
		if !scaled.PointValue.Equal(base.PointValue) {
			t.Errorf("at %.1fx the point value became %s; it describes the venue, not the stress",
				tc.multiplier, scaled.PointValue)
		}
	}
}

// TestTheCostSweepStillScalesPercentageFees checks the older model did not
// lose anything when the spread model was added beside it.
func TestTheCostSweepStillScalesPercentageFees(t *testing.T) {
	base := backtest.Costs{
		FeeTakerPct:   decimal.RequireFromString("0.05"),
		FeeMakerPct:   decimal.RequireFromString("0.02"),
		SlippageTicks: 1,
		TickSize:      decimal.RequireFromString("0.01"),
	}

	scaled := scaleCosts(base, 2)

	if !scaled.FeeTakerPct.Equal(decimal.RequireFromString("0.1")) {
		t.Errorf("taker fee is %s at 2x, want 0.1", scaled.FeeTakerPct)
	}
	// Both rates move together. Scaling only the taker rate would make a
	// maker-configured run look progressively cheaper relative to its own
	// assumption, which is the opposite of what a stress test is for.
	if !scaled.FeeMakerPct.Equal(decimal.RequireFromString("0.04")) {
		t.Errorf("maker fee is %s at 2x, want 0.04", scaled.FeeMakerPct)
	}
}

// TestScalingAMakerlessCostsKeepsTheFallback covers the configuration where no
// maker rate was ever set, so MakerFeePct falls back to the taker rate.
//
// Scaling must not turn that fallback into a stored zero: a run that started
// out charging the taker rate on resting orders would silently begin charging
// nothing for them at 1.5x, and the sweep would report costs falling as they
// were supposed to be rising.
func TestScalingAMakerlessCostsKeepsTheFallback(t *testing.T) {
	base := backtest.Costs{
		FeeTakerPct:   decimal.RequireFromString("0.05"),
		SlippageTicks: 1,
		TickSize:      decimal.RequireFromString("0.01"),
	}
	if !base.MakerFeePct().Equal(base.FeeTakerPct) {
		t.Fatalf("precondition: an unset maker rate should fall back to the taker rate, got %s", base.MakerFeePct())
	}

	scaled := scaleCosts(base, 2)
	if !scaled.MakerFeePct().Equal(decimal.RequireFromString("0.1")) {
		t.Errorf("maker rate is %s at 2x, want 0.1 — the taker fallback was scaled away", scaled.MakerFeePct())
	}
}
