package usecase

import (
	"math"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
)

// Hand-computed cases, small enough to check in a spreadsheet.
//
// They catch what a 5000-bar fixture masks: an off-by-one in the seed, the
// wrong smoothing coefficient, the first bar counted when it should not be. A
// large fixture agrees or disagrees as a whole and says nothing about which
// line is wrong.
//
// These are internal tests because every expected value falls inside the
// warm-up window, where Update deliberately returns NaN. Reading the
// recurrence directly is the point: warm-up is verified separately, and here
// only the arithmetic is under test.

const handTolerance = 1e-9

func handStart() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }

func bar(openTime time.Time, high, low, close, volume float64) models.Candle {
	return models.Candle{
		Symbol:      "BTCUSDT",
		MarketType:  constants.MarketTypeSpot,
		Timeframe:   constants.Timeframe1m,
		OpenTime:    openTime,
		CloseTime:   openTime.Add(time.Minute).Add(-time.Millisecond),
		Open:        decimal.NewFromFloat(close),
		High:        decimal.NewFromFloat(high),
		Low:         decimal.NewFromFloat(low),
		Close:       decimal.NewFromFloat(close),
		Volume:      decimal.NewFromFloat(volume),
		QuoteVolume: decimal.NewFromFloat(close * volume),
		TradeCount:  1,
		IsClosed:    true,
	}
}

func closeBars(closes []float64) []models.Candle {
	candles := make([]models.Candle, 0, len(closes))
	for i, close := range closes {
		candles = append(candles,
			bar(handStart().Add(time.Duration(i)*time.Minute), close+1, close-1, close, 10))
	}
	return candles
}

func assertHand(t *testing.T, label string, index int, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > handTolerance {
		t.Errorf("%s value %d = %.10f, want %.10f", label, index, got, want)
	}
}

// TestEMAHandComputed walks EMA(3) over eight closes.
//
// multiplier k = 2/(3+1) = 0.5
// seed = SMA of the first three closes = (10+11+12)/3 = 11
//
//	bar 4 (13): (13-11)*0.5 + 11     = 12
//	bar 5 (14): (14-12)*0.5 + 12     = 13
//	bar 6 (13): (13-13)*0.5 + 13     = 13
//	bar 7 (12): (12-13)*0.5 + 13     = 12.5
//	bar 8 (20): (20-12.5)*0.5 + 12.5 = 16.25
func TestEMAHandComputed(t *testing.T) {
	ema, err := NewEMA(3)
	if err != nil {
		t.Fatalf("NewEMA() returned error: %v", err)
	}

	closes := []float64{10, 11, 12, 13, 14, 13, 12, 20}
	want := []float64{11, 12, 13, 13, 12.5, 16.25}

	got := make([]float64, 0, len(want))
	for _, c := range closeBars(closes) {
		ema.Update(c)
		if ema.seeded {
			got = append(got, ema.value)
		}
	}

	if len(got) != len(want) {
		t.Fatalf("computed %d values, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		assertHand(t, "ema", i, got[i], want[i])
	}
}

// TestRSIHandComputed walks RSI(3) over five closes.
//
// closes: 100, 102, 101, 105, 104
// deltas:      +2,  -1,  +4,  -1
//
// Wilder seed over the first three deltas:
//
//	avgGain = (2 + 0 + 4)/3 = 2
//	avgLoss = (0 + 1 + 0)/3 = 1/3
//	RS = 6, RSI = 100 - 100/7
//
// Then the fourth delta (-1), smoothed with p = 3:
//
//	avgGain = (2*2 + 0)/3     = 4/3
//	avgLoss = ((1/3)*2 + 1)/3 = 5/9
//	RS = 2.4, RSI = 100 - 100/3.4
func TestRSIHandComputed(t *testing.T) {
	rsi, err := NewRSI(3)
	if err != nil {
		t.Fatalf("NewRSI() returned error: %v", err)
	}

	closes := []float64{100, 102, 101, 105, 104}
	want := []float64{100 - 100.0/7.0, 100 - 100.0/3.4}

	got := make([]float64, 0, len(want))
	for _, c := range closeBars(closes) {
		rsi.Update(c)
		if rsi.deltas >= rsi.period {
			got = append(got, rsi.currentValue())
		}
	}

	if len(got) != len(want) {
		t.Fatalf("computed %d values, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		assertHand(t, "rsi", i, got[i], want[i])
	}
}

// TestATRHandComputed walks ATR(3) over five bars.
//
//	bar 1: H=12 L=10 C=11  no previous close, so no true range at all
//	bar 2: H=13 L=11 C=12  TR = max(2, |13-11|, |11-11|) = 2
//	bar 3: H=15 L=12 C=14  TR = max(3, |15-12|, |12-12|) = 3
//	bar 4: H=14 L=12 C=13  TR = max(2, |14-14|, |12-14|) = 2
//	bar 5: H=18 L=13 C=17  TR = max(5, |18-13|, |13-13|) = 5
//
// seed = (2+3+2)/3 = 7/3
// bar 5: ((7/3)*2 + 5)/3
func TestATRHandComputed(t *testing.T) {
	atr, err := NewATR(3)
	if err != nil {
		t.Fatalf("NewATR() returned error: %v", err)
	}

	bars := [][3]float64{
		{12, 10, 11},
		{13, 11, 12},
		{15, 12, 14},
		{14, 12, 13},
		{18, 13, 17},
	}
	want := []float64{7.0 / 3.0, (7.0/3.0*2 + 5) / 3.0}

	got := make([]float64, 0, len(want))
	for i, b := range bars {
		atr.Update(bar(handStart().Add(time.Duration(i)*time.Minute), b[0], b[1], b[2], 1))
		if atr.ranges >= atr.period {
			got = append(got, atr.value)
		}
	}

	if len(got) != len(want) {
		t.Fatalf("computed %d values, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		assertHand(t, "atr", i, got[i], want[i])
	}
}

// TestATRIgnoresTheFirstBarsSpan pins the seeding convention that differs
// between implementations. The opening bar has no previous close, so it has no
// true range — only its own span, which is a different quantity. Averaging it
// in shifts every subsequent value; against the TA-Lib fixture the error was
// 0.06%, small enough to look like rounding and large enough to matter.
func TestATRIgnoresTheFirstBarsSpan(t *testing.T) {
	atr, err := NewATR(3)
	if err != nil {
		t.Fatalf("NewATR() returned error: %v", err)
	}

	// A deliberately enormous opening span. If it reached the seed it would
	// dominate the average and the result would be nowhere near 7/3.
	bars := [][3]float64{
		{1000, 1, 11},
		{13, 11, 12},
		{15, 12, 14},
		{14, 12, 13},
	}
	for i, b := range bars {
		atr.Update(bar(handStart().Add(time.Duration(i)*time.Minute), b[0], b[1], b[2], 1))
	}

	if atr.ranges != 3 {
		t.Fatalf("counted %d true ranges from 4 bars, want 3", atr.ranges)
	}
	assertHand(t, "atr", 0, atr.value, 7.0/3.0)
}
