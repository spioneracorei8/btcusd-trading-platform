package usecase_test

import (
	"encoding/csv"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/indicator"
	_indicator_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/indicator/usecase"
)

// relativeTolerance is the agreement required against the external reference.
const relativeTolerance = 1e-6

// Periods must match testdata/indicator/generate_reference.py.
const (
	referenceEMAPeriod = 200
	referenceRSIPeriod = 14
	referenceATRPeriod = 14
)

// expectedValue is one row of the TA-Lib reference. A NaN means the reference
// had no value at that bar, which its own warm-up explains.
type expectedValue struct {
	OpenTime time.Time
	EMA      float64
	RSI      float64
	ATR      float64
	VWAP     float64
}

func fixturePath(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join("..", "..", "..", "testdata", "indicator", name)
}

func readCSV(t *testing.T, name string) [][]string {
	t.Helper()

	file, err := os.Open(fixturePath(t, name))
	if err != nil {
		t.Fatalf("open fixture %s: %v", name, err)
	}
	defer func() { _ = file.Close() }()

	rows, err := csv.NewReader(file).ReadAll()
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	if len(rows) < 2 {
		t.Fatalf("fixture %s has no data rows", name)
	}
	return rows[1:] // drop the header
}

func parseFloat(t *testing.T, raw string) float64 {
	t.Helper()
	if raw == "" {
		return math.NaN()
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return value
}

// loadReferenceCandles reads the synthetic OHLCV series.
func loadReferenceCandles(t *testing.T) []models.Candle {
	t.Helper()

	rows := readCSV(t, "btcusdt_1m_reference.csv")
	candles := make([]models.Candle, 0, len(rows))

	for i, row := range rows {
		if len(row) < 6 {
			t.Fatalf("row %d has %d columns, want 6", i, len(row))
		}
		openMillis, err := strconv.ParseInt(row[0], 10, 64)
		if err != nil {
			t.Fatalf("row %d: parse open_time: %v", i, err)
		}
		openTime := time.UnixMilli(openMillis).UTC()

		candles = append(candles, models.Candle{
			Symbol:     "BTCUSDT",
			MarketType: constants.MarketTypeSpot,
			Timeframe:  constants.Timeframe1m,
			OpenTime:   openTime,
			CloseTime:  openTime.Add(time.Minute).Add(-time.Millisecond),
			Open:       decimal.RequireFromString(row[1]),
			High:       decimal.RequireFromString(row[2]),
			Low:        decimal.RequireFromString(row[3]),
			Close:      decimal.RequireFromString(row[4]),
			Volume:     decimal.RequireFromString(row[5]),
			// quote_volume is not used by any indicator; VWAP weights by base
			// volume, per ADR 0008.
			QuoteVolume: decimal.RequireFromString(row[5]),
			TradeCount:  1,
			IsClosed:    true,
		})
	}
	return candles
}

// loadExpected reads the TA-Lib reference values.
func loadExpected(t *testing.T) []expectedValue {
	t.Helper()

	rows := readCSV(t, "btcusdt_1m_expected.csv")
	expected := make([]expectedValue, 0, len(rows))

	for i, row := range rows {
		if len(row) < 5 {
			t.Fatalf("expected row %d has %d columns, want 5", i, len(row))
		}
		openMillis, err := strconv.ParseInt(row[0], 10, 64)
		if err != nil {
			t.Fatalf("expected row %d: parse open_time: %v", i, err)
		}

		expected = append(expected, expectedValue{
			OpenTime: time.UnixMilli(openMillis).UTC(),
			EMA:      parseFloat(t, row[1]),
			RSI:      parseFloat(t, row[2]),
			ATR:      parseFloat(t, row[3]),
			VWAP:     parseFloat(t, row[4]),
		})
	}
	return expected
}

// assertClose compares against the reference within the relative tolerance.
func assertClose(t *testing.T, name string, openTime time.Time, got, want float64) {
	t.Helper()

	if math.IsNaN(want) {
		t.Fatalf("%s at %s: the reference has no value here; the test should not be comparing it",
			name, openTime.Format(time.RFC3339))
	}
	if math.IsNaN(got) {
		t.Fatalf("%s at %s: produced NaN while the reference has %g",
			name, openTime.Format(time.RFC3339), want)
	}

	diff := math.Abs(got - want)
	scale := math.Abs(want)
	if scale < 1 {
		scale = 1 // absolute comparison for values near zero
	}
	if relative := diff / scale; relative > relativeTolerance {
		t.Fatalf("%s at %s: got %.12g, reference %.12g (relative error %.3g exceeds %g)",
			name, openTime.Format(time.RFC3339), got, want, relative, relativeTolerance)
	}
}

// expectedByTime indexes the reference for lookup by candle.
func expectedByTime(expected []expectedValue) map[time.Time]expectedValue {
	index := make(map[time.Time]expectedValue, len(expected))
	for _, e := range expected {
		index[e.OpenTime] = e
	}
	return index
}

// TestEMAMatchesReference compares against TA-Lib after our warm-up window.
//
// Our warm-up is deliberately longer than TA-Lib's: it emits at period-1 while
// this implementation waits 5x the period. Values inside our window are not
// compared, which is what phase 03 section 5 asks for.
func TestEMAMatchesReference(t *testing.T) {
	candles := loadReferenceCandles(t)
	index := expectedByTime(loadExpected(t))

	ema, err := _indicator_us.NewEMA(referenceEMAPeriod)
	if err != nil {
		t.Fatalf("NewEMA() returned error: %v", err)
	}

	values := indicator.Evaluate(ema, candles)
	if len(values) == 0 {
		t.Fatal("EMA emitted no values")
	}

	for _, value := range values {
		want, ok := index[value.OpenTime]
		if !ok {
			t.Fatalf("no reference row for %s", value.OpenTime)
		}
		assertClose(t, "ema", value.OpenTime, value.Value, want.EMA)
	}
	t.Logf("compared %d EMA values against TA-Lib", len(values))
}

func TestRSIMatchesReference(t *testing.T) {
	candles := loadReferenceCandles(t)
	index := expectedByTime(loadExpected(t))

	rsi, err := _indicator_us.NewRSI(referenceRSIPeriod)
	if err != nil {
		t.Fatalf("NewRSI() returned error: %v", err)
	}

	values := indicator.Evaluate(rsi, candles)
	if len(values) == 0 {
		t.Fatal("RSI emitted no values")
	}

	for _, value := range values {
		want := index[value.OpenTime]
		assertClose(t, "rsi", value.OpenTime, value.Value, want.RSI)
	}
	t.Logf("compared %d RSI values against TA-Lib (Wilder)", len(values))
}

func TestATRMatchesReference(t *testing.T) {
	candles := loadReferenceCandles(t)
	index := expectedByTime(loadExpected(t))

	atr, err := _indicator_us.NewATR(referenceATRPeriod)
	if err != nil {
		t.Fatalf("NewATR() returned error: %v", err)
	}

	values := indicator.Evaluate(atr, candles)
	if len(values) == 0 {
		t.Fatal("ATR emitted no values")
	}

	for _, value := range values {
		want := index[value.OpenTime]
		assertClose(t, "atr", value.OpenTime, value.Value, want.ATR)
	}
	t.Logf("compared %d ATR values against TA-Lib (Wilder)", len(values))
}

// TestVWAPMatchesReference compares against the reference implementation of
// the same rule. TA-Lib has no VWAP, so the script expresses the definition
// independently rather than offering a second opinion on it.
func TestVWAPMatchesReference(t *testing.T) {
	candles := loadReferenceCandles(t)
	index := expectedByTime(loadExpected(t))

	values := indicator.Evaluate(_indicator_us.NewVWAP(), candles)
	if len(values) != len(candles) {
		t.Fatalf("VWAP emitted %d values for %d candles", len(values), len(candles))
	}

	for _, value := range values {
		want := index[value.OpenTime]
		assertClose(t, "vwap", value.OpenTime, value.Value, want.VWAP)
	}
	t.Logf("compared %d VWAP values", len(values))
}
