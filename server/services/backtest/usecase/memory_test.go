package usecase_test

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
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

// benchSymbol is a synthetic series, kept apart from any real one so the
// benchmark never depends on what has actually been collected.
const benchSymbol = "RSSBENCH"

// rssLimitMB is the phase-04 budget: a full year of 1m data must replay
// without exceeding it.
//
// The number is not arbitrary. The deployment target is a 2 vCPU / 4 GB VPS
// that also runs PostgreSQL, the collector and the api; a backtest that needed
// a gigabyte would be a backtest that could only be run by stopping the system
// it is meant to measure.
const rssLimitMB = 500

// TestFullYearOfOneMinuteBarsStaysUnderTheMemoryBudget replays 2024 end to end
// against a real database.
//
// It is the test that would catch the engine quietly loading the series into a
// slice: a keyset cursor holds one page, and the difference between that and
// 527,040 resident candles is the difference between tens of megabytes and
// most of the VPS.
func TestFullYearOfOneMinuteBarsStaysUnderTheMemoryBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("replaying a year of 1m candles is too slow for -short")
	}

	pool := testhelper.NewTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	from := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2024, 12, 31, 23, 59, 0, 0, time.UTC)
	seedYear(ctx, t, pool, from, to)

	engine := _backtest_us.NewBacktestUsecaseImpl(
		silentLogger(),
		_candle_us.NewCandleUsecaseImpl(_candle_repo.NewCandleRepoImpl(pool)),
		_datagap_us.NewDataGapUsecaseImpl(_datagap_repo.NewDataGapRepoImpl(pool)),
		// Real periods, not the shortened test ones: EMA(200) is what a run
		// on this machine would actually carry.
		_indicator_us.DefaultSetConfig(),
	)

	before := peakRSSMB(t)

	// The alternating strategy trades throughout, so the trade list and the
	// equity curve both grow — a flat strategy would understate the footprint.
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
		Strategy:      &alternating{everyN: 500},
	})
	if err != nil {
		t.Fatalf("Run() over a year of 1m candles returned error: %v", err)
	}

	after := peakRSSMB(t)

	const barsInLeapYear = 366 * 24 * 60
	if result.BarsEvaluated < barsInLeapYear-2000 {
		t.Fatalf("evaluated only %d bars of an expected ~%d; the run did not cover the year",
			result.BarsEvaluated, barsInLeapYear)
	}
	if int64(len(result.Equity)) != result.BarsEvaluated {
		t.Errorf("curve has %d points for %d evaluated bars", len(result.Equity), result.BarsEvaluated)
	}

	t.Logf("replayed %d bars, %d trades; peak RSS %d MB (was %d MB before the run)",
		result.BarsEvaluated, len(result.Trades), after, before)

	if after > rssLimitMB {
		t.Errorf("peak RSS reached %d MB, over the %d MB budget", after, rssLimitMB)
	}
}

// seedYear fills the synthetic series if it is not already stored.
//
// One statement rather than a loop: generating half a million rows through the
// application would take longer than the test it is setting up for.
func seedYear(ctx context.Context, t *testing.T, pool *pgxpool.Pool, from, to time.Time) {
	t.Helper()

	var stored int64
	err := pool.QueryRow(ctx,
		`SELECT count(*) FROM candles WHERE symbol = $1 AND timeframe = '1m'`, benchSymbol).Scan(&stored)
	if err != nil {
		t.Fatalf("count seeded candles: %v", err)
	}

	wanted := int64(to.Sub(from)/time.Minute) + 1
	if stored >= wanted {
		return
	}

	// A sawtooth: it moves enough for the indicators to have something to
	// converge on, without needing random numbers that would make the run
	// non-reproducible.
	_, err = pool.Exec(ctx, `
		INSERT INTO candles (symbol, market_type, timeframe, open_time, close_time,
		                     open, high, low, close, volume, quote_volume, trade_count, is_closed)
		SELECT $1, 'spot', '1m', t, t + interval '1 minute',
		       27000 + (extract(epoch from t)::bigint % 5000),
		       27000 + (extract(epoch from t)::bigint % 5000) + 10,
		       27000 + (extract(epoch from t)::bigint % 5000) - 10,
		       27000 + (extract(epoch from t)::bigint % 5000) + 2,
		       12.5, 340000, 250, true
		FROM generate_series($2::timestamptz, $3::timestamptz, interval '1 minute') AS t
		ON CONFLICT (symbol, market_type, timeframe, open_time) DO NOTHING`,
		benchSymbol, from, to)
	if err != nil {
		t.Fatalf("seed a year of candles: %v", err)
	}
}

// peakRSSMB reads the high-water mark of this process's resident set.
//
// VmHWM rather than VmRSS: the budget is about the peak a run reaches, and a
// sample taken after the fact would miss a spike the garbage collector had
// already returned.
func peakRSSMB(t *testing.T) int {
	t.Helper()

	status, err := os.ReadFile("/proc/self/status")
	if err != nil {
		t.Skipf("cannot read /proc/self/status on this platform: %v", err)
	}

	for _, line := range strings.Split(string(status), "\n") {
		if !strings.HasPrefix(line, "VmHWM:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			break
		}
		kb, err := strconv.Atoi(fields[1])
		if err != nil {
			break
		}
		return kb / 1024
	}

	t.Skip("no VmHWM in /proc/self/status")
	return 0
}

// TestFullYearWithTheTrendFilterStaysUnderTheMemoryBudget.
//
// Phase 05 adds three more cursors to a run — 5m, 15m and 1h — each paging its
// own series alongside the base one. The budget is unchanged, so the filter
// has to be paid for out of the same 500 MB, and the only way that works is if
// the higher timeframes page rather than load.
//
// For a year that is 105,120 five-minute bars, 35,040 fifteen-minute and 8,760
// hourly on top of 527,040 base bars. Held resident that is most of the
// allowance; paged, it is kilobytes.
func TestFullYearWithTheTrendFilterStaysUnderTheMemoryBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("replaying a year of 1m candles is too slow for -short")
	}

	pool := testhelper.NewTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	from := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2024, 12, 31, 23, 59, 0, 0, time.UTC)
	seedYear(ctx, t, pool, from, to)
	seedHigherTimeframes(ctx, t, pool, from, to)

	candles := _candle_us.NewCandleUsecaseImpl(_candle_repo.NewCandleRepoImpl(pool))
	engine := _backtest_us.NewBacktestUsecaseImpl(
		silentLogger(), candles,
		_datagap_us.NewDataGapUsecaseImpl(_datagap_repo.NewDataGapRepoImpl(pool)),
		_indicator_us.DefaultSetConfig(),
	)

	config := trend.DefaultConfig()
	filter, err := _trend_us.NewFilterImpl(config)
	if err != nil {
		t.Fatalf("NewFilterImpl() returned error: %v", err)
	}
	aligner, err := _trend_us.NewAlignerImpl(_trend_us.AlignerConfig{
		Symbol:     benchSymbol,
		MarketType: constants.MarketTypeSpot,
		Base:       constants.Timeframe1m,
		Higher:     config.Timeframes(),
		From:       from,
		To:         to,
		Indicators: _indicator_us.DefaultSetConfig(),
	}, candles)
	if err != nil {
		t.Fatalf("NewAlignerImpl() returned error: %v", err)
	}

	before := peakRSSMB(t)

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
		Strategy:      &alternating{everyN: 500},
		TrendFilter:   filter,
		TrendAligner:  aligner,
		TrendConfig:   config,
	})
	if err != nil {
		t.Fatalf("Run() with the trend filter returned error: %v", err)
	}

	after := peakRSSMB(t)

	t.Logf("replayed %d bars with the filter on; %d vetoed, %d not-ready; peak RSS %d MB (was %d MB)",
		result.BarsEvaluated, result.BarsVetoed, result.BarsFilterNotReady, after, before)

	if after > rssLimitMB {
		t.Errorf("peak RSS reached %d MB with the filter on, over the %d MB budget", after, rssLimitMB)
	}
}

// seedHigherTimeframes fills 5m, 15m and 1h for the same synthetic series.
func seedHigherTimeframes(ctx context.Context, t *testing.T, pool *pgxpool.Pool, from, to time.Time) {
	t.Helper()

	for _, timeframe := range []struct {
		name     string
		interval string
	}{
		{"5m", "5 minutes"},
		{"15m", "15 minutes"},
		{"1h", "1 hour"},
	} {
		var stored int64
		err := pool.QueryRow(ctx,
			`SELECT count(*) FROM candles WHERE symbol = $1 AND timeframe = $2`,
			benchSymbol, timeframe.name).Scan(&stored)
		if err != nil {
			t.Fatalf("count %s candles: %v", timeframe.name, err)
		}
		if stored > 0 {
			continue
		}

		_, err = pool.Exec(ctx, `
			INSERT INTO candles (symbol, market_type, timeframe, open_time, close_time,
			                     open, high, low, close, volume, quote_volume, trade_count, is_closed)
			SELECT $1, 'spot', $2, t, t + $3::interval,
			       27000 + (extract(epoch from t)::bigint % 5000),
			       27000 + (extract(epoch from t)::bigint % 5000) + 10,
			       27000 + (extract(epoch from t)::bigint % 5000) - 10,
			       27000 + (extract(epoch from t)::bigint % 5000) + 2,
			       12.5, 340000, 250, true
			FROM generate_series($4::timestamptz, $5::timestamptz, $3::interval) AS t
			ON CONFLICT (symbol, market_type, timeframe, open_time) DO NOTHING`,
			benchSymbol, timeframe.name, timeframe.interval, from, to)
		if err != nil {
			t.Fatalf("seed %s candles: %v", timeframe.name, err)
		}
	}
}
