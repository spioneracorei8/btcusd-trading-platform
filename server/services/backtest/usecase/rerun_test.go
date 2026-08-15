package usecase_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/backtest"
	_backtest_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/backtest/usecase"
	_candle_repo "github.com/spioneracorei8/btcusd-trading-platform/server/services/candle/repository"
	_candle_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/candle/usecase"
	_datagap_repo "github.com/spioneracorei8/btcusd-trading-platform/server/services/datagap/repository"
	_datagap_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/datagap/usecase"
	_indicator_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/indicator/usecase"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/trend"
	_trend_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/trend/usecase"
	"github.com/spioneracorei8/btcusd-trading-platform/server/testhelper"
)

// This file is about the defect behind Fix 2: --cost-sweep reused one trend
// filter across its passes, so the second pass began with an aligner already
// parked at the end of the range and died on the forward-only invariant.
//
// The CLI builds every run through prepareRun now. What these tests pin is the
// engine-level property that makes that necessary and sufficient: a filter is
// single-use, and two independently built runs over the same range agree
// exactly.

// finalEquity is the account value at the last evaluated bar.
func finalEquity(result backtest.Result) decimal.Decimal {
	if len(result.Equity) == 0 {
		return result.Params.InitialEquity
	}
	return result.Equity[len(result.Equity)-1].Equity
}

// sweepRange is short enough to run repeatedly and long enough for the 1h
// contributor to close several times.
func sweepWindow() (from, to time.Time) {
	from = time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)
	to = time.Date(2024, 3, 3, 0, 0, 0, 0, time.UTC)
	return from, to
}

// TestASweepPassCannotShareTheFilterWithTheOneBeforeIt reproduces the reported
// failure, so the guarantee is a test rather than a habit.
//
// The aligner only moves forward, which is correct: rewinding it would mean a
// higher-timeframe cursor could be asked about an instant it has already
// passed, and the answer would be a bar that closed after the decision. The
// defect was reusing the instance, not the invariant.
func TestASweepPassCannotShareTheFilterWithTheOneBeforeIt(t *testing.T) {
	pool := testhelper.NewTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	from, to := sweepWindow()
	seedYear(ctx, t, pool, from, to)
	seedHigherTimeframes(ctx, t, pool, from, to)

	candles := _candle_us.NewCandleUsecaseImpl(_candle_repo.NewCandleRepoImpl(pool))
	engine := _backtest_us.NewBacktestUsecaseImpl(
		silentLogger(), candles,
		_datagap_us.NewDataGapUsecaseImpl(_datagap_repo.NewDataGapRepoImpl(pool)),
		_indicator_us.DefaultSetConfig(),
	)

	config, err := trend.DefaultConfig().ForBase(constants.Timeframe1m)
	if err != nil {
		t.Fatalf("ForBase() returned error: %v", err)
	}
	filter, err := _trend_us.NewFilterImpl(config)
	if err != nil {
		t.Fatalf("NewFilterImpl() returned error: %v", err)
	}
	aligner, err := _trend_us.NewAlignerImpl(_trend_us.AlignerConfig{
		Symbol: benchSymbol, MarketType: constants.MarketTypeSpot,
		Base: constants.Timeframe1m, Higher: config.Timeframes(),
		From: from, To: to, Indicators: _indicator_us.DefaultSetConfig(),
	}, candles)
	if err != nil {
		t.Fatalf("NewAlignerImpl() returned error: %v", err)
	}

	params := backtest.RunParams{
		Symbol: benchSymbol, MarketType: constants.MarketTypeSpot,
		Timeframe: constants.Timeframe1m, From: from, To: to,
		InitialEquity: decimal.NewFromInt(10000),
		Costs:         testCosts(),
		Sizing:        backtest.AllInSizing(),
		GapPolicy:     backtest.GapIgnore,
		Strategy:      &alternating{everyN: 60},
		TrendFilter:   filter, TrendAligner: aligner, TrendConfig: config,
	}

	if _, err := engine.Run(ctx, params); err != nil {
		t.Fatalf("the first run failed: %v", err)
	}

	// The second pass, built the way the broken sweep built it: a fresh
	// strategy and the filter it was handed.
	params.Strategy = &alternating{everyN: 60}
	_, err = engine.Run(ctx, params)
	if err == nil {
		t.Fatal("a reused aligner replayed the range silently; " +
			"the forward-only invariant is what stops a cursor being rewound past a close")
	}
	if !strings.Contains(err.Error(), "only moves forward") {
		t.Errorf("failed with %v, want the aligner's forward-only refusal", err)
	}
}

// TestTwoIndependentlyBuiltRunsAgreeExactly is the other half: the 1.0x sweep
// pass has to reproduce the headline run it anchors.
//
// If it does not, state is leaking between passes and every row of the cost
// sensitivity table is measuring the leak as well as the cost.
func TestTwoIndependentlyBuiltRunsAgreeExactly(t *testing.T) {
	pool := testhelper.NewTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	from, to := sweepWindow()
	seedYear(ctx, t, pool, from, to)
	seedHigherTimeframes(ctx, t, pool, from, to)

	candles := _candle_us.NewCandleUsecaseImpl(_candle_repo.NewCandleRepoImpl(pool))
	engine := _backtest_us.NewBacktestUsecaseImpl(
		silentLogger(), candles,
		_datagap_us.NewDataGapUsecaseImpl(_datagap_repo.NewDataGapRepoImpl(pool)),
		_indicator_us.DefaultSetConfig(),
	)

	// build is what prepareRun does: nothing stateful survives from last time.
	build := func() backtest.RunParams {
		config, err := trend.DefaultConfig().ForBase(constants.Timeframe1m)
		if err != nil {
			t.Fatalf("ForBase() returned error: %v", err)
		}
		filter, err := _trend_us.NewFilterImpl(config)
		if err != nil {
			t.Fatalf("NewFilterImpl() returned error: %v", err)
		}
		aligner, err := _trend_us.NewAlignerImpl(_trend_us.AlignerConfig{
			Symbol: benchSymbol, MarketType: constants.MarketTypeSpot,
			Base: constants.Timeframe1m, Higher: config.Timeframes(),
			From: from, To: to, Indicators: _indicator_us.DefaultSetConfig(),
		}, candles)
		if err != nil {
			t.Fatalf("NewAlignerImpl() returned error: %v", err)
		}
		return backtest.RunParams{
			Symbol: benchSymbol, MarketType: constants.MarketTypeSpot,
			Timeframe: constants.Timeframe1m, From: from, To: to,
			InitialEquity: decimal.NewFromInt(10000),
			Costs:         testCosts(),
			Sizing:        backtest.AllInSizing(),
			GapPolicy:     backtest.GapIgnore,
			Strategy:      &alternating{everyN: 60},
			TrendFilter:   filter, TrendAligner: aligner, TrendConfig: config,
		}
	}

	first, err := engine.Run(ctx, build())
	if err != nil {
		t.Fatalf("the first run failed: %v", err)
	}
	second, err := engine.Run(ctx, build())
	if err != nil {
		t.Fatalf("the second run failed: %v", err)
	}

	if len(first.Trades) != len(second.Trades) {
		t.Fatalf("trade counts differ: %d then %d — state leaked between the runs",
			len(first.Trades), len(second.Trades))
	}
	if !finalEquity(first).Equal(finalEquity(second)) {
		t.Errorf("final equity differs: %s then %s", finalEquity(first), finalEquity(second))
	}
	if first.BarsVetoed != second.BarsVetoed {
		t.Errorf("vetoed bar counts differ: %d then %d — the filter did not start fresh",
			first.BarsVetoed, second.BarsVetoed)
	}
	if first.BarsFilterNotReady != second.BarsFilterNotReady {
		t.Errorf("not-ready counts differ: %d then %d — the aligner did not start fresh",
			first.BarsFilterNotReady, second.BarsFilterNotReady)
	}
	for i := range first.Trades {
		if !first.Trades[i].NetPnL.Equal(second.Trades[i].NetPnL) {
			t.Fatalf("trade %d differs: %s then %s",
				i, first.Trades[i].NetPnL, second.Trades[i].NetPnL)
		}
	}
}

// TestTheOrderOfTheComparisonPassesDoesNotMatter.
//
// --compare used to be correct by accident: the unfiltered pass ran on params
// the caller had not yet attached a filter to, so moving the compare check a
// few lines down would have made "unfiltered" a second filtered run. Both
// passes are built explicitly now, and neither may depend on running first.
func TestTheOrderOfTheComparisonPassesDoesNotMatter(t *testing.T) {
	pool := testhelper.NewTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	from, to := sweepWindow()
	seedYear(ctx, t, pool, from, to)
	seedHigherTimeframes(ctx, t, pool, from, to)

	candles := _candle_us.NewCandleUsecaseImpl(_candle_repo.NewCandleRepoImpl(pool))
	engine := _backtest_us.NewBacktestUsecaseImpl(
		silentLogger(), candles,
		_datagap_us.NewDataGapUsecaseImpl(_datagap_repo.NewDataGapRepoImpl(pool)),
		_indicator_us.DefaultSetConfig(),
	)

	base := backtest.RunParams{
		Symbol: benchSymbol, MarketType: constants.MarketTypeSpot,
		Timeframe: constants.Timeframe1m, From: from, To: to,
		InitialEquity: decimal.NewFromInt(10000),
		Costs:         testCosts(),
		Sizing:        backtest.AllInSizing(),
		GapPolicy:     backtest.GapIgnore,
	}

	withFilter := func() backtest.RunParams {
		config, err := trend.DefaultConfig().ForBase(constants.Timeframe1m)
		if err != nil {
			t.Fatalf("ForBase() returned error: %v", err)
		}
		filter, err := _trend_us.NewFilterImpl(config)
		if err != nil {
			t.Fatalf("NewFilterImpl() returned error: %v", err)
		}
		aligner, err := _trend_us.NewAlignerImpl(_trend_us.AlignerConfig{
			Symbol: benchSymbol, MarketType: constants.MarketTypeSpot,
			Base: constants.Timeframe1m, Higher: config.Timeframes(),
			From: from, To: to, Indicators: _indicator_us.DefaultSetConfig(),
		}, candles)
		if err != nil {
			t.Fatalf("NewAlignerImpl() returned error: %v", err)
		}
		params := base
		params.Strategy = &alternating{everyN: 60}
		params.TrendFilter, params.TrendAligner, params.TrendConfig = filter, aligner, config
		return params
	}
	withoutFilter := func() backtest.RunParams {
		params := base
		params.Strategy = &alternating{everyN: 60}
		return params
	}

	// Filtered first, then unfiltered.
	filteredA, err := engine.Run(ctx, withFilter())
	if err != nil {
		t.Fatalf("filtered-first run failed: %v", err)
	}
	unfilteredA, err := engine.Run(ctx, withoutFilter())
	if err != nil {
		t.Fatalf("unfiltered-second run failed: %v", err)
	}

	// Unfiltered first, then filtered.
	unfilteredB, err := engine.Run(ctx, withoutFilter())
	if err != nil {
		t.Fatalf("unfiltered-first run failed: %v", err)
	}
	filteredB, err := engine.Run(ctx, withFilter())
	if err != nil {
		t.Fatalf("filtered-second run failed: %v", err)
	}

	if !finalEquity(filteredA).Equal(finalEquity(filteredB)) {
		t.Errorf("the filtered result depends on when it ran: %s vs %s",
			finalEquity(filteredA), finalEquity(filteredB))
	}
	if !finalEquity(unfilteredA).Equal(finalEquity(unfilteredB)) {
		t.Errorf("the unfiltered result depends on when it ran: %s vs %s",
			finalEquity(unfilteredA), finalEquity(unfilteredB))
	}
	// And the unfiltered pass must really be unfiltered.
	if unfilteredA.TrendFilterName != "" || unfilteredA.BarsVetoed != 0 {
		t.Errorf("the unfiltered run reports filter %q and %d vetoed bars",
			unfilteredA.TrendFilterName, unfilteredA.BarsVetoed)
	}
}
