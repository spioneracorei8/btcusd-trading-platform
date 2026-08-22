package repository_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/signal"
	_signal_repo "github.com/spioneracorei8/btcusd-trading-platform/server/services/signal/repository"
	_signal_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/signal/usecase"
	"github.com/spioneracorei8/btcusd-trading-platform/server/testhelper"
)

// TestASignalSurvivesTheRoundTripThroughPostgres is the phase-06 acceptance
// item that only a real database can answer.
//
// The usecase tests prove the rules; they cannot prove the reason arrives
// intact. `reason` is jsonb, and a []byte handed to pgx can just as easily be
// stored as an escaped string — at which case the column still reads back
// without error and the audit record is quietly worthless. The failure would
// not show up until somebody queried a stored signal months later, which is
// exactly when it cannot be fixed retroactively.
//
// So this drives the live path — usecase, repository, PostgreSQL — and then
// asks the database, not Go, to take the value apart.
func TestASignalSurvivesTheRoundTripThroughPostgres(t *testing.T) {
	pool := testhelper.NewTestPool(t)
	const symbol = "TESTREASON"
	testhelper.CleanupSymbol(t, pool, symbol)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	us := _signal_us.NewSignalUsecaseImpl(_signal_repo.NewSignalRepoImpl(pool))

	open := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	bar := models.Candle{
		Symbol: symbol, MarketType: constants.MarketTypeSpot,
		Timeframe: constants.Timeframe5m,
		OpenTime:  open, CloseTime: open.Add(5 * time.Minute),
		Open:  decimal.RequireFromString("64000.00"),
		High:  decimal.RequireFromString("64120.50"),
		Low:   decimal.RequireFromString("63980.25"),
		Close: decimal.RequireFromString("64080.25"),
		// The rule under CLAUDE.md §3.1: only a closed bar may produce one.
		IsClosed: true,
	}

	reason := signal.BuildReason("ema(9) crossed above ema(21)", bar,
		models.IndicatorSnapshot{OpenTime: open, EMA: 64010.5, RSI: 58.25, ATR: 130.75, VWAP: 64005},
		"64080.25", "63950.00", "64470.50",
		signal.StrategyReason{Name: "ema_crossover", Version: "v1"})
	reason.Trend = &signal.TrendReason{
		Filter: "ema_rsi_mtf", Version: "v1", Bias: "bullish",
		Confidence: 0.62, Ready: true,
		PerTF: []signal.TimeframeReason{
			{Timeframe: "15m", Score: 0.55, Weight: 0.5,
				CloseTime: open.Format(time.RFC3339), Ready: true},
			{Timeframe: "1h", Score: 0.80, Weight: 0.5,
				CloseTime: open.Truncate(time.Hour).Format(time.RFC3339), Ready: true},
		},
	}

	encoded, err := reason.Encode()
	if err != nil {
		t.Fatalf("Encode() returned error: %v", err)
	}

	stored, err := us.CreateSignal(ctx, models.Signal{
		Symbol: symbol, MarketType: bar.MarketType, Timeframe: bar.Timeframe,
		SignalTime:      bar.CloseTime,
		Direction:       constants.DirectionLong,
		Strength:        decimal.RequireFromString("72.50"),
		EntryPrice:      decimal.NullDecimal{Decimal: bar.Close, Valid: true},
		StopLoss:        decimal.NullDecimal{Decimal: decimal.RequireFromString("63950.00"), Valid: true},
		TakeProfit:      decimal.NullDecimal{Decimal: decimal.RequireFromString("64470.50"), Valid: true},
		StrategyName:    "ema_crossover",
		StrategyVersion: "v1",
		Reason:          encoded,
	}, bar)
	if err != nil {
		t.Fatalf("CreateSignal() returned error: %v", err)
	}

	// PostgreSQL takes the value apart. If the payload had been stored as a
	// JSON string rather than an object these operators would return NULL
	// instead of the values, which a Go-side unmarshal would never notice.
	var (
		trigger   string
		rsi       float64
		bias      string
		htfClose  string
		stopLevel string
	)
	err = pool.QueryRow(ctx, `
		SELECT reason ->> 'trigger',
		       (reason -> 'indicators' ->> 'rsi')::float8,
		       reason -> 'trend' ->> 'bias',
		       reason -> 'trend' -> 'per_timeframe' -> 1 ->> 'close_time',
		       reason -> 'levels' ->> 'stop'
		  FROM signals WHERE id = $1`, stored.Id).
		Scan(&trigger, &rsi, &bias, &htfClose, &stopLevel)
	if err != nil {
		t.Fatalf("the stored reason could not be queried as jsonb: %v", err)
	}

	if trigger != "ema(9) crossed above ema(21)" {
		t.Errorf("trigger read back as %q", trigger)
	}
	if rsi != 58.25 {
		t.Errorf("rsi read back as %v, want 58.25", rsi)
	}
	if bias != "bullish" {
		t.Errorf("trend bias read back as %q, want bullish", bias)
	}
	// Verbatim, including the trailing zeros the strategy wrote. A level that
	// was reformatted on the way through would no longer be the number the
	// owner was shown.
	if stopLevel != "63950.00" {
		t.Errorf("stop level read back as %q, want 63950.00", stopLevel)
	}
	// The higher-timeframe close time is the evidence there was no
	// cross-timeframe look-ahead, and it is the field most likely to be lost:
	// it lives two levels down inside an array.
	if htfClose != open.Truncate(time.Hour).Format(time.RFC3339) {
		t.Errorf("the 1h close_time read back as %q", htfClose)
	}

	// And the whole document still parses, so nothing was truncated on the way.
	var whole map[string]any
	if err := json.Unmarshal(stored.Reason, &whole); err != nil {
		t.Fatalf("the returned reason is not valid JSON: %v", err)
	}
	for _, key := range []string{"trigger", "bar", "indicators", "trend", "levels"} {
		if _, ok := whole[key]; !ok {
			t.Errorf("the returned reason lost %q", key)
		}
	}
}

// TestAForminBarProducesNoRowAtAll checks the closed-candle rule holds with a
// real database behind it, not only against a spy.
//
// The usecase is the only thing standing between a forming bar and a
// notification the owner cannot un-receive, so it is worth proving that
// nothing reaches the table when it refuses — a refusal that still inserted
// would be the worst of both.
func TestAForminBarProducesNoRowAtAll(t *testing.T) {
	pool := testhelper.NewTestPool(t)
	const symbol = "TESTFORMING"
	testhelper.CleanupSymbol(t, pool, symbol)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	us := _signal_us.NewSignalUsecaseImpl(_signal_repo.NewSignalRepoImpl(pool))

	open := time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC)
	forming := models.Candle{
		Symbol: symbol, MarketType: constants.MarketTypeSpot,
		Timeframe: constants.Timeframe5m,
		OpenTime:  open, CloseTime: open.Add(5 * time.Minute),
		Open:  decimal.RequireFromString("64000"),
		High:  decimal.RequireFromString("64100"),
		Low:   decimal.RequireFromString("63900"),
		Close: decimal.RequireFromString("64050"),
		// Binance sends this as k.x == false. It must reach nothing downstream.
		IsClosed: false,
	}

	encoded, err := signal.BuildReason("test", forming,
		models.IndicatorSnapshot{OpenTime: open, EMA: 1, RSI: 2, ATR: 3, VWAP: 4},
		"1", "2", "3",
		signal.StrategyReason{Name: "ema_crossover", Version: "v1"}).Encode()
	if err != nil {
		t.Fatalf("Encode() returned error: %v", err)
	}

	if _, err := us.CreateSignal(ctx, models.Signal{
		Symbol: symbol, MarketType: forming.MarketType, Timeframe: forming.Timeframe,
		SignalTime:      forming.CloseTime,
		Direction:       constants.DirectionLong,
		Strength:        decimal.RequireFromString("50"),
		StrategyName:    "ema_crossover",
		StrategyVersion: "v1",
		Reason:          encoded,
	}, forming); err == nil {
		t.Fatal("a signal from a forming bar was accepted")
	}

	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM signals WHERE symbol = $1`, symbol).Scan(&count); err != nil {
		t.Fatalf("count signals: %v", err)
	}
	if count != 0 {
		t.Errorf("%d rows reached the signals table from a forming bar", count)
	}
}
