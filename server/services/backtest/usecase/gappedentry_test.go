package usecase_test

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/backtest"
)

// gappedSeries is a flat market that jumps once, between the bar a decision is
// taken on and the bar the fill happens on.
//
// The jump is what the case needs: the stop is computed from a close, the fill
// happens at the next open, and nothing constrains the distance between them.
func gappedSeries(n, at int, before, after string) []models.Candle {
	series := make([]models.Candle, 0, n)
	for i := range n {
		price := before
		if i >= at {
			price = after
		}
		series = append(series, bar(seriesStart.Add(time.Duration(i)*time.Minute),
			price, price, price, price))
	}
	return series
}

// TestAPositionThatOpensPastItsOwnStopIsCounted.
//
// The engine takes the position and then closes it at the stop — a price the
// market did not trade at on that bar — so the trade is recorded as better
// than a real fill would have been. A long that gapped down comes out as a
// stop that made money.
//
// This test does not assert that behaviour is right. It asserts that it is
// counted, which is what makes the size of it knowable. See ADR 0023.
func TestAPositionThatOpensPastItsOwnStopIsCounted(t *testing.T) {
	// The decision is taken on the first scored bar and fills on the next
	// one, so that is where the jump has to land: flat at 100, then 80 from
	// the fill bar onward. The stop at 95 sits inside the jump.
	fillBar := firstScoredIndex(t) + 1
	series := gappedSeries(fillBar+20, fillBar, "100", "80")
	candles := &fakeCandles{series: series}

	strat := &longWithLevels{
		stop:   decimal.RequireFromString("95"),
		target: decimal.RequireFromString("130"),
	}
	params := scoredParams(t, series, strat)

	result := runEngine(t, candles, nil, params)

	if len(result.Trades) != 1 {
		t.Fatalf("expected one trade, got %d", len(result.Trades))
	}
	trade := result.Trades[0]

	if trade.ExitReason != backtest.ExitStop {
		t.Fatalf("ExitReason = %q, want stop", trade.ExitReason)
	}
	if !trade.EntryPrice.LessThan(decimal.RequireFromString("95")) {
		t.Fatalf("the fixture did not produce a gapped entry: filled at %s, stop at 95",
			trade.EntryPrice)
	}

	if result.EntriesBeyondStop != 1 {
		t.Errorf("EntriesBeyondStop = %d, want 1", result.EntriesBeyondStop)
	}
	if result.EntriesBeyondTarget != 0 {
		t.Errorf("EntriesBeyondTarget = %d, want 0", result.EntriesBeyondTarget)
	}

	// The consequence worth seeing: the exit is priced at the stop, which is
	// above the entry, so a long that gapped down is recorded as a gain.
	if !trade.ExitPrice.GreaterThan(trade.EntryPrice) {
		t.Errorf("exit %s is not above entry %s; the fixture no longer shows the flaw",
			trade.ExitPrice, trade.EntryPrice)
	}
	if !trade.GrossPnL.IsPositive() {
		t.Errorf("gross P&L = %s; the flaw this counts is that such a trade books a gain",
			trade.GrossPnL)
	}
}

// TestAPositionThatOpensPastItsOwnTargetIsCounted, which is the same fault in
// the flattering direction.
func TestAPositionThatOpensPastItsOwnTargetIsCounted(t *testing.T) {
	fillBar := firstScoredIndex(t) + 1
	series := gappedSeries(fillBar+20, fillBar, "100", "140")
	candles := &fakeCandles{series: series}

	strat := &longWithLevels{
		stop:   decimal.RequireFromString("70"),
		target: decimal.RequireFromString("130"),
	}
	result := runEngine(t, candles, nil, scoredParams(t, series, strat))

	if result.EntriesBeyondTarget != 1 {
		t.Errorf("EntriesBeyondTarget = %d, want 1", result.EntriesBeyondTarget)
	}
	if result.EntriesBeyondStop != 0 {
		t.Errorf("EntriesBeyondStop = %d, want 0", result.EntriesBeyondStop)
	}
}

// TestAnOrdinaryEntryIsNotCountedAsGapped, so a non-zero count means
// something when it appears.
func TestAnOrdinaryEntryIsNotCountedAsGapped(t *testing.T) {
	series := flatSeries(firstScoredIndex(t)+21, "100")
	candles := &fakeCandles{series: series}

	strat := &longWithLevels{
		stop:   decimal.RequireFromString("95"),
		target: decimal.RequireFromString("130"),
	}
	result := runEngine(t, candles, nil, scoredParams(t, series, strat))

	if result.EntriesBeyondStop != 0 || result.EntriesBeyondTarget != 0 {
		t.Errorf("a fill between its levels was counted as gapped: %d past stop, %d past target",
			result.EntriesBeyondStop, result.EntriesBeyondTarget)
	}
}
