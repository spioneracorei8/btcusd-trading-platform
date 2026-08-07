package usecase_test

import (
	"math"
	"testing"
	"time"

	"github.com/spioneracorei8/btcusd-trading-platform/server/services/indicator"
	_indicator_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/indicator/usecase"
)

// Hand-computed cases reachable through the public API. VWAP qualifies
// because its warm-up is a single candle; the EMA, RSI and ATR equivalents
// live in arithmetic_internal_test.go, where the recurrence can be read
// without waiting out a warm-up window longer than the worked example.

const handTolerance = 1e-9

func handStart() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }

// TestVWAPHandComputed walks three bars of one session.
//
//	bar 1: H=12 L=8  C=10 -> typical 10, volume 2 -> PV 20,  cum 20/2  = 10
//	bar 2: H=24 L=16 C=20 -> typical 20, volume 3 -> PV 60,  cum 80/5  = 16
//	bar 3: H=36 L=24 C=30 -> typical 30, volume 5 -> PV 150, cum 230/10 = 23
func TestVWAPHandComputed(t *testing.T) {
	start := handStart()
	candles := []struct {
		high, low, close, volume float64
	}{
		{12, 8, 10, 2},
		{24, 16, 20, 3},
		{36, 24, 30, 5},
	}
	want := []float64{10, 16, 23}

	vwap := _indicator_us.NewVWAP()

	for i, bar := range candles {
		c := candleAt(start.Add(time.Duration(i)*time.Minute),
			bar.close, bar.high, bar.low, bar.close, bar.volume)

		value, ok := vwap.Update(c)
		if !ok {
			t.Fatalf("bar %d: VWAP emitted nothing", i)
		}
		if math.Abs(value-want[i]) > handTolerance {
			t.Errorf("bar %d = %.10f, want %.10f", i, value, want[i])
		}
	}
}

// TestEvaluateEmitsOnlyReadyValues checks the helper itself: it must skip the
// warm-up rather than record placeholders.
func TestEvaluateEmitsOnlyReadyValues(t *testing.T) {
	const period = 3
	ema, err := _indicator_us.NewEMA(period)
	if err != nil {
		t.Fatalf("NewEMA() returned error: %v", err)
	}

	closes := make([]float64, ema.WarmupPeriod()+4)
	for i := range closes {
		closes[i] = 100 + float64(i)
	}

	values := indicator.Evaluate(ema, closeSeries(handStart(), closes))
	if want := len(closes) - ema.WarmupPeriod() + 1; len(values) != want {
		t.Fatalf("Evaluate() emitted %d values, want %d", len(values), want)
	}
	for i, value := range values {
		if math.IsNaN(value.Value) {
			t.Errorf("value %d is NaN", i)
		}
		if value.OpenTime.IsZero() {
			t.Errorf("value %d has no open time", i)
		}
	}
}

func TestIndicatorNames(t *testing.T) {
	ema, _ := _indicator_us.NewEMA(200)
	rsi, _ := _indicator_us.NewRSI(14)
	atr, _ := _indicator_us.NewATR(14)

	for _, tt := range []struct {
		got  string
		want string
	}{
		{ema.Name(), "ema_200"},
		{rsi.Name(), "rsi_14"},
		{atr.Name(), "atr_14"},
		{_indicator_us.NewVWAP().Name(), "vwap_daily_utc"},
	} {
		if tt.got != tt.want {
			t.Errorf("Name() = %q, want %q", tt.got, tt.want)
		}
	}
}
