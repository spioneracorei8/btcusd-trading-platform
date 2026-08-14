package usecase_test

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/backtest"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/backtest/report"
	_backtest_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/backtest/usecase"
	_candle_repo "github.com/spioneracorei8/btcusd-trading-platform/server/services/candle/repository"
	_candle_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/candle/usecase"
	_datagap_repo "github.com/spioneracorei8/btcusd-trading-platform/server/services/datagap/repository"
	_datagap_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/datagap/usecase"
	"github.com/spioneracorei8/btcusd-trading-platform/server/testhelper"
)

// TestTwoIdenticalRunsProduceByteIdenticalJSON is the phase-04 determinism
// requirement, checked over the whole pipeline rather than the renderer alone.
//
// Rendering the same Result twice is a weaker test: it cannot catch
// non-determinism in the engine itself — a map iterated while collecting
// trades, a slice ordered by something unstable, a page boundary that lands
// differently. This runs the engine twice against a real database, through the
// same keyset cursor a real run uses, and diffs the bytes.
//
// Byte-identity is what makes "did my change alter the result" answerable with
// diff instead of with judgement.
func TestTwoIdenticalRunsProduceByteIdenticalJSON(t *testing.T) {
	pool := testhelper.NewTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	from := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	// A few days: long enough to cross several cursor pages and a daily VWAP
	// reset, short enough to run twice quickly.
	to := time.Date(2024, 1, 5, 0, 0, 0, 0, time.UTC)
	seedYear(ctx, t, pool, from, time.Date(2024, 12, 31, 23, 59, 0, 0, time.UTC))

	render := func() []byte {
		t.Helper()

		engine := _backtest_us.NewBacktestUsecaseImpl(
			silentLogger(),
			_candle_us.NewCandleUsecaseImpl(_candle_repo.NewCandleRepoImpl(pool)),
			_datagap_us.NewDataGapUsecaseImpl(_datagap_repo.NewDataGapRepoImpl(pool)),
			testSetConfig(),
		)

		result, err := engine.Run(ctx, backtest.RunParams{
			Symbol:        benchSymbol,
			MarketType:    constants.MarketTypeSpot,
			Timeframe:     constants.Timeframe1m,
			From:          from,
			To:            to,
			InitialEquity: decimal.NewFromInt(10000),
			Costs:         testCosts(),
			Sizing:        backtest.AllInSizing(),
			GapPolicy:     backtest.GapIgnore,
			Strategy:      &alternating{everyN: 37},
		})
		if err != nil {
			t.Fatalf("Run() returned error: %v", err)
		}

		var buf bytes.Buffer
		if err := report.WriteJSON(&buf, report.BuildDocument(result, report.Compute(result))); err != nil {
			t.Fatalf("WriteJSON() returned error: %v", err)
		}
		return buf.Bytes()
	}

	first := render()
	second := render()

	if !bytes.Equal(first, second) {
		t.Fatalf("two identical runs produced different JSON\n\nfirst %d bytes, second %d bytes\n%s",
			len(first), len(second), firstDifference(first, second))
	}

	// A run that produced nothing would pass the comparison trivially.
	if !strings.Contains(string(first), `"exit_reason"`) {
		t.Error("the run produced no trades, so determinism was not actually exercised")
	}
}

// firstDifference locates where two renders diverge, so a failure says which
// field moved rather than only that something did.
func firstDifference(a, b []byte) string {
	limit := min(len(a), len(b))

	for i := range limit {
		if a[i] == b[i] {
			continue
		}
		start := max(i-120, 0)
		return "first difference at byte " + itoa(i) + ":\n" +
			"  A: ..." + string(a[start:min(i+120, len(a))]) + "\n" +
			"  B: ..." + string(b[start:min(i+120, len(b))])
	}
	return "one render is a prefix of the other"
}

// itoa avoids pulling strconv in for one call in a failure path.
func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var digits []byte
	for v > 0 {
		digits = append([]byte{byte('0' + v%10)}, digits...)
		v /= 10
	}
	return string(digits)
}

// TestFullPipelineRendersASummary walks the whole path a CLI invocation takes
// — engine, statistics, both renderers — against real stored candles.
//
// The unit tests each cover one stage with fakes. This is the one that would
// notice the stages no longer fitting together.
func TestFullPipelineRendersASummary(t *testing.T) {
	pool := testhelper.NewTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	from := time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2024, 2, 3, 0, 0, 0, 0, time.UTC)
	seedYear(ctx, t, pool, time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2024, 12, 31, 23, 59, 0, 0, time.UTC))

	engine := _backtest_us.NewBacktestUsecaseImpl(
		silentLogger(),
		_candle_us.NewCandleUsecaseImpl(_candle_repo.NewCandleRepoImpl(pool)),
		_datagap_us.NewDataGapUsecaseImpl(_datagap_repo.NewDataGapRepoImpl(pool)),
		testSetConfig(),
	)

	result, err := engine.Run(ctx, backtest.RunParams{
		Symbol:        benchSymbol,
		MarketType:    constants.MarketTypeSpot,
		Timeframe:     constants.Timeframe1m,
		From:          from,
		To:            to,
		InitialEquity: decimal.NewFromInt(10000),
		Costs:         testCosts(),
		Sizing:        backtest.AllInSizing(),
		GapPolicy:     backtest.GapIgnore,
		Strategy:      &alternating{everyN: 60},
	})
	if err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}

	stats := report.Compute(result)

	var summary bytes.Buffer
	if err := report.WriteSummary(&summary, result, stats); err != nil {
		t.Fatalf("WriteSummary() returned error: %v", err)
	}
	text := summary.String()

	// The header is never suppressible, so every one of these must be there.
	for _, want := range []string{
		"net return after costs",
		"total costs paid",
		"bars evaluated",
		"fee applied",
		"slippage applied",
		"gap policy",
		"stop-before-target bars",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the summary omits %q:\n%s", want, text)
		}
	}

	// This run is over complete data, so it must not claim otherwise.
	if strings.Contains(text, report.DataIncompleteStamp) {
		t.Error("a run over complete data was stamped incomplete")
	}

	// The costs the report claims must be the costs the trades recorded.
	totalCosts := decimal.Zero
	for _, trade := range result.Trades {
		totalCosts = totalCosts.Add(trade.Costs)
	}
	if !stats.TotalCosts.Equal(totalCosts) {
		t.Errorf("the report totals costs at %s but the trades add to %s",
			stats.TotalCosts, totalCosts)
	}
}
