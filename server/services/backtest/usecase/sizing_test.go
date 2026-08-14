package usecase_test

import (
	"context"
	"runtime"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/spioneracorei8/btcusd-trading-platform/server/services/backtest"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/strategy"
)

// enterOnceWithStop enters a single long with an explicit stop, so the size
// the engine chose can be checked against the arithmetic by hand.
type enterOnceWithStop struct {
	stop   decimal.Decimal
	target decimal.Decimal
	done   bool
}

func (s *enterOnceWithStop) OnBar(bar strategy.BarContext) []strategy.Intent {
	if s.done || bar.Position.IsOpen() {
		return nil
	}
	s.done = true
	return []strategy.Intent{strategy.EnterLong(s.stop, s.target, "sized entry")}
}
func (s *enterOnceWithStop) WarmupPeriod() int { return 0 }
func (s *enterOnceWithStop) Name() string      { return "enter_once_with_stop" }
func (s *enterOnceWithStop) Version() string   { return "v1" }

// TestFixedFractionalRisksExactlyTheConfiguredShare is §A4's arithmetic,
// checked against a figure computed independently of the engine.
//
// The whole point of sizing against the stop is that the loss at the stop is
// the same fraction of equity whatever the stop distance happens to be. If
// that does not hold exactly, "1% risk" is a label rather than a property.
func TestFixedFractionalRisksExactlyTheConfiguredShare(t *testing.T) {
	// Flat at 100 so the entry price is knowable: the fill is the next bar's
	// open plus one tick of slippage.
	series := flatSeries(40, "100")
	stop := decimal.RequireFromString("98")

	params := scoredParams(t, series, &enterOnceWithStop{
		stop:   stop,
		target: decimal.RequireFromString("110"),
	})
	params.Sizing = backtest.DefaultSizing() // 1%

	result := runEngine(t, &fakeCandles{series: series}, nil, params)
	if len(result.Trades) != 1 {
		t.Fatalf("produced %d trades, want 1", len(result.Trades))
	}
	trade := result.Trades[0]

	entry := decimal.RequireFromString("100").Add(testCosts().SlippageAmount())
	distance := entry.Sub(stop)
	risked := initialEquity.Mul(decimal.NewFromInt(1)).Div(decimal.NewFromInt(100))
	wantSize := risked.Div(distance)

	if !trade.Size.Equal(wantSize) {
		t.Errorf("size is %s, want %s (1%% of %s divided by a stop distance of %s)",
			trade.Size, wantSize, initialEquity, distance)
	}

	// And the property the arithmetic exists for: the loss at the stop is 1%
	// of equity, before costs.
	//
	// Compared within a tolerance rather than exactly, because size comes from
	// a division and decimal.Div rounds at 16 decimal places (ADR 0012).
	// Multiplying the rounded size back leaves a residue around 1e-16 — some
	// twenty orders of magnitude below a satoshi, and deterministic, which is
	// what determinism actually requires.
	lossAtStop := entry.Sub(stop).Mul(trade.Size)
	if !within(lossAtStop, risked, "0.000000001") {
		t.Errorf("a stop-out loses %s, want %s (1%% of equity)", lossAtStop, risked)
	}
	if result.EntriesSizeCapped != 0 {
		t.Errorf("EntriesSizeCapped = %d; a 2%% stop distance needs no cap", result.EntriesSizeCapped)
	}
}

// TestATightStopIsCappedAndCounted is the case that would otherwise report a
// risk it never took.
//
// A 0.1% stop distance at 1% risk implies ten times the account's notional.
// On a spot account that position cannot exist, so the size is capped at
// all-in — but a capped entry is no longer risking 1%, and a run where the cap
// binds often will show a drawdown far worse than the setting suggests with
// nothing else to explain it.
func TestATightStopIsCappedAndCounted(t *testing.T) {
	series := flatSeries(40, "100")

	// A stop 0.1 below a fill of ~100: one tenth of one percent.
	params := scoredParams(t, series, &enterOnceWithStop{
		stop:   decimal.RequireFromString("99.9"),
		target: decimal.RequireFromString("110"),
	})
	params.Sizing = backtest.DefaultSizing()

	result := runEngine(t, &fakeCandles{series: series}, nil, params)
	if len(result.Trades) != 1 {
		t.Fatalf("produced %d trades, want 1", len(result.Trades))
	}

	if result.EntriesSizeCapped != 1 {
		t.Errorf("EntriesSizeCapped = %d, want 1: a stop this tight cannot be sized at 1%% risk",
			result.EntriesSizeCapped)
	}

	// Capped means all-in, never more. A position larger than the account
	// could buy is leverage, which a spot backtest must not invent.
	entry := result.Trades[0].EntryPrice
	feeRate := testCosts().FeeTakerPct.Div(decimal.NewFromInt(100))
	affordable := initialEquity.Div(entry.Mul(decimal.NewFromInt(1).Add(feeRate)))

	if result.Trades[0].Size.GreaterThan(affordable) {
		t.Errorf("size %s exceeds what the account can buy (%s); that is leverage on a spot run",
			result.Trades[0].Size, affordable)
	}
}

// TestFixedFractionalRefusesToSizeWithoutAStop.
//
// Falling back to all-in silently would be the worst available behaviour: the
// run would report fixed-fractional sizing while committing the entire
// account on every trade.
func TestFixedFractionalRefusesToSizeWithoutAStop(t *testing.T) {
	series := flatSeries(40, "100")

	params := scoredParams(t, series, buyAndHold{}) // no stop
	params.Sizing = backtest.DefaultSizing()

	result, err := newEngine(&fakeCandles{series: series}, nil).Run(context.Background(), params)
	if err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}
	if len(result.Trades) != 0 {
		t.Errorf("a stop-less strategy produced %d trades under fixed-fractional sizing; "+
			"it should have been unable to size any", len(result.Trades))
	}
}

// TestSizingModeMustBeStated. The zero value is not a default, because a
// silent default here changes every number in the report.
func TestSizingModeMustBeStated(t *testing.T) {
	series := flatSeries(40, "100")

	params := scoredParams(t, series, alwaysFlat{})
	params.Sizing = backtest.Sizing{}

	if _, err := newEngine(&fakeCandles{series: series}, nil).Run(context.Background(), params); err == nil {
		t.Fatal("Run() accepted a run with no sizing mode")
	}
}

// TestRiskOutsideBoundsIsRejected.
func TestRiskOutsideBoundsIsRejected(t *testing.T) {
	for name, risk := range map[string]string{
		"zero":             "0",
		"negative":         "-1",
		"over the account": "101",
	} {
		sizing := backtest.Sizing{
			Mode:    backtest.SizingFixedFractional,
			RiskPct: decimal.RequireFromString(risk),
		}
		if err := sizing.Validate(); err == nil {
			t.Errorf("%s risk (%s%%) was accepted", name, risk)
		}
	}

	if err := backtest.DefaultSizing().Validate(); err != nil {
		t.Errorf("the shipped default is invalid: %v", err)
	}
	if err := backtest.AllInSizing().Validate(); err != nil {
		t.Errorf("all-in sizing is invalid: %v", err)
	}
}

// TestSizingIsIdenticalWhateverTheEquity checks that risk scales with the
// account rather than being a fixed notional in disguise.
//
// Doubling the starting equity must double the size at the same stop distance,
// so the fraction risked is what stays constant. It is the difference between
// a strategy that survives a drawdown and one that risks a growing share of a
// shrinking account.
func TestSizingIsIdenticalWhateverTheEquity(t *testing.T) {
	series := flatSeries(40, "100")
	stop := decimal.RequireFromString("98")

	sizeAt := func(equity decimal.Decimal) decimal.Decimal {
		t.Helper()

		params := scoredParams(t, series, &enterOnceWithStop{
			stop: stop, target: decimal.RequireFromString("110"),
		})
		params.Sizing = backtest.DefaultSizing()
		params.InitialEquity = equity

		result := runEngine(t, &fakeCandles{series: series}, nil, params)
		if len(result.Trades) != 1 {
			t.Fatalf("produced %d trades at equity %s, want 1", len(result.Trades), equity)
		}
		return result.Trades[0].Size
	}

	small := sizeAt(decimal.NewFromInt(10000))
	large := sizeAt(decimal.NewFromInt(20000))

	// Within a tolerance for the same reason as above: 20000/d and 2*(10000/d)
	// round at the sixteenth decimal place in different directions.
	if !within(large, small.Mul(decimal.NewFromInt(2)), "0.000000001") {
		t.Errorf("doubling equity gave size %s against %s; risk is not a constant fraction",
			large, small)
	}
}

// within reports whether two decimals agree to a stated tolerance.
//
// It exists for one reason: decimal.Div rounds, so a value that came through a
// division and back cannot be compared with Equal. The tolerance is always
// stated at the call site and is always far below anything the instrument
// could measure — this is rounding, not slack in the model.
func within(got, want decimal.Decimal, tolerance string) bool {
	return got.Sub(want).Abs().LessThanOrEqual(decimal.RequireFromString(tolerance))
}

// TestAStrategyInstanceCannotBeRunTwice is a regression test for a defect
// found by running the tooling rather than by reading it.
//
// --cost-sweep re-ran the same strategy instance at each cost multiple, so the
// 1.0x row — which is identical to the headline run by construction — reported
// a different trade count. The strategy had carried its moving averages and its
// pending-crossing flag into the second run and started mid-stream.
//
// That is the mild symptom. The same defect in --compare would have made the
// filtered and unfiltered runs incomparable, which is the entire purpose of the
// feature, and nothing in the output would have said so.
func TestAStrategyInstanceCannotBeRunTwice(t *testing.T) {
	series := risingSeries(80, 100)
	engine := newEngine(&fakeCandles{series: series}, nil)

	strat := &alternating{everyN: 5}
	params := scoredParams(t, series, strat)

	if _, err := engine.Run(context.Background(), params); err != nil {
		t.Fatalf("the first run failed: %v", err)
	}
	if _, err := engine.Run(context.Background(), params); err == nil {
		t.Fatal("the same strategy instance ran twice.\n" +
			"The second run started with the state the first left behind, so the " +
			"two results are not comparable — and a comparison is the only thing " +
			"two runs are ever for.")
	}
}

// TestFreshInstancesRunFreely is the other half, and the reason the guard
// holds a reference to each strategy it has seen.
//
// The first version keyed on the pointer address alone. Go reuses the address
// of a collected object, so the second freshly-built strategy landed where the
// first had been and was rejected as a repeat — a false positive, which is a
// worse failure than the one being prevented.
func TestFreshInstancesRunFreely(t *testing.T) {
	series := risingSeries(80, 100)
	engine := newEngine(&fakeCandles{series: series}, nil)

	// Enough iterations that the allocator would very likely reuse an address
	// if nothing were holding the earlier instances alive.
	for i := range 25 {
		params := scoredParams(t, series, &alternating{everyN: 5})
		if _, err := engine.Run(context.Background(), params); err != nil {
			t.Fatalf("run %d with a fresh strategy failed: %v", i, err)
		}
		runtime.GC()
	}
}
