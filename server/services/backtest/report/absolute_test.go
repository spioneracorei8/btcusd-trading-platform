package report_test

import (
	"bytes"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/backtest"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/backtest/report"
)

// riskedResult is three trades with known stop distances and sizes, so the
// risk statistics have an answer that can be worked out on paper.
//
//	entry 100, stop  98, size 2   ->  2 x 2 =  4 risked
//	entry 100, stop  90, size 1   -> 10 x 1 = 10 risked
//	entry 100, stop  99, size 0.5 ->  1 x 0.5 = 0.5 risked
//
// Average 4.8333..., worst 10, against a 100 balance.
func riskedResult() backtest.Result {
	params := backtest.RunParams{
		Symbol:        "BTCUSD",
		MarketType:    constants.MarketTypeSpot,
		Timeframe:     constants.Timeframe1m,
		From:          reportStart,
		To:            reportStart.Add(10 * time.Minute),
		InitialEquity: dec("100"),
		Costs: backtest.Costs{
			Model:            constants.CostModelSpread,
			SpreadPoints:     2500,
			PointValue:       dec("0.01"),
			ContractSize:     dec("1"),
			MinLot:           dec("0.01"),
			LotStep:          dec("0.01"),
			CommissionPerLot: decimal.Zero,
			SlippageTicks:    1,
			TickSize:         dec("0.01"),
		},
		GapPolicy: backtest.GapHalt,
	}

	trade := func(minute int, stop, size, net string) backtest.Trade {
		return backtest.Trade{
			Direction:  constants.DirectionLong,
			EntryTime:  reportStart.Add(time.Duration(minute) * time.Minute),
			EntryPrice: dec("100"),
			ExitTime:   reportStart.Add(time.Duration(minute+1) * time.Minute),
			ExitPrice:  dec("101"),
			StopPrice:  dec(stop),
			Size:       dec(size),
			GrossPnL:   dec(net).Add(dec("0.25")),
			Costs:      dec("0.25"),
			NetPnL:     dec(net),
			ExitReason: backtest.ExitTarget,
		}
	}

	return backtest.Result{
		Params:          params,
		StrategyName:    "fixture",
		StrategyVersion: "v1",
		FirstBar:        reportStart,
		LastBar:         reportStart.Add(6 * time.Minute),
		BarsEvaluated:   7,
		Trades: []backtest.Trade{
			trade(0, "98", "2", "10"),
			trade(2, "90", "1", "-45"),
			trade(4, "99", "0.5", "5"),
		},
		// Peak 110 at minute 1, trough 65 at minute 3: a 45 fall, which on a
		// 100 balance is 45% and 45 USD — the same number, and only the second
		// one answers whether it could be sat through.
		Equity: []backtest.EquityPoint{
			{OpenTime: reportStart, Equity: dec("100")},
			{OpenTime: reportStart.Add(time.Minute), Equity: dec("110")},
			{OpenTime: reportStart.Add(2 * time.Minute), Equity: dec("110")},
			{OpenTime: reportStart.Add(3 * time.Minute), Equity: dec("65")},
			{OpenTime: reportStart.Add(4 * time.Minute), Equity: dec("70")},
		},
	}
}

// TestRiskIsMeasuredFromTheStopNotTheOutcome checks the arithmetic against
// figures computed by hand.
//
// Risk is the distance to the stop, not the loss that happened. A trade that
// closed at its target still risked the stop distance, and it is that distance
// which decides how much of the account a losing streak consumes — the number
// that matters on a 100 USD balance where 0.01 lot is the smallest bet
// available.
func TestRiskIsMeasuredFromTheStopNotTheOutcome(t *testing.T) {
	stats := report.Compute(riskedResult())

	// (4 + 10 + 0.5) / 3
	wantAverage := dec("14.5").Div(dec("3"))
	if !stats.AverageRisk.Equal(wantAverage) {
		t.Errorf("average risk is %s, want %s", stats.AverageRisk, wantAverage)
	}
	if !stats.WorstRisk.Equal(dec("10")) {
		t.Errorf("worst risk is %s, want 10", stats.WorstRisk)
	}

	// As a share of the balance: 4.8333/100 and 10/100.
	wantAveragePct, _ := wantAverage.Div(dec("100")).Float64()
	if math.Abs(stats.AverageRiskPct-wantAveragePct) > 1e-12 {
		t.Errorf("average risk is %v of balance, want %v", stats.AverageRiskPct, wantAveragePct)
	}
	if math.Abs(stats.WorstRiskPct-0.1) > 1e-12 {
		t.Errorf("worst risk is %v of balance, want 0.1", stats.WorstRiskPct)
	}

	// The middle trade lost 45 and risked 10. Measuring from the outcome would
	// report 45 as the worst risk, which is the realised loss of a trade that
	// gapped through its stop rather than the risk it was opened with.
	if stats.WorstRisk.Equal(dec("45")) {
		t.Error("worst risk equals the largest realised loss; risk is being read from the outcome")
	}
}

// TestRiskShareIsMeasuredAgainstTheBalanceAtTheTime.
//
// Dividing every trade's risk by the run's *starting* balance is right only
// while the account does not compound. A real 5m run over three months grew
// its equity by a factor of 740, and reported a 1%-risk rule as risking "748%
// of balance" — a number that is alarming, meaningless, and not what the
// sizing rule did.
//
// Each trade's share is taken against the equity it was opened on instead.
func TestRiskShareIsMeasuredAgainstTheBalanceAtTheTime(t *testing.T) {
	result := riskedResult()

	// Each trade risks exactly 1% of what the account held when it opened, on
	// an account that grows tenfold between the first trade and the last.
	for i, equity := range []string{"100", "1000", "10000"} {
		result.Trades[i].EquityAtEntry = dec(equity)
		result.Trades[i].EntryPrice = dec("100")
		result.Trades[i].StopPrice = dec("99")
		// Risk is 1 x size, so size is the risk: 1% of the balance.
		result.Trades[i].Size = dec(equity).Div(dec("100"))
	}

	stats := report.Compute(result)

	if math.Abs(stats.AverageRiskPct-0.01) > 1e-12 {
		t.Errorf("average risk is %v of balance, want 0.01 — a 1%% rule reported as something else",
			stats.AverageRiskPct)
	}
	if math.Abs(stats.WorstRiskPct-0.01) > 1e-12 {
		t.Errorf("worst risk is %v of balance, want 0.01", stats.WorstRiskPct)
	}

	// The absolute figures still differ, and should: the last trade risked 100
	// where the first risked 1. Both facts are true and the report shows each.
	if !stats.WorstRisk.Equal(dec("100")) {
		t.Errorf("worst risk is %s in currency, want 100", stats.WorstRisk)
	}

	// The failure this replaces: 100 of risk against a 100 starting balance
	// would have reported 100%.
	if math.Abs(stats.WorstRiskPct-1.0) < 1e-9 {
		t.Error("worst risk share was computed against the starting balance")
	}
}

// TestTradesWithoutAStopDoNotDiluteTheRiskAverage.
//
// Averaging a stop-less trade in as zero risk would understate the risk of the
// trades that did carry one, which is the wrong direction to be wrong in.
func TestTradesWithoutAStopDoNotDiluteTheRiskAverage(t *testing.T) {
	result := riskedResult()
	stopless := result.Trades[0]
	stopless.StopPrice = decimal.Zero
	result.Trades = append(result.Trades, stopless)

	stats := report.Compute(result)

	wantAverage := dec("14.5").Div(dec("3"))
	if !stats.AverageRisk.Equal(wantAverage) {
		t.Errorf("average risk is %s with a stop-less trade present, want %s from the three that had stops",
			stats.AverageRisk, wantAverage)
	}
}

// TestWorstDrawdownIsReportedInCurrencyAsWellAsPercent is the line that decides
// whether an account survives.
//
// A 72-trade losing streak at 0.5 USD a trade is 36 USD, over a third of a 100
// USD balance. The report has to make that arithmetic visible rather than
// leaving it to be discovered live.
func TestWorstDrawdownIsReportedInCurrencyAsWellAsPercent(t *testing.T) {
	result := riskedResult()
	stats := report.Compute(result)

	if !stats.MaxDrawdown.Absolute.Equal(dec("-45")) {
		t.Errorf("max drawdown is %s in currency, want -45", stats.MaxDrawdown.Absolute)
	}
	// 45 of a 110 peak.
	wantPercent, _ := dec("-45").Div(dec("110")).Float64()
	if math.Abs(stats.MaxDrawdown.Percent-wantPercent) > 1e-12 {
		t.Errorf("max drawdown is %v, want %v", stats.MaxDrawdown.Percent, wantPercent)
	}

	var buf bytes.Buffer
	if err := report.WriteSummary(&buf, result, stats); err != nil {
		t.Fatalf("WriteSummary() returned error: %v", err)
	}
	text := buf.String()

	// Both, on the same line. Either alone is the half of the story that
	// misleads on a small balance.
	for _, want := range []string{"-45.00 USD", "max drawdown"} {
		if !strings.Contains(text, want) {
			t.Errorf("the summary does not show %q:\n%s", want, text)
		}
	}
	if !strings.Contains(text, "risk per trade") {
		t.Errorf("the summary does not state risk per trade:\n%s", text)
	}
}

// TestCostsAreReportedAsAShareOfGrossProfit. The absolute figure alone does not
// say whether costs were the whole result.
func TestCostsAreReportedAsAShareOfGrossProfit(t *testing.T) {
	result := riskedResult()
	stats := report.Compute(result)

	// Gross: 10.25 - 44.75 + 5.25 = -29.25. Negative, so there is no gross
	// profit for the costs to be a share of.
	if !math.IsNaN(stats.CostShareOfGross) {
		t.Errorf("cost share is %v on a run with no gross profit, want NaN", stats.CostShareOfGross)
	}

	// A profitable run does have one: 0.75 of costs against 35.75 gross
	// (10.25 + 20.25 + 5.25).
	result.Trades[1].GrossPnL = dec("20.25")
	result.Trades[1].NetPnL = dec("20")
	stats = report.Compute(result)

	wantShare, _ := dec("0.75").Div(dec("35.75")).Float64()
	if math.Abs(stats.CostShareOfGross-wantShare) > 1e-12 {
		t.Errorf("cost share is %v, want %v", stats.CostShareOfGross, wantShare)
	}

	var buf bytes.Buffer
	if err := report.WriteSummary(&buf, result, stats); err != nil {
		t.Fatalf("WriteSummary() returned error: %v", err)
	}
	if !strings.Contains(buf.String(), "of gross profit") {
		t.Errorf("the summary does not relate costs to gross profit:\n%s", buf.String())
	}
}

// TestTheSpreadHeaderStatesTheVenueAndTheCaveat.
//
// Every candle in this system is Binance BTCUSDT; the intended venue is IUX
// BTCUSD CFD. Those are different instruments. The cost model can be made to
// match — the price series cannot, without collecting from the venue itself —
// so the caveat is printed on every spread run rather than relied on to be
// remembered.
func TestTheSpreadHeaderStatesTheVenueAndTheCaveat(t *testing.T) {
	result := riskedResult()

	var buf bytes.Buffer
	if err := report.WriteSummary(&buf, result, report.Compute(result)); err != nil {
		t.Fatalf("WriteSummary() returned error: %v", err)
	}
	text := buf.String()

	for _, want := range []string{
		"cost model",
		"spread (25.00 USD typical, 2500 points, half each side)",
		"commission",
		"per lot per side",
		"min 0.01, step 0.01",
		"prices are Binance BTCUSDT; costs model IUX BTCUSD CFD",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the spread header does not state %q:\n%s", want, text)
		}
	}

	// The percentage model's lines have no business appearing in it: a reader
	// seeing a taker rate on a spread run would have no way to tell which one
	// priced the trades. Scoped to the header, because the fill counts further
	// down legitimately say "maker" and "taker" about how orders filled.
	header, _, _ := strings.Cut(text, "\nPERFORMANCE")
	if strings.Contains(header, "taker") || strings.Contains(header, "fee") {
		t.Errorf("the spread header quotes a percentage fee, which did not price anything:\n%s", header)
	}
}

// TestSkippedForSizeIsReportedWhenItHappens. With a 100 USD account and a 0.01
// lot floor the count can be most of the signals, and a run whose statistics
// describe a fraction of the strategy's intent has to say so.
func TestSkippedForSizeIsReportedWhenItHappens(t *testing.T) {
	result := riskedResult()
	result.EntriesBelowMinLot = 412

	var buf bytes.Buffer
	if err := report.WriteSummary(&buf, result, report.Compute(result)); err != nil {
		t.Fatalf("WriteSummary() returned error: %v", err)
	}
	text := buf.String()

	if !strings.Contains(text, "412") || !strings.Contains(text, "skipped for size") {
		t.Errorf("the summary does not report entries skipped for size:\n%s", text)
	}

	// And stays silent when there were none, rather than printing a zero that
	// reads as a warning.
	result.EntriesBelowMinLot = 0
	buf.Reset()
	if err := report.WriteSummary(&buf, result, report.Compute(result)); err != nil {
		t.Fatalf("WriteSummary() returned error: %v", err)
	}
	if strings.Contains(buf.String(), "skipped for size") {
		t.Errorf("the summary reports a skipped-for-size line on a run with none:\n%s", buf.String())
	}
}
