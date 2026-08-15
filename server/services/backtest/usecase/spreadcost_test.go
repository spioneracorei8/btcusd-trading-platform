package usecase_test

import (
	"testing"

	"github.com/shopspring/decimal"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/backtest"
)

// spreadCosts is the venue model under test: an IUX Standard account trading
// BTCUSD as a CFD.
//
// 2500 points at 0.01 of price is a 25.00 spread, which is 0.25 across a round
// trip at the 0.01 lot minimum. Commission is zero, because on a Standard
// account the cost is entirely in the spread; the Raw variant is tested
// separately.
func spreadCosts() backtest.Costs {
	return backtest.Costs{
		// Still set, and deliberately: a percentage rate left lying around
		// must have no effect once the spread model is in force. If one of
		// these leaks into the arithmetic, the figures below move.
		FeeTakerPct: decimal.RequireFromString(testFeePct),

		Model:        constants.CostModelSpread,
		SpreadPoints: 2500,
		PointValue:   decimal.RequireFromString("0.01"),

		ContractSize: decimal.NewFromInt(1),
		MinLot:       decimal.RequireFromString("0.01"),
		LotStep:      decimal.RequireFromString("0.01"),

		CommissionPerLot: decimal.Zero,

		SlippageTicks: 0,
		TickSize:      decimal.RequireFromString(testTick),
	}
}

// TestTheDefaultCostModelIsPercentage guards the promise that made this change
// safe to make: a Costs built without mentioning a model prices exactly as it
// did before the model existed.
//
// The golden-file test proves it end to end over a real run. This proves it at
// the one line the whole guarantee rests on, so a regression here is reported
// as itself rather than as 28 trades that moved for no stated reason.
func TestTheDefaultCostModelIsPercentage(t *testing.T) {
	var zero backtest.Costs
	if zero.CostModel() != constants.CostModelPercentage {
		t.Errorf("a zero-value Costs reports %q, want %q",
			zero.CostModel(), constants.CostModelPercentage)
	}

	// And the venue fields do nothing while it is in force. A percentage run
	// keeps continuous sizing, which is what lets every earlier evaluation
	// stay comparable with a later one.
	percentage := testCosts()
	percentage.MinLot = decimal.RequireFromString("0.01")
	percentage.LotStep = decimal.RequireFromString("0.01")
	percentage.ContractSize = decimal.NewFromInt(1)
	if percentage.LotConstrained() {
		t.Error("a percentage run is lot-constrained; sizing would change under settings that model a different venue")
	}
}

// TestSpreadCostDoesNotDependOnThePriceLevel is the whole reason the model
// exists.
//
// On the venue, a quoted spread of 25 USD costs 25 USD to cross whether BTC is
// at 20,000 or at 100,000. A percentage of notional would charge five times as
// much at the higher price for the identical trade, which at 1m — where a bar's
// range and the spread are the same order of magnitude — misprices every trade
// in the model, and in opposite directions depending on where price happens to
// be sitting.
func TestSpreadCostDoesNotDependOnThePriceLevel(t *testing.T) {
	costAt := func(t *testing.T, price string) decimal.Decimal {
		t.Helper()

		// Flat, so nothing but the cost model can move equity, and a round
		// trip every other bar so there is something to measure.
		series := flatSeries(40, price)
		params := scoredParams(t, series, &alternating{everyN: 2})
		params.Costs = spreadCosts()

		// All-in, so the two runs hold different quantities at different price
		// levels. That is fine and deliberate: the figure compared below is the
		// cost *per unit held*, which is exactly the thing that must not move.
		params.Sizing = backtest.AllInSizing()

		result := runEngine(t, &fakeCandles{series: series}, nil, params)
		if len(result.Trades) == 0 {
			t.Fatalf("no trades at price %s; nothing to price", price)
		}
		return result.Trades[0].Costs.Div(result.Trades[0].Size)
	}

	low := costAt(t, "20000")
	high := costAt(t, "100000")

	// Cost per unit held, which is the figure that must not move. The round
	// trip is 25.00 of price: half a spread each side, twice.
	want := decimal.RequireFromString("25")
	for name, got := range map[string]decimal.Decimal{"at 20,000": low, "at 100,000": high} {
		if !got.Equal(want) {
			t.Errorf("a round trip %s costs %s per unit, want %s", name, got, want)
		}
	}
	if !low.Equal(high) {
		t.Errorf("the same trade costs %s at 20,000 and %s at 100,000; "+
			"the spread model is still scaling with the price level", low, high)
	}
}

// TestASingleRoundTripCostsTheQuotedSpreadOnce checks the arithmetic against
// the venue's own figure, in the units it is quoted in.
//
// This is the number the whole cost model was rebuilt to produce: 0.25 USD per
// round trip at the minimum lot, against 0.63 for the same size on Binance at
// 63,000. Charging a full spread on each side instead of half would double it
// — an error in the safe direction, and still an error, because it would make
// a viable strategy look like half of one.
func TestASingleRoundTripCostsTheQuotedSpreadOnce(t *testing.T) {
	series := flatSeries(40, "63000")

	params := scoredParams(t, series, &alternating{everyN: 2})
	params.Costs = spreadCosts()

	// Enough equity to reach a tradeable size — a 100 USD balance at 63,000
	// cannot, which is the subject of its own test below. The venue's own
	// figure is then recovered from the per-unit cost.
	params.Sizing = backtest.AllInSizing()

	result := runEngine(t, &fakeCandles{series: series}, nil, params)
	if len(result.Trades) == 0 {
		t.Fatal("no trades; nothing to price")
	}

	trade := result.Trades[0]
	perUnit := trade.Costs.Div(trade.Size)
	if !perUnit.Equal(decimal.NewFromInt(25)) {
		t.Fatalf("a round trip costs %s per unit held, want 25 (the quoted spread, crossed once)", perUnit)
	}

	// The same figure at the size the account can actually trade.
	atMinimumLot := perUnit.Mul(decimal.RequireFromString("0.01"))
	if !atMinimumLot.Equal(decimal.RequireFromString("0.25")) {
		t.Errorf("a round trip at the 0.01 lot minimum costs %s, want 0.25", atMinimumLot)
	}
}

// TestCommissionIsChargedPerLotPerSide covers the Raw account, where part of
// the cost moves out of the spread and into an explicit charge.
//
// Per side rather than per round trip: 4 USD per lot on a Raw account is 8 USD
// across a round trip of one lot, and modelling it as 4 would understate every
// result by half a commission.
func TestCommissionIsChargedPerLotPerSide(t *testing.T) {
	series := flatSeries(40, "63000")

	base := spreadCosts()
	base.SpreadPoints = 0 // Isolate the commission from the spread.

	withCommission := base
	withCommission.CommissionPerLot = decimal.NewFromInt(4)

	run := func(t *testing.T, costs backtest.Costs) backtest.Trade {
		t.Helper()

		params := scoredParams(t, series, &alternating{everyN: 2})
		params.Costs = costs
		params.Sizing = backtest.AllInSizing()

		result := runEngine(t, &fakeCandles{series: series}, nil, params)
		if len(result.Trades) == 0 {
			t.Fatal("no trades; nothing to price")
		}
		return result.Trades[0]
	}

	// Standard: zero commission, and with the spread removed the round trip is
	// free. Checked so that the figure below is known to be commission alone.
	standard := run(t, base)
	if !standard.Costs.IsZero() {
		t.Errorf("with no spread and no commission a round trip cost %s, want 0", standard.Costs)
	}

	raw := run(t, withCommission)
	perLot := raw.Costs.Div(raw.Size) // ContractSize is 1, so size is lots.
	if !perLot.Equal(decimal.NewFromInt(8)) {
		t.Errorf("a round trip paid %s per lot, want 8 (4 per side)", perLot)
	}
}

// TestSizeIsRoundedDownToTheLotGrid checks that a size the venue cannot trade
// becomes one it can, in the direction that risks less.
//
// Rounding to nearest would sometimes round up, and rounding up is taking a
// larger position than the strategy asked for. On this account the difference
// between 0.01 and 0.02 lot is a doubling of risk.
func TestSizeIsRoundedDownToTheLotGrid(t *testing.T) {
	series := flatSeries(40, "1000")

	// A 100-wide stop at 1% of 10,000 equity solves for 1.0 exactly; a 99-wide
	// one solves for 1.0101..., which the grid must cut to 1.01 rather than
	// carry as a size no order could express.
	params := scoredParams(t, series, &enterOnceWithStop{
		stop:   decimal.RequireFromString("901"),
		target: decimal.RequireFromString("2000"),
	})
	params.Costs = spreadCosts()
	params.Sizing = backtest.DefaultSizing() // 1% risk

	result := runEngine(t, &fakeCandles{series: series}, nil, params)
	if len(result.Trades) != 1 {
		t.Fatalf("produced %d trades, want 1", len(result.Trades))
	}

	size := result.Trades[0].Size
	if !size.Equal(decimal.RequireFromString("1.01")) {
		t.Fatalf("size is %s, want 1.01 (100/99 floored onto a 0.01 step)", size)
	}

	// And the property the flooring exists for: never more than was asked.
	entry := decimal.RequireFromString("1000")
	wanted := initialEquity.Div(decimal.NewFromInt(100)).
		Div(entry.Sub(decimal.RequireFromString("901")))
	if size.GreaterThan(wanted) {
		t.Errorf("size %s exceeds the %s the strategy sized for; the grid rounded up", size, wanted)
	}
}

// TestATradeBelowTheMinimumLotIsRefusedAndCounted is the rule that keeps a
// small account honest.
//
// The alternative — taking it at the minimum anyway — is a different and
// larger bet than the strategy asked for, and it is how an account dies while
// the report still describes the strategy that was tested. So the trade does
// not happen, and the refusals are counted, because on a 100 USD balance there
// may be a great many of them and that is a fact about the account rather than
// about the strategy.
func TestATradeBelowTheMinimumLotIsRefusedAndCounted(t *testing.T) {
	series := flatSeries(40, "63000")

	// 100 USD of equity, 1% risk, a stop 630 away: 1 USD of risk over 630 of
	// distance is 0.0015873 BTC — a sixth of the smallest lot the venue will
	// accept.
	params := scoredParams(t, series, &enterOnceWithStop{
		stop:   decimal.RequireFromString("62370"),
		target: decimal.RequireFromString("64000"),
	})
	params.Costs = spreadCosts()
	params.InitialEquity = decimal.NewFromInt(100)
	params.Sizing = backtest.DefaultSizing() // 1% risk

	result := runEngine(t, &fakeCandles{series: series}, nil, params)

	if len(result.Trades) != 0 {
		t.Fatalf("took %d trades at a size below the venue minimum; the position was rounded up rather than refused",
			len(result.Trades))
	}
	if result.EntriesBelowMinLot != 1 {
		t.Errorf("EntriesBelowMinLot = %d, want 1; a refused entry that is not counted is invisible",
			result.EntriesBelowMinLot)
	}

	// Equity untouched: a refusal is not a trade that lost nothing, it is no
	// trade at all.
	if !result.Equity[len(result.Equity)-1].Equity.Equal(params.InitialEquity) {
		t.Errorf("equity ended at %s from %s; a refused entry moved the account",
			result.Equity[len(result.Equity)-1].Equity, params.InitialEquity)
	}
}

// TestTheLotGridDoesNotApplyUnderThePercentageModel is what keeps every
// earlier evaluation comparable with a later one.
//
// The lot constraint arrives with the venue, so it is tied to the venue's cost
// model rather than to MIN_LOT happening to hold a value. Were it otherwise, a
// stray setting in an env file would silently change the sizing of a run whose
// report says nothing about lots.
func TestTheLotGridDoesNotApplyUnderThePercentageModel(t *testing.T) {
	series := flatSeries(40, "63000")

	params := scoredParams(t, series, &enterOnceWithStop{
		stop:   decimal.RequireFromString("62370"),
		target: decimal.RequireFromString("64000"),
	})
	params.Costs = testCosts()
	params.Costs.MinLot = decimal.RequireFromString("0.01")
	params.Costs.LotStep = decimal.RequireFromString("0.01")
	params.Costs.ContractSize = decimal.NewFromInt(1)
	params.InitialEquity = decimal.NewFromInt(100)
	params.Sizing = backtest.DefaultSizing() // 1% risk

	result := runEngine(t, &fakeCandles{series: series}, nil, params)

	if len(result.Trades) != 1 {
		t.Fatalf("produced %d trades, want 1; the percentage model gained a lot constraint", len(result.Trades))
	}
	if result.EntriesBelowMinLot != 0 {
		t.Errorf("EntriesBelowMinLot = %d under the percentage model, want 0", result.EntriesBelowMinLot)
	}

	// Continuous, not on any grid: the size is whatever the risk arithmetic
	// asked for.
	if result.Trades[0].Size.Equal(result.Trades[0].Size.Round(2)) {
		t.Errorf("size %s landed exactly on a 0.01 grid; sizing was quantised", result.Trades[0].Size)
	}
}
