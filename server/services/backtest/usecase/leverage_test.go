package usecase_test

import (
	"testing"

	"github.com/shopspring/decimal"

	"github.com/spioneracorei8/btcusd-trading-platform/server/services/backtest"
)

// sizeAtRisk runs one sized entry and reports what the engine committed.
//
// The series is flat, so the entry price is knowable and every figure below can
// be checked with a calculator.
func sizeAtRisk(t *testing.T, riskPct int64, leverage string, costs backtest.Costs) (backtest.Result, decimal.Decimal) {
	t.Helper()

	series := flatSeries(40, "27000")

	params := scoredParams(t, series, &enterOnceWithStop{
		stop:   decimal.RequireFromString("26950"), // a 50-wide stop
		target: decimal.RequireFromString("28000"),
	})
	params.Costs = costs
	params.InitialEquity = decimal.NewFromInt(100)
	params.Sizing = backtest.Sizing{
		Mode:        backtest.SizingFixedFractional,
		RiskPct:     decimal.NewFromInt(riskPct),
		MaxLeverage: decimal.RequireFromString(leverage),
	}

	result := runEngine(t, &fakeCandles{series: series}, nil, params)
	if len(result.Trades) == 0 {
		return result, decimal.Zero
	}
	return result, result.Trades[0].Size
}

// TestPositionSizeScalesWithRiskPct is the property the sizing rule exists for,
// and the one that was silently not holding.
//
// Position size was capped at equity/price — the most the balance could pay for
// outright. On a 100 USD account at 27,000 that is 0.0037 BTC, far below what
// 1% risk against a 50-wide stop asks for, so the cap bound at every setting
// and 1%, 5% and 20% risk produced byte-identical runs. The risk figure was
// reported and never used.
//
// With the account's leverage stated, the arithmetic is the venue's: 1% of 100
// is 1 USD, over a 50 USD stop distance, is 0.02 BTC.
func TestPositionSizeScalesWithRiskPct(t *testing.T) {
	costs := spreadCosts()

	for _, tc := range []struct {
		riskPct int64
		want    string
	}{
		// risked / stop distance, floored onto the 0.01 lot grid.
		{1, "0.02"}, // 1 / 50  = 0.02
		{5, "0.1"},  // 5 / 50  = 0.10
		{10, "0.2"}, // 10 / 50 = 0.20
		{20, "0.4"}, // 20 / 50 = 0.40
	} {
		// Leverage high enough that the notional cap never binds, so this
		// measures the risk rule rather than the account's buying power.
		result, size := sizeAtRisk(t, tc.riskPct, "500", costs)

		if size.IsZero() {
			t.Errorf("risk %d%%: no trade taken (%d refused below the lot minimum)",
				tc.riskPct, result.EntriesBelowMinLot)
			continue
		}
		if !size.Equal(decimal.RequireFromString(tc.want)) {
			t.Errorf("risk %d%%: size is %s BTC, want %s", tc.riskPct, size, tc.want)
		}

		// And the property the arithmetic exists for: a stop-out loses the
		// configured share of the balance.
		entry := decimal.NewFromInt(27000)
		lossAtStop := entry.Sub(decimal.RequireFromString("26950")).Mul(size)
		wantLoss := decimal.NewFromInt(tc.riskPct)
		if !lossAtStop.Equal(wantLoss) {
			t.Errorf("risk %d%%: a stop-out loses %s, want %s (%d%% of 100)",
				tc.riskPct, lossAtStop, wantLoss, tc.riskPct)
		}
	}
}

// TestDifferentRiskSettingsAreDifferentRuns is the symptom as it was reported:
// two settings, one answer.
//
// Asserted as inequality rather than against expected values, because the
// failure was not a wrong number — it was the same number twice, which no
// single-value assertion would have caught.
func TestDifferentRiskSettingsAreDifferentRuns(t *testing.T) {
	costs := spreadCosts()

	_, atOne := sizeAtRisk(t, 1, "500", costs)
	_, atFive := sizeAtRisk(t, 5, "500", costs)
	_, atTwenty := sizeAtRisk(t, 20, "500", costs)

	if atOne.Equal(atFive) || atFive.Equal(atTwenty) {
		t.Fatalf("sizes at 1%%, 5%% and 20%% risk are %s, %s and %s; "+
			"the risk setting is not reaching the position size", atOne, atFive, atTwenty)
	}
	if !atFive.Equal(atOne.Mul(decimal.NewFromInt(5))) {
		t.Errorf("5%% risk sized %s against %s at 1%%; it should be five times", atFive, atOne)
	}
}

// TestTheNotionalCapIsWhatRefusedTheTrade reproduces the reported failure at
// the default leverage, and proves the report now names its cause.
//
// A cash account cannot hold 0.02 BTC on a 100 USD balance, and refusing is
// correct. What was wrong is that the refusal was indistinguishable from a
// strategy asking for a tiny position, so the setting that would fix it was
// impossible to identify from the report.
func TestTheNotionalCapIsWhatRefusedTheTrade(t *testing.T) {
	costs := spreadCosts()

	result, size := sizeAtRisk(t, 1, "1", costs)
	if !size.IsZero() {
		t.Fatalf("a 100 USD cash account held %s BTC at 27,000; that is %s of notional",
			size, size.Mul(decimal.NewFromInt(27000)))
	}
	if result.EntriesBelowMinLot != 1 {
		t.Fatalf("EntriesBelowMinLot = %d, want 1", result.EntriesBelowMinLot)
	}
	if result.EntriesRefusedAfterCap != 1 {
		t.Errorf("EntriesRefusedAfterCap = %d, want 1; the report cannot tell "+
			"'the strategy wanted a tiny position' from 'the account could not hold it'",
			result.EntriesRefusedAfterCap)
	}

	// And with the venue's real leverage the same signal is tradeable. 0.02 BTC
	// at 27,000 is 540 of notional on a 100 balance — 5.4x, which is ordinary
	// on a CFD account and impossible on a cash one.
	result, size = sizeAtRisk(t, 1, "10", costs)
	if !size.Equal(decimal.RequireFromString("0.02")) {
		t.Errorf("at 10x the size is %s, want 0.02 (%d refused)", size, result.EntriesBelowMinLot)
	}
	if result.EntriesRefusedAfterCap != 0 {
		t.Errorf("EntriesRefusedAfterCap = %d at 10x, want 0", result.EntriesRefusedAfterCap)
	}
}

// TestLeverageDefaultsToACashAccount.
//
// An unstated leverage must never make positions larger than the run before it.
// Every evaluation in docs/experiments.md was a cash account, and a zero value
// that meant "the venue's maximum" would silently reprice all of them.
func TestLeverageDefaultsToACashAccount(t *testing.T) {
	var unset backtest.Sizing
	if !unset.Leverage().Equal(decimal.NewFromInt(1)) {
		t.Errorf("an unset leverage is %s, want 1", unset.Leverage())
	}
	if !backtest.DefaultSizing().Leverage().Equal(decimal.NewFromInt(1)) {
		t.Errorf("DefaultSizing leverage is %s, want 1", backtest.DefaultSizing().Leverage())
	}
	if !backtest.AllInSizing().Leverage().Equal(decimal.NewFromInt(1)) {
		t.Errorf("AllInSizing leverage is %s, want 1", backtest.AllInSizing().Leverage())
	}

	// A negative one is a configuration error, not a short.
	bad := backtest.DefaultSizing()
	bad.MaxLeverage = decimal.NewFromInt(-2)
	if err := bad.Validate(); err == nil {
		t.Error("negative leverage was accepted")
	}
}

// TestLeverageRaisesTheCapProportionally, under the percentage model too, so
// the change is not confined to the venue that motivated it.
func TestLeverageRaisesTheCapProportionally(t *testing.T) {
	series := flatSeries(40, "27000")

	sizeAt := func(leverage string) decimal.Decimal {
		params := scoredParams(t, series, &enterOnceWithStop{
			stop:   decimal.RequireFromString("26999"), // a 1-wide stop: the cap must bind
			target: decimal.RequireFromString("28000"),
		})
		params.Costs = testCosts()
		params.InitialEquity = decimal.NewFromInt(10000)
		params.Sizing = backtest.Sizing{
			Mode:        backtest.SizingFixedFractional,
			RiskPct:     decimal.NewFromInt(1),
			MaxLeverage: decimal.RequireFromString(leverage),
		}

		result := runEngine(t, &fakeCandles{series: series}, nil, params)
		if len(result.Trades) != 1 {
			t.Fatalf("produced %d trades at %sx, want 1", len(result.Trades), leverage)
		}
		if result.EntriesSizeCapped != 1 {
			t.Fatalf("at %sx the cap did not bind; this test measures the cap", leverage)
		}
		return result.Trades[0].Size
	}

	atOne := sizeAt("1")
	atThree := sizeAt("3")

	// Compared within a tolerance rather than exactly: the cap comes from a
	// division and decimal.Div rounds at 16 places (ADR 0012), so multiplying
	// the rounded result back leaves a residue around 1e-16 — twenty orders of
	// magnitude below a satoshi, and deterministic.
	if !within(atThree, atOne.Mul(decimal.NewFromInt(3)), "0.000000001") {
		t.Errorf("3x leverage sized %s against %s at 1x; it should be three times", atThree, atOne)
	}
}
