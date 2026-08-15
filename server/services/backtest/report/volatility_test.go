package report_test

import (
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/backtest"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/backtest/report"
)

// tradeAt builds a trade with a stated entry ATR and a stated outcome, so the
// two can be varied independently — which is the whole point of the fix.
func tradeAt(index int, entryATR float64, net string) backtest.Trade {
	at := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(index) * time.Hour)
	entry := decimal.NewFromInt(50000)
	netPnL := decimal.RequireFromString(net)

	return backtest.Trade{
		Direction:  constants.DirectionLong,
		EntryTime:  at,
		ExitTime:   at.Add(30 * time.Minute),
		EntryPrice: entry,
		ExitPrice:  entry.Add(netPnL),
		Size:       decimal.NewFromInt(1),
		GrossPnL:   netPnL,
		NetPnL:     netPnL,
		EntryATR:   entryATR,
	}
}

func analysisOf(trades []backtest.Trade) report.Analysis {
	result := backtest.Result{Trades: trades}
	return report.Analyse(result, report.Compute(result))
}

// TestTheSplitUsesTheEntryBarNotTheOutcome is the defect.
//
// The old split bucketed on the entry-to-exit move — the trade's own result —
// so every trade that barely moved lost to costs and landed in "low" by
// construction. The report then announced a 0.00% win rate for that bucket
// across thousands of trades, which is arithmetic wearing the costume of a
// finding.
//
// Here every trade in the *high* ATR bucket loses and every trade in the low
// bucket wins. If the split still read the outcome, the losers would sort
// themselves into the low bucket and this would come out backwards.
func TestTheSplitUsesTheEntryBarNotTheOutcome(t *testing.T) {
	var trades []backtest.Trade
	// Low ATR, winning.
	for i := range 6 {
		trades = append(trades, tradeAt(i, 100, "250"))
	}
	// High ATR, losing — a large adverse move, which the old split would have
	// filed under "high volatility" for the wrong reason entirely.
	for i := range 6 {
		trades = append(trades, tradeAt(100+i, 900, "-400"))
	}

	buckets := analysisOf(trades).ByVolatility
	if len(buckets) != 3 {
		t.Fatalf("got %d buckets, want 3 terciles", len(buckets))
	}

	low, high := buckets[0], buckets[len(buckets)-1]
	if !low.NetPnL.IsPositive() {
		t.Errorf("the low-ATR bucket is %s; the winners were entered at low ATR", low.NetPnL)
	}
	if !high.NetPnL.IsNegative() {
		t.Errorf("the high-ATR bucket is %s; the losers were entered at high ATR", high.NetPnL)
	}
	// WinRate is a fraction; the writer renders it as a percentage.
	if low.WinRate != 1 {
		t.Errorf("low-ATR win rate is %.2f, want 1 (every low-ATR trade won)", low.WinRate)
	}
	if high.WinRate != 0 {
		t.Errorf("high-ATR win rate is %.2f, want 0 (every high-ATR trade lost)", high.WinRate)
	}
}

// TestNeitherBucketIsAnArtefactOfTheSplit.
//
// The tell for the old defect was a 0.00% win rate across thousands of trades.
// With winners and losers spread evenly across ATR levels, no bucket may come
// out all-losing: if one does, the split is sorting by outcome again.
func TestNeitherBucketIsAnArtefactOfTheSplit(t *testing.T) {
	var trades []backtest.Trade
	// Alternating win/loss at steadily rising ATR, so outcome and ATR are
	// uncorrelated by construction.
	for i := range 30 {
		net := "150"
		if i%2 == 1 {
			net = "-150"
		}
		trades = append(trades, tradeAt(i, 100+float64(i)*20, net))
	}

	for _, bucket := range analysisOf(trades).ByVolatility {
		if bucket.Trades == 0 {
			t.Errorf("%s is empty", bucket.Label)
			continue
		}
		// WinRate is a fraction: 0 is all-losing, 1 is all-winning. The tell
		// for the original defect was exactly the former.
		if bucket.WinRate == 0 || bucket.WinRate == 1 {
			t.Errorf("%s has a %.2f win rate over %d trades; with outcomes spread "+
				"evenly across ATR that can only come from the split reading the outcome",
				bucket.Label, bucket.WinRate, bucket.Trades)
		}
	}
}

// TestBucketsAreLabelledWithTheirATRRange. "High" is meaningless without
// knowing whether it means 0.3% or 3% — a reader cannot tell whether the
// strategy is regime-dependent or whether the run simply never saw a quiet
// market.
func TestBucketsAreLabelledWithTheirATRRange(t *testing.T) {
	var trades []backtest.Trade
	for i := range 30 {
		// 100 → 680 against a 50000 entry, so 0.20% → 1.36%.
		trades = append(trades, tradeAt(i, 100+float64(i)*20, "10"))
	}

	buckets := analysisOf(trades).ByVolatility
	if len(buckets) != 3 {
		t.Fatalf("got %d buckets, want 3", len(buckets))
	}

	for _, bucket := range buckets {
		if !strings.Contains(bucket.Label, "ATR") || !strings.Contains(bucket.Label, "%") {
			t.Errorf("%q does not state its ATR range", bucket.Label)
		}
	}
	// The lowest bucket must start at the lowest ATR actually seen: 100/50000.
	if !strings.Contains(buckets[0].Label, "0.20") {
		t.Errorf("the low bucket is labelled %q, want it to start at 0.20%%", buckets[0].Label)
	}
	if !strings.Contains(buckets[2].Label, "1.36") {
		t.Errorf("the high bucket is labelled %q, want it to end at 1.36%%", buckets[2].Label)
	}
}

// TestATRIsNormalisedByPrice. BTC ranged from roughly $16k to $100k over the
// development period. An absolute ATR split would put most of 2023 in one
// bucket and most of 2024 in another and call the result a volatility regime,
// when it is a calendar.
func TestATRIsNormalisedByPrice(t *testing.T) {
	build := func(price int64, atr float64) backtest.Trade {
		trade := tradeAt(0, atr, "10")
		trade.EntryPrice = decimal.NewFromInt(price)
		trade.ExitPrice = trade.EntryPrice.Add(decimal.NewFromInt(10))
		return trade
	}

	// Same 1% ATR at both ends of the price range, plus a genuinely quiet
	// group at 0.2% and a violent one at 3%.
	var trades []backtest.Trade
	for i := range 4 {
		trades = append(trades, build(16000, 160))  // 1.0%
		trades = append(trades, build(100000, 999)) // ~1.0%
		trades = append(trades, build(60000, 120))  // 0.2%
		trades = append(trades, build(60000, 1800)) // 3.0%
		_ = i
	}

	buckets := analysisOf(trades).ByVolatility
	if len(buckets) != 3 {
		t.Fatalf("got %d buckets, want 3", len(buckets))
	}
	// The quiet trades are all at $60k and the violent ones too, so a split
	// that worked in absolute terms would not separate them from the $100k
	// trades. Normalised, the extremes must sit in different buckets.
	if strings.Contains(buckets[0].Label, "3.0") {
		t.Errorf("the low bucket reaches 3%% ATR: %q", buckets[0].Label)
	}
	if strings.Contains(buckets[2].Label, "0.20") {
		t.Errorf("the high bucket starts at 0.20%% ATR: %q", buckets[2].Label)
	}
}

// TestATooSmallRunIsNotSplit. A bucket holding one trade reports that trade's
// outcome as though it were a regime.
func TestATooSmallRunIsNotSplit(t *testing.T) {
	var trades []backtest.Trade
	for i := range 4 {
		trades = append(trades, tradeAt(i, 100+float64(i)*10, "10"))
	}
	if buckets := analysisOf(trades).ByVolatility; buckets != nil {
		t.Errorf("4 trades were split into %d buckets", len(buckets))
	}
}

// TestTradesWithoutAnEntryATRAreExcluded rather than defaulted to zero, which
// would pile every one of them into the low bucket and recreate the original
// defect from the other direction.
func TestTradesWithoutAnEntryATRAreExcluded(t *testing.T) {
	var trades []backtest.Trade
	for i := range 12 {
		trades = append(trades, tradeAt(i, 0, "-100")) // no ATR recorded
	}
	if buckets := analysisOf(trades).ByVolatility; buckets != nil {
		t.Errorf("trades with no entry ATR were bucketed anyway: %+v", buckets)
	}
}

// TestTheSplitIsDeterministic. Two runs over the same trades must bucket them
// identically, including when several share an ATR — ADR 0012.
func TestTheSplitIsDeterministic(t *testing.T) {
	var trades []backtest.Trade
	for i := range 30 {
		// Deliberately repeated ATR values, so ties have to be broken stably.
		trades = append(trades, tradeAt(i, 100+float64(i%3)*50, "10"))
	}

	first := analysisOf(trades).ByVolatility
	for run := range 10 {
		next := analysisOf(trades).ByVolatility
		if len(first) != len(next) {
			t.Fatalf("run %d produced %d buckets, want %d", run, len(next), len(first))
		}
		for i := range first {
			if first[i].Label != next[i].Label || first[i].Trades != next[i].Trades ||
				!first[i].NetPnL.Equal(next[i].NetPnL) {
				t.Fatalf("run %d differs at bucket %d:\n %+v\n %+v", run, i, first[i], next[i])
			}
		}
	}
}

// TestTheYearSplitStillUsesEntryTime is the audit the fix asked for: any
// breakdown that partitions trades by something only knowable after the exit
// is measuring itself. The year is known at entry, so it is fine — this pins
// that it stays that way.
func TestTheYearSplitStillUsesEntryTime(t *testing.T) {
	// A trade that opens on new year's eve and closes in January. If the split
	// used the exit it would land in 2025.
	newYear := backtest.Trade{
		Direction:  constants.DirectionLong,
		EntryTime:  time.Date(2024, 12, 31, 23, 30, 0, 0, time.UTC),
		ExitTime:   time.Date(2025, 1, 1, 0, 30, 0, 0, time.UTC),
		EntryPrice: decimal.NewFromInt(50000),
		ExitPrice:  decimal.NewFromInt(50100),
		NetPnL:     decimal.NewFromInt(100),
		EntryATR:   100,
	}

	buckets := analysisOf([]backtest.Trade{newYear}).ByYear
	if len(buckets) != 1 {
		t.Fatalf("got %d year buckets, want 1", len(buckets))
	}
	if !strings.Contains(buckets[0].Label, "2024") {
		t.Errorf("the trade landed in %q; it was entered in 2024", buckets[0].Label)
	}
}
