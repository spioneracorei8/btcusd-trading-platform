package usecase_test

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	_candle_repo "github.com/spioneracorei8/btcusd-trading-platform/server/services/candle/repository"
	_candle_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/candle/usecase"
	_datagap_repo "github.com/spioneracorei8/btcusd-trading-platform/server/services/datagap/repository"
	_datagap_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/datagap/usecase"
	_market_repo "github.com/spioneracorei8/btcusd-trading-platform/server/services/market/repository"
	_market_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/market/usecase"
	"github.com/spioneracorei8/btcusd-trading-platform/server/testhelper"
)

// The defect this file is about: on a machine whose history arrived by
// restoring a dump, every timeframe reported earliest_open_time: null while
// the data was demonstrably present.
//
// Restoring from a dump is a first-class way for this system to acquire data —
// it is how a developer machine is meant to be seeded — so any field that goes
// blank under restore is reporting on the wrong thing. These tests seed by
// insert alone, with no collector having ever run, which is exactly the state
// a restored database is in.

const restoreSymbol = "TESTRESTORE"

// seedByInsertAlone writes candles directly, the way a restored dump arrives:
// rows in the table and nothing in collector_status.
func seedByInsertAlone(
	ctx context.Context,
	t *testing.T,
	candles interface {
		SaveCandles(context.Context, []models.Candle) error
	},
	timeframe constants.Timeframe,
	first time.Time,
	count int,
) {
	t.Helper()

	batch := make([]models.Candle, 0, count)
	for i := range count {
		open := first.Add(time.Duration(i) * timeframe.Duration())
		price := decimal.NewFromInt(27000 + int64(i))
		batch = append(batch, models.Candle{
			Symbol: restoreSymbol, MarketType: constants.MarketTypeSpot,
			Timeframe: timeframe,
			OpenTime:  open, CloseTime: open.Add(timeframe.Duration()),
			Open: price, High: price.Add(decimal.NewFromInt(5)),
			Low: price.Sub(decimal.NewFromInt(5)), Close: price,
			Volume: decimal.NewFromInt(1), QuoteVolume: decimal.NewFromInt(27000),
			TradeCount: 1, IsClosed: true,
		})
	}
	if err := candles.SaveCandles(ctx, batch); err != nil {
		t.Fatalf("seed %s: %v", timeframe, err)
	}
}

// TestEarliestOpenTimeSurvivesARestore is the fix, stated as the situation
// that produced it.
func TestEarliestOpenTimeSurvivesARestore(t *testing.T) {
	pool := testhelper.NewTestPool(t)
	testhelper.CleanupSymbol(t, pool, restoreSymbol)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	candles := _candle_us.NewCandleUsecaseImpl(_candle_repo.NewCandleRepoImpl(pool))
	gaps := _datagap_us.NewDataGapUsecaseImpl(_datagap_repo.NewDataGapRepoImpl(pool))

	first := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)
	seedByInsertAlone(ctx, t, candles, constants.Timeframe1m, first, 120)
	seedByInsertAlone(ctx, t, candles, constants.Timeframe5m, first, 60)

	us := _market_us.NewMarketUsecaseImpl(
		_market_us.Config{
			Symbol:     restoreSymbol,
			MarketType: constants.MarketTypeSpot,
			Timeframes: []constants.Timeframe{constants.Timeframe1m, constants.Timeframe5m},
		},
		silentLogger(),
		nil, // no market data client: nothing here reaches the network
		_market_repo.NewCollectorStatusRepoImpl(pool),
		candles,
		gaps,
	)

	status, err := us.Status(ctx, time.Now().UTC())
	if err != nil {
		t.Fatalf("Status() returned error: %v", err)
	}
	if len(status.Timeframes) != 2 {
		t.Fatalf("got %d timeframes, want 2", len(status.Timeframes))
	}

	for _, timeframe := range status.Timeframes {
		if timeframe.EarliestOpenTime == nil {
			t.Errorf("%s reports earliest_open_time: null, but the rows are in the table. "+
				"A field describing stored data must come from the table, not from state "+
				"this process happens to have accumulated.", timeframe.Timeframe)
			continue
		}
		if !timeframe.EarliestOpenTime.Equal(first) {
			t.Errorf("%s earliest_open_time is %s, want %s",
				timeframe.Timeframe, timeframe.EarliestOpenTime, first)
		}
	}
}

// TestEarliestMatchesTheTableForEveryTimeframe checks the reported value
// against MIN(open_time) asked of the database directly, so the two cannot
// drift apart without this failing.
func TestEarliestMatchesTheTableForEveryTimeframe(t *testing.T) {
	pool := testhelper.NewTestPool(t)
	testhelper.CleanupSymbol(t, pool, restoreSymbol)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	candles := _candle_us.NewCandleUsecaseImpl(_candle_repo.NewCandleRepoImpl(pool))
	gaps := _datagap_us.NewDataGapUsecaseImpl(_datagap_repo.NewDataGapRepoImpl(pool))

	// Deliberately different starts per timeframe: a single shared constant
	// would pass even if the query ignored the timeframe entirely.
	starts := map[constants.Timeframe]time.Time{
		constants.Timeframe1m:  time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
		constants.Timeframe5m:  time.Date(2023, 2, 1, 0, 0, 0, 0, time.UTC),
		constants.Timeframe15m: time.Date(2023, 3, 1, 0, 0, 0, 0, time.UTC),
	}
	order := []constants.Timeframe{
		constants.Timeframe1m, constants.Timeframe5m, constants.Timeframe15m,
	}
	for _, timeframe := range order {
		seedByInsertAlone(ctx, t, candles, timeframe, starts[timeframe], 30)
	}

	us := _market_us.NewMarketUsecaseImpl(
		_market_us.Config{
			Symbol: restoreSymbol, MarketType: constants.MarketTypeSpot, Timeframes: order,
		},
		silentLogger(),
		nil,
		_market_repo.NewCollectorStatusRepoImpl(pool),
		candles,
		gaps,
	)

	status, err := us.Status(ctx, time.Now().UTC())
	if err != nil {
		t.Fatalf("Status() returned error: %v", err)
	}

	for _, reported := range status.Timeframes {
		var actual time.Time
		err := pool.QueryRow(ctx,
			`SELECT min(open_time) FROM candles
			  WHERE symbol = $1 AND market_type = 'spot' AND timeframe = $2`,
			restoreSymbol, reported.Timeframe.String()).Scan(&actual)
		if err != nil {
			t.Fatalf("query min(open_time) for %s: %v", reported.Timeframe, err)
		}

		if reported.EarliestOpenTime == nil {
			t.Errorf("%s reports null; the table says %s", reported.Timeframe, actual)
			continue
		}
		if !reported.EarliestOpenTime.Equal(actual) {
			t.Errorf("%s reports %s; the table says %s",
				reported.Timeframe, reported.EarliestOpenTime, actual)
		}
	}
}

// TestATimeframeWithNoRowsReportsNull. Null has to keep meaning "genuinely
// absent". If it also meant "present but unrecorded", as it did on the
// restored machine, the field could not be relied on for either.
func TestATimeframeWithNoRowsReportsNull(t *testing.T) {
	pool := testhelper.NewTestPool(t)
	testhelper.CleanupSymbol(t, pool, restoreSymbol)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	candles := _candle_us.NewCandleUsecaseImpl(_candle_repo.NewCandleRepoImpl(pool))
	gaps := _datagap_us.NewDataGapUsecaseImpl(_datagap_repo.NewDataGapRepoImpl(pool))

	first := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)
	seedByInsertAlone(ctx, t, candles, constants.Timeframe1m, first, 10)
	// 1h is configured but never seeded.

	us := _market_us.NewMarketUsecaseImpl(
		_market_us.Config{
			Symbol: restoreSymbol, MarketType: constants.MarketTypeSpot,
			Timeframes: []constants.Timeframe{constants.Timeframe1m, constants.Timeframe1h},
		},
		silentLogger(),
		nil,
		_market_repo.NewCollectorStatusRepoImpl(pool),
		candles,
		gaps,
	)

	status, err := us.Status(ctx, time.Now().UTC())
	if err != nil {
		t.Fatalf("Status() returned error: %v", err)
	}

	for _, timeframe := range status.Timeframes {
		switch timeframe.Timeframe {
		case constants.Timeframe1m:
			if timeframe.EarliestOpenTime == nil {
				t.Error("1m has rows but reports null")
			}
		case constants.Timeframe1h:
			if timeframe.EarliestOpenTime != nil {
				t.Errorf("1h has no rows but reports %s", timeframe.EarliestOpenTime)
			}
			if timeframe.LatestOpenTime != nil {
				t.Errorf("1h has no rows but reports a latest of %s", timeframe.LatestOpenTime)
			}
		}
	}
}
