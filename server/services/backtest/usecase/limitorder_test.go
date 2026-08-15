package usecase_test

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/backtest"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/strategy"
)

// enterOnce asks to enter on one nominated bar and never again, so a test can
// place a resting order at a price it chose and then control what the next
// bars do to it.
type enterOnce struct {
	atIndex int
	stop    decimal.Decimal
	target  decimal.Decimal
	seen    int
}

func (e *enterOnce) OnBar(strategy.BarContext) []strategy.Intent {
	e.seen++
	if e.seen-1 != e.atIndex {
		return nil
	}
	return []strategy.Intent{strategy.EnterLong(e.stop, e.target, "limit test")}
}
func (e *enterOnce) WarmupPeriod() int { return 0 }
func (e *enterOnce) Name() string      { return "enter_once" }
func (e *enterOnce) Version() string   { return "v1" }

// limitCosts separates the two rates so a fill can be attributed to one of
// them by arithmetic rather than by trusting a flag.
func limitCosts() backtest.Costs {
	return backtest.Costs{
		FeeTakerPct:   decimal.RequireFromString("0.10"),
		FeeMakerPct:   decimal.RequireFromString("0.02"),
		SlippageTicks: 1,
		TickSize:      decimal.RequireFromString("1"),
	}
}

// scenario prepends flat warm-up bars to the bars a test actually cares about,
// and returns the series with the index the scenario starts at.
//
// The engine scores nothing until its indicators have converged, so a handful
// of hand-written bars would otherwise be consumed entirely by warm-up. The
// warm-up bars are flat at the scenario's opening price, so they settle the
// indicators without moving anything the test then asserts on.
func scenario(t *testing.T, price string, bars ...models.Candle) ([]models.Candle, int) {
	t.Helper()

	lead := warmupBars(t)
	series := make([]models.Candle, 0, lead+len(bars))
	for i := range lead {
		series = append(series, candleAt(i, price, price, price, price))
	}
	for i, bar := range bars {
		series = append(series, shiftTo(bar, lead+i))
	}
	return series, lead
}

// shiftTo moves a hand-written bar to its place in the series.
func shiftTo(bar models.Candle, index int) models.Candle {
	at := seriesStart.Add(time.Duration(index) * time.Minute)
	bar.OpenTime = at
	bar.CloseTime = at.Add(time.Minute)
	return bar
}

// candleAt builds one bar with an explicit range.
func candleAt(index int, open, high, low, closePrice string) models.Candle {
	at := seriesStart.Add(time.Duration(index) * time.Minute)
	return models.Candle{
		Symbol: "BTCUSDT", MarketType: constants.MarketTypeSpot,
		Timeframe: constants.Timeframe1m,
		OpenTime:  at, CloseTime: at.Add(time.Minute),
		Open:  decimal.RequireFromString(open),
		High:  decimal.RequireFromString(high),
		Low:   decimal.RequireFromString(low),
		Close: decimal.RequireFromString(closePrice),
		// Only closed candles ever reach the engine (CLAUDE.md §3.1).
		IsClosed: true,
	}
}

// limitParams runs a series with a limit entry and the given timeout.
func limitParams(series []models.Candle, from int, strat strategy.Strategy, timeout int) backtest.RunParams {
	params := runParams(series, strat)
	params.From = series[from].OpenTime
	params.Costs = limitCosts()
	params.Sizing = backtest.AllInSizing()
	params.Execution = backtest.Execution{
		EntryOrderType:   constants.OrderTypeLimit,
		LimitTimeoutBars: timeout,
	}
	return params
}

// TestABuyLimitFillsWhenTheBarReachesDownToIt.
//
// The order rests at the close of the signal bar. A later bar whose low
// reaches that price would have traded through the order, so it fills — at the
// limit price, not at the bar's open.
func TestABuyLimitFillsWhenTheBarReachesDownToIt(t *testing.T) {
	series, start := scenario(t, "100",
		candleAt(0, "100", "100", "100", "100"), // signal bar: limit rests at 100
		candleAt(0, "105", "106", "99", "104"),  // low 99 <= 100, so it fills
		candleAt(0, "104", "108", "103", "107"),
		candleAt(0, "107", "109", "106", "108"),
	)

	result := runEngine(t, &fakeCandles{series: series}, nil,
		limitParams(series, start, &enterOnce{atIndex: 0}, 2))

	if result.MakerEntries != 1 {
		t.Fatalf("got %d maker entries, want 1 (the resting order should have filled)",
			result.MakerEntries)
	}
	if result.TakerEntries != 0 {
		t.Errorf("a resting order was filled as a taker %d time(s)", result.TakerEntries)
	}
	if result.LimitOrdersExpired != 0 {
		t.Errorf("%d orders expired though the price was reached", result.LimitOrdersExpired)
	}

	// The fill price is the limit itself. Slippage is what crossing the spread
	// costs, and a resting order does not cross it.
	if len(result.Trades) == 0 {
		t.Fatal("no trade was recorded")
	}
	if !result.Trades[0].EntryPrice.Equal(decimal.RequireFromString("100")) {
		t.Errorf("filled at %s, want the limit price 100", result.Trades[0].EntryPrice)
	}
	if !result.Trades[0].EntryMaker {
		t.Error("the trade does not record its entry as a maker fill")
	}
}

// TestABuyLimitDoesNotFillWhenThePriceStaysAbove, and the signal produces no
// trade at all. This is the half that makes maker fees honest: cheaper fills
// that always happened would be a strictly better strategy than the one being
// tested.
func TestABuyLimitDoesNotFillWhenThePriceStaysAbove(t *testing.T) {
	series, start := scenario(t, "100",
		candleAt(0, "100", "100", "100", "100"), // limit rests at 100
		candleAt(0, "105", "106", "101", "104"), // low 101 > 100
		candleAt(0, "106", "108", "102", "107"),
		candleAt(0, "108", "110", "104", "109"),
	)

	result := runEngine(t, &fakeCandles{series: series}, nil,
		limitParams(series, start, &enterOnce{atIndex: 0}, 1))

	if len(result.Trades) != 0 {
		t.Errorf("%d trades happened though the limit was never reached", len(result.Trades))
	}
	if result.MakerEntries != 0 || result.TakerEntries != 0 {
		t.Errorf("an entry filled: %d maker, %d taker", result.MakerEntries, result.TakerEntries)
	}
	if result.LimitOrdersExpired != 1 {
		t.Errorf("%d orders were recorded as cancelled, want 1", result.LimitOrdersExpired)
	}
	if result.EntriesRequested != 1 {
		t.Errorf("%d entries were requested, want 1 — the signal must still be counted",
			result.EntriesRequested)
	}
}

// TestAnOrderRestsForItsTimeoutAndNoLonger.
//
// The bar that touches the limit arrives after the order should have been
// cancelled, so it must not fill. Getting this wrong by one bar would let a
// strategy pick up fills from arbitrarily far in the future.
func TestAnOrderRestsForItsTimeoutAndNoLonger(t *testing.T) {
	series, start := scenario(t, "100",
		candleAt(0, "100", "100", "100", "100"), // limit rests at 100
		candleAt(0, "105", "106", "101", "104"), // no touch — order ages
		candleAt(0, "106", "108", "102", "107"), // no touch — order expires
		candleAt(0, "104", "105", "98", "99"),   // touches 100, but too late
		candleAt(0, "99", "101", "97", "100"),
	)

	result := runEngine(t, &fakeCandles{series: series}, nil,
		limitParams(series, start, &enterOnce{atIndex: 0}, 2))

	if len(result.Trades) != 0 {
		t.Errorf("%d trades filled from a bar after the order had expired", len(result.Trades))
	}
	if result.LimitOrdersExpired != 1 {
		t.Errorf("%d orders expired, want 1", result.LimitOrdersExpired)
	}
}

// TestALimitFillPaysMakerAndNoSlippage, checked by arithmetic rather than by
// reading the flag back.
func TestALimitFillPaysMakerAndNoSlippage(t *testing.T) {
	series, start := scenario(t, "100",
		candleAt(0, "100", "100", "100", "100"),
		candleAt(0, "101", "102", "100", "101"), // touches 100, fills at 100
		candleAt(0, "101", "102", "100", "101"),
		candleAt(0, "101", "102", "100", "101"),
	)

	params := limitParams(series, start, &enterOnce{atIndex: 0}, 2)
	result := runEngine(t, &fakeCandles{series: series}, nil, params)

	if len(result.Trades) != 1 {
		t.Fatalf("got %d trades, want 1", len(result.Trades))
	}
	trade := result.Trades[0]

	// Entry: maker on the notional at the limit price. Exit: end-of-run, which
	// is a market order and pays taker plus one side of slippage.
	entryNotional := trade.EntryPrice.Mul(trade.Size)
	expectedEntryFee := entryNotional.Mul(decimal.RequireFromString("0.02")).Div(decimal.NewFromInt(100))
	exitNotional := trade.ExitPrice.Mul(trade.Size)
	expectedExitFee := exitNotional.Mul(decimal.RequireFromString("0.10")).Div(decimal.NewFromInt(100))

	if diff := trade.Fees.Sub(expectedEntryFee.Add(expectedExitFee)).Abs(); diff.GreaterThan(decimal.RequireFromString("0.01")) {
		t.Errorf("fees are %s, want %s (maker entry %s + taker exit %s)",
			trade.Fees, expectedEntryFee.Add(expectedExitFee), expectedEntryFee, expectedExitFee)
	}

	// One tick of slippage, on the market exit only. The old model charged two
	// sides unconditionally, which would double this.
	oneSide := params.Costs.SlippageAmount().Mul(trade.Size)
	if !trade.Slippage.Equal(oneSide) {
		t.Errorf("slippage is %s, want %s — a resting entry crosses no spread",
			trade.Slippage, oneSide)
	}
}

// TestAStopExitPaysTakerEvenUnderALimitExitModel is §7.3, and it is the rule
// with the most riding on it.
//
// A stop that only filled at its limit price is a stop that does not fill when
// the market gaps through it — precisely the situation stops exist for.
// Modelling one as a resting order would delete the worst losses from the
// record and produce a strategy that looks robust because its tail was removed.
func TestAStopExitPaysTakerEvenUnderALimitExitModel(t *testing.T) {
	series, start := scenario(t, "100",
		candleAt(0, "100", "100", "100", "100"),
		candleAt(0, "100", "101", "99", "100"),
		// Gaps straight through the stop at 95.
		candleAt(0, "90", "91", "88", "89"),
		candleAt(0, "89", "90", "87", "88"),
	)

	params := runParams(series, &enterOnce{
		atIndex: 0,
		stop:    decimal.RequireFromString("95"),
		target:  decimal.RequireFromString("130"),
	})
	params.From = series[start].OpenTime
	params.Costs = limitCosts()
	params.Sizing = backtest.AllInSizing()
	// Both sides configured as limit. The stop must ignore it.
	params.Execution = backtest.Execution{
		EntryOrderType: constants.OrderTypeMarket,
		ExitOrderType:  constants.OrderTypeLimit,
	}

	result := runEngine(t, &fakeCandles{series: series}, nil, params)

	if len(result.Trades) != 1 {
		t.Fatalf("got %d trades, want 1", len(result.Trades))
	}
	trade := result.Trades[0]

	if trade.ExitReason != backtest.ExitStop {
		t.Fatalf("the position left by %q, want a stop", trade.ExitReason)
	}
	if trade.ExitMaker {
		t.Error("a stop exit was recorded as a maker fill. A stop is a market order " +
			"under every configuration, or the tail of the loss distribution is deleted.")
	}
	if result.MakerExits != 0 {
		t.Errorf("%d maker exits under a stop-only run", result.MakerExits)
	}

	// And it paid slippage, which a resting order would not have.
	if !trade.Slippage.IsPositive() {
		t.Error("the stop exit paid no slippage")
	}
}

// TestATargetExitRestsWhenConfiguredTo. The other side of 7.3: reaching a
// target means price came to a price the position was already willing to sell
// at, which is what a resting order is.
func TestATargetExitRestsWhenConfiguredTo(t *testing.T) {
	series, start := scenario(t, "100",
		candleAt(0, "100", "100", "100", "100"),
		candleAt(0, "100", "101", "99", "100"),
		candleAt(0, "101", "112", "100", "111"), // reaches the target at 110
		candleAt(0, "111", "112", "110", "111"),
	)

	params := runParams(series, &enterOnce{
		atIndex: 0,
		stop:    decimal.RequireFromString("80"),
		target:  decimal.RequireFromString("110"),
	})
	params.From = series[start].OpenTime
	params.Costs = limitCosts()
	params.Sizing = backtest.AllInSizing()
	params.Execution = backtest.Execution{ExitOrderType: constants.OrderTypeLimit}

	result := runEngine(t, &fakeCandles{series: series}, nil, params)

	if len(result.Trades) != 1 {
		t.Fatalf("got %d trades, want 1", len(result.Trades))
	}
	trade := result.Trades[0]

	if trade.ExitReason != backtest.ExitTarget {
		t.Fatalf("the position left by %q, want a target", trade.ExitReason)
	}
	if !trade.ExitMaker {
		t.Error("a target exit under a limit exit model was charged as a taker fill")
	}
	if !trade.ExitPrice.Equal(decimal.RequireFromString("110")) {
		t.Errorf("filled at %s, want the target price 110 with no slippage", trade.ExitPrice)
	}
}

// TestFillCountsSumToTheTradeCount, so an entry that filled by some path
// nobody counted cannot hide.
func TestFillCountsSumToTheTradeCount(t *testing.T) {
	series := risingSeries(120, 100)

	for _, testCase := range []struct {
		name      string
		execution backtest.Execution
	}{
		{"market", backtest.Execution{}},
		{"limit entry", backtest.Execution{
			EntryOrderType: constants.OrderTypeLimit, LimitTimeoutBars: 3}},
		{"limit exit", backtest.Execution{ExitOrderType: constants.OrderTypeLimit}},
		{"both", backtest.Execution{
			EntryOrderType:   constants.OrderTypeLimit,
			ExitOrderType:    constants.OrderTypeLimit,
			LimitTimeoutBars: 3}},
	} {
		params := scoredParams(t, series, &alternating{everyN: 9})
		params.Costs = limitCosts()
		params.Sizing = backtest.AllInSizing()
		params.Execution = testCase.execution

		result := runEngine(t, &fakeCandles{series: series}, nil, params)

		entries := result.MakerEntries + result.TakerEntries
		exits := result.MakerExits + result.TakerExits

		if entries != int64(len(result.Trades)) {
			t.Errorf("%s: %d entry fills for %d trades", testCase.name, entries, len(result.Trades))
		}
		if exits != int64(len(result.Trades)) {
			t.Errorf("%s: %d exit fills for %d trades", testCase.name, exits, len(result.Trades))
		}
		// Every requested entry either filled or was cancelled.
		if result.EntriesRequested != entries+result.LimitOrdersExpired {
			t.Errorf("%s: %d signals, but %d filled and %d cancelled",
				testCase.name, result.EntriesRequested, entries, result.LimitOrdersExpired)
		}
	}
}

// TestAnUnconfiguredExecutionModelChargesTakerOnBothSides.
//
// The zero value has to be the conservative one. A run that never mentioned
// order types must pay what it always paid, or every result produced before
// this change silently becomes a different measurement.
func TestAnUnconfiguredExecutionModelChargesTakerOnBothSides(t *testing.T) {
	series := risingSeries(120, 100)

	params := scoredParams(t, series, &alternating{everyN: 9})
	params.Sizing = backtest.AllInSizing()
	// Costs with no maker rate at all, as every fixture had before.
	params.Costs = backtest.Costs{
		FeeTakerPct:   decimal.RequireFromString("0.05"),
		SlippageTicks: 1,
		TickSize:      decimal.RequireFromString("0.01"),
	}

	result := runEngine(t, &fakeCandles{series: series}, nil, params)

	if result.MakerEntries != 0 || result.MakerExits != 0 {
		t.Errorf("an unconfigured run produced %d maker entries and %d maker exits",
			result.MakerEntries, result.MakerExits)
	}
	if !params.Costs.MakerFeePct().Equal(params.Costs.FeeTakerPct) {
		t.Errorf("an unset maker rate is %s, want the taker rate %s — a zero-value "+
			"Costs must not make trading cheaper",
			params.Costs.MakerFeePct(), params.Costs.FeeTakerPct)
	}
}
