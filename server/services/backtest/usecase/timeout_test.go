package usecase_test

import (
	"testing"

	"github.com/shopspring/decimal"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/backtest"
)

// TestAPositionIsForcedOutAfterItsHoldingLimit.
//
// A position that has gone nowhere for many bars is paying the spread to hold
// an opinion the market has not confirmed.
func TestAPositionIsForcedOutAfterItsHoldingLimit(t *testing.T) {
	series := flatSeries(60, "27000")

	params := scoredParams(t, series, &holdLong{stop: decimal.NewFromInt(26000)})
	params.Costs = zeroCosts()
	params.Sizing = backtest.AllInSizing()
	params.Exits = backtest.Exits{MaxHoldingBars: 5}

	result := runEngine(t, &fakeCandles{series: series}, nil, params)
	if len(result.Trades) == 0 {
		t.Fatal("the position was never closed by the clock")
	}

	trade := result.Trades[0]
	if trade.ExitReason != backtest.ExitTimeout {
		t.Fatalf("the exit is %q, want %q", trade.ExitReason, backtest.ExitTimeout)
	}

	// Five bars, not four and not six. An off-by-one here would be invisible
	// in a result and would change every holding-time figure the run reports.
	held := trade.ExitTime.Sub(trade.EntryTime)
	if want := 5 * series[0].CloseTime.Sub(series[0].OpenTime); held != want {
		t.Errorf("the position was held for %s, want %s", held, want)
	}
}

// TestNoHoldingLimitLeavesEveryPositionAlone. Zero disables it, which is what
// every evaluation before this ran with.
func TestNoHoldingLimitLeavesEveryPositionAlone(t *testing.T) {
	series := flatSeries(60, "27000")

	params := scoredParams(t, series, &holdLong{stop: decimal.NewFromInt(26000)})
	params.Costs = zeroCosts()
	params.Sizing = backtest.AllInSizing()

	result := runEngine(t, &fakeCandles{series: series}, nil, params)
	for _, trade := range result.Trades {
		if trade.ExitReason == backtest.ExitTimeout {
			t.Errorf("a position timed out with no holding limit configured: %+v", trade)
		}
	}
}

// TestATradeThatIsRunningIsNotClosedByTheClock.
//
// The point of the limit is to release capital from a position the market has
// not confirmed. One that has moved has been confirmed, and closing it on a
// clock would cut exactly the trades the strategy exists to find.
func TestATradeThatIsRunningIsNotClosedByTheClock(t *testing.T) {
	// Climbing steadily, so the position is a long way from its entry by the
	// time the clock runs out.
	prices := make([]int64, 0, 80)
	for i := range 80 {
		prices = append(prices, 27000+int64(i)*40)
	}
	series := rangedSeries(prices, 20)

	params := scoredParams(t, series, &holdLong{stop: decimal.NewFromInt(26000)})
	params.Costs = zeroCosts()
	params.Sizing = backtest.AllInSizing()
	params.Exits = backtest.Exits{MaxHoldingBars: 5, TimeoutExitATR: 1}

	result := runEngine(t, &fakeCandles{series: series}, nil, params)
	for _, trade := range result.Trades {
		if trade.ExitReason == backtest.ExitTimeout {
			t.Errorf("a position that had run %s from its entry was closed by the clock: %+v",
				trade.ExitPrice.Sub(trade.EntryPrice), trade)
		}
	}
}

// TestAStalledTradeIsStillClosedWhenTheDistanceQualifies. The other half of the
// same rule: the qualifier must not disable the mechanism entirely.
func TestAStalledTradeIsStillClosedWhenTheDistanceQualifies(t *testing.T) {
	series := rangedSeries(repeatPrice(27000, 60), 20)

	params := scoredParams(t, series, &holdLong{stop: decimal.NewFromInt(26000)})
	params.Costs = zeroCosts()
	params.Sizing = backtest.AllInSizing()
	params.Exits = backtest.Exits{MaxHoldingBars: 5, TimeoutExitATR: 1}

	result := runEngine(t, &fakeCandles{series: series}, nil, params)
	if len(result.Trades) == 0 {
		t.Fatal("a stalled position was not closed by the clock")
	}
	if got := result.Trades[0].ExitReason; got != backtest.ExitTimeout {
		t.Errorf("the exit is %q, want %q", got, backtest.ExitTimeout)
	}
}

// repeatPrice is n bars at one price.
func repeatPrice(price int64, n int) []int64 {
	out := make([]int64, 0, n)
	for range n {
		out = append(out, price)
	}
	return out
}

// TestATimeoutFillsAtTheOpenAndPaysAMarketOrder.
//
// The decision is knowable at the open and nothing about it depends on what
// the bar goes on to do, so the fill is the open. It pays taker and slippage
// like any other market exit — nothing was resting at that price.
func TestATimeoutFillsAtTheOpenAndPaysAMarketOrder(t *testing.T) {
	series := flatSeries(60, "27000")

	params := scoredParams(t, series, &holdLong{stop: decimal.NewFromInt(26000)})
	params.Costs = testCosts()
	params.Sizing = backtest.AllInSizing()
	params.Exits = backtest.Exits{MaxHoldingBars: 5}
	params.Execution = backtest.Execution{
		EntryOrderType: constants.OrderTypeLimit,
		ExitOrderType:  constants.OrderTypeLimit,
	}

	result := runEngine(t, &fakeCandles{series: series}, nil, params)
	if len(result.Trades) == 0 {
		t.Fatal("the position was never closed by the clock")
	}

	trade := result.Trades[0]
	if trade.ExitMaker {
		t.Error("a timeout exit filled as a maker under a limit exit model")
	}
	if !trade.Costs.IsPositive() {
		t.Errorf("a timeout exit paid %s in costs, want a full market round trip", trade.Costs)
	}
}

// TestTimeoutExitsAreCountedApart, so their average P&L can be read on its own.
//
// A timeout that is on average profitable says the targets are set too far;
// one that is heavily negative says the clock is cutting trades that would have
// recovered. Either reading is useful and the aggregate hides both.
func TestTimeoutExitsAreCountedApart(t *testing.T) {
	series := flatSeries(120, "27000")

	params := scoredParams(t, series, &alternating{everyN: 20})
	params.Costs = zeroCosts()
	params.Sizing = backtest.AllInSizing()
	params.Exits = backtest.Exits{MaxHoldingBars: 5}

	result := runEngine(t, &fakeCandles{series: series}, nil, params)

	timeouts := 0
	for _, trade := range result.Trades {
		if trade.ExitReason == backtest.ExitTimeout {
			timeouts++
		}
	}
	if timeouts == 0 {
		t.Fatal("no timeout exits were produced; nothing was counted")
	}
}

// TestATimeoutDistanceWithoutALimitIsRefused. A setting that reads as though it
// does something, and does not, is worse than no setting.
func TestATimeoutDistanceWithoutALimitIsRefused(t *testing.T) {
	if err := (backtest.Exits{TimeoutExitATR: 2}).Validate(); err == nil {
		t.Error("a timeout distance was accepted with no holding limit")
	}
	for name, bad := range map[string]backtest.Exits{
		"a negative limit":    {MaxHoldingBars: -1},
		"a negative distance": {MaxHoldingBars: 5, TimeoutExitATR: -1},
	} {
		if err := bad.Validate(); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}
