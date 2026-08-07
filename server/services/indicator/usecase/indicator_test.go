package usecase_test

import (
	"math"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/indicator"
	_indicator_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/indicator/usecase"
)

// candleAt builds a bar with the given OHLC, one minute long.
func candleAt(openTime time.Time, open, high, low, close, volume float64) models.Candle {
	return models.Candle{
		Symbol:      "BTCUSDT",
		MarketType:  constants.MarketTypeSpot,
		Timeframe:   constants.Timeframe1m,
		OpenTime:    openTime,
		CloseTime:   openTime.Add(time.Minute).Add(-time.Millisecond),
		Open:        decimal.NewFromFloat(open),
		High:        decimal.NewFromFloat(high),
		Low:         decimal.NewFromFloat(low),
		Close:       decimal.NewFromFloat(close),
		Volume:      decimal.NewFromFloat(volume),
		QuoteVolume: decimal.NewFromFloat(close * volume),
		TradeCount:  1,
		IsClosed:    true,
	}
}

// closeSeries turns a list of closes into candles with a sane bracket.
func closeSeries(start time.Time, closes []float64) []models.Candle {
	candles := make([]models.Candle, 0, len(closes))
	for i, close := range closes {
		openTime := start.Add(time.Duration(i) * time.Minute)
		candles = append(candles, candleAt(openTime, close, close+1, close-1, close, 10))
	}
	return candles
}

// ---------------------------------------------------------------------------
// Constructors
// ---------------------------------------------------------------------------

func TestConstructorsRejectInvalidPeriods(t *testing.T) {
	for _, period := range []int{-1, 0, 1} {
		if _, err := _indicator_us.NewEMA(period); err == nil {
			t.Errorf("NewEMA(%d) was accepted", period)
		}
		if _, err := _indicator_us.NewRSI(period); err == nil {
			t.Errorf("NewRSI(%d) was accepted", period)
		}
		if _, err := _indicator_us.NewATR(period); err == nil {
			t.Errorf("NewATR(%d) was accepted", period)
		}
	}
	if _, err := _indicator_us.NewSet(_indicator_us.SetConfig{EMAPeriod: 1, RSIPeriod: 14, ATRPeriod: 14}); err == nil {
		t.Error("NewSet with an invalid EMA period was accepted")
	}
}

// ---------------------------------------------------------------------------
// Warm-up
// ---------------------------------------------------------------------------

// TestReadyIsFalseForTheWholeWarmupWindow covers the rule that there is no
// partial-credit state: an indicator is either warmed up or it is not.
func TestReadyIsFalseForTheWholeWarmupWindow(t *testing.T) {
	const period = 14
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	build := map[string]func() indicator.Indicator{
		"ema": func() indicator.Indicator { ind, _ := _indicator_us.NewEMA(period); return ind },
		"rsi": func() indicator.Indicator { ind, _ := _indicator_us.NewRSI(period); return ind },
		"atr": func() indicator.Indicator { ind, _ := _indicator_us.NewATR(period); return ind },
	}

	for name, newIndicator := range build {
		t.Run(name, func(t *testing.T) {
			ind := newIndicator()
			warmup := ind.WarmupPeriod()
			if warmup != period*constants.WarmupMultiplier {
				t.Fatalf("WarmupPeriod() = %d, want %d", warmup, period*constants.WarmupMultiplier)
			}

			closes := make([]float64, warmup+5)
			for i := range closes {
				closes[i] = 64000 + float64(i)*3
			}

			for i, c := range closeSeries(start, closes) {
				value, ok := ind.Update(c)
				consumed := i + 1

				switch {
				case consumed < warmup:
					if ok {
						t.Fatalf("candle %d of %d: emitted before warm-up finished", consumed, warmup)
					}
					if ind.Ready() {
						t.Fatalf("candle %d of %d: Ready() is true during warm-up", consumed, warmup)
					}
					// Never a zero standing in for "no value": a zero would
					// flow into a strategy indistinguishably from a real one.
					if !math.IsNaN(value) {
						t.Fatalf("candle %d: value %g during warm-up, want NaN", consumed, value)
					}
				default:
					if !ok {
						t.Fatalf("candle %d of %d: still not ready after warm-up", consumed, warmup)
					}
					if !ind.Ready() {
						t.Fatalf("candle %d: Ready() is false after emitting", consumed)
					}
					if math.IsNaN(value) {
						t.Fatalf("candle %d: emitted NaN with ok=true", consumed)
					}
				}
			}
		})
	}
}

// TestEMAConvergenceDependsOnHistoryLength documents the hazard warm-up exists
// for, rather than guarding a regression.
//
// An EMA never fully forgets its seed. Two EMA(200) instances ending on the
// same candle but fed different amounts of history hold measurably different
// values, so a backtest that starts scoring at the first bar with "a value" is
// scoring against numbers that have not converged.
func TestEMAConvergenceDependsOnHistoryLength(t *testing.T) {
	const (
		period   = 200
		endIndex = 4000
	)
	candles := loadReferenceCandles(t)

	// Both runs end on the same candle. Each is fed enough history to finish
	// warm-up, so both values come from the indicator itself rather than any
	// reimplementation — but one has seen four times as much history.
	shortRun := lastEmittedValue(t, period, candles[endIndex-1200:endIndex])
	longRun := lastEmittedValue(t, period, candles[:endIndex])

	relative := math.Abs(shortRun-longRun) / math.Abs(longRun)
	if relative < 1e-12 {
		t.Fatalf("the two EMAs agree to %.3g, which would mean the seed is forgotten "+
			"and warm-up were unnecessary", relative)
	}
	t.Logf("EMA(%d) ending on the same candle: %.8f after 1200 bars vs %.8f after %d "+
		"(relative difference %.3g)", period, shortRun, longRun, endIndex, relative)
}

// TestEMAWithOnlyPeriodCandlesEmitsNothing is the other half of the same
// point, and the reason the comparison above needs 1200 bars: an EMA(200) fed
// exactly 200 candles has a number internally, but it is unconverged and the
// warm-up rule refuses to hand it out at all.
func TestEMAWithOnlyPeriodCandlesEmitsNothing(t *testing.T) {
	const period = 200
	candles := loadReferenceCandles(t)

	ema, err := _indicator_us.NewEMA(period)
	if err != nil {
		t.Fatalf("NewEMA() returned error: %v", err)
	}

	values := indicator.Evaluate(ema, candles[:period])
	if len(values) != 0 {
		t.Errorf("EMA(%d) emitted %d values from exactly %d candles; the seed is not "+
			"converged yet and must not escape", period, len(values), period)
	}
	if ema.Ready() {
		t.Error("Ready() is true after only `period` candles")
	}
}

// lastEmittedValue feeds a slice and returns the final value the indicator
// actually emitted.
func lastEmittedValue(t *testing.T, period int, candles []models.Candle) float64 {
	t.Helper()

	ema, err := _indicator_us.NewEMA(period)
	if err != nil {
		t.Fatalf("NewEMA() returned error: %v", err)
	}

	values := indicator.Evaluate(ema, candles)
	if len(values) == 0 {
		t.Fatalf("EMA(%d) emitted nothing from %d candles", period, len(candles))
	}
	return values[len(values)-1].Value
}

// ---------------------------------------------------------------------------
// Incremental and batch identity
// ---------------------------------------------------------------------------

// TestIncrementalAndBatchAreIdentical is the guarantee that live and backtest
// cannot drift: there is one implementation and batch evaluation is a loop
// over it. Values must be bit-identical, not merely close.
func TestIncrementalAndBatchAreIdentical(t *testing.T) {
	candles := loadReferenceCandles(t)
	if len(candles) < 5000 {
		t.Fatalf("fixture has %d candles, want at least 5000", len(candles))
	}

	build := map[string]func() indicator.Indicator{
		"ema_200": func() indicator.Indicator { ind, _ := _indicator_us.NewEMA(200); return ind },
		"rsi_14":  func() indicator.Indicator { ind, _ := _indicator_us.NewRSI(14); return ind },
		"atr_14":  func() indicator.Indicator { ind, _ := _indicator_us.NewATR(14); return ind },
		"vwap":    func() indicator.Indicator { return _indicator_us.NewVWAP() },
	}

	for name, newIndicator := range build {
		t.Run(name, func(t *testing.T) {
			batch := indicator.Evaluate(newIndicator(), candles)

			// The same series, fed one candle at a time to a fresh instance.
			manual := make([]indicator.Value, 0, len(candles))
			incremental := newIndicator()
			for _, c := range candles {
				value, ok := incremental.Update(c)
				if !ok {
					continue
				}
				manual = append(manual, indicator.Value{OpenTime: c.OpenTime, Value: value})
			}

			if len(batch) != len(manual) {
				t.Fatalf("batch emitted %d values, incremental %d", len(batch), len(manual))
			}
			for i := range batch {
				if !batch[i].OpenTime.Equal(manual[i].OpenTime) {
					t.Fatalf("value %d: open times differ", i)
				}
				// Bit-identical. Any divergence means state is leaking.
				if math.Float64bits(batch[i].Value) != math.Float64bits(manual[i].Value) {
					t.Fatalf("value %d at %s: batch %.17g, incremental %.17g",
						i, batch[i].OpenTime, batch[i].Value, manual[i].Value)
				}
			}
			t.Logf("%d values identical", len(batch))
		})
	}
}

// ---------------------------------------------------------------------------
// Reset
// ---------------------------------------------------------------------------

// TestResetReturnsToFreshState feeds an instance, resets it, feeds the same
// candles again, and compares against an instance that never saw the first
// pass.
func TestResetReturnsToFreshState(t *testing.T) {
	candles := loadReferenceCandles(t)[:1500]

	build := map[string]func() indicator.Indicator{
		"ema_200": func() indicator.Indicator { ind, _ := _indicator_us.NewEMA(200); return ind },
		"rsi_14":  func() indicator.Indicator { ind, _ := _indicator_us.NewRSI(14); return ind },
		"atr_14":  func() indicator.Indicator { ind, _ := _indicator_us.NewATR(14); return ind },
		"vwap":    func() indicator.Indicator { return _indicator_us.NewVWAP() },
	}

	for name, newIndicator := range build {
		t.Run(name, func(t *testing.T) {
			reused := newIndicator()
			indicator.Evaluate(reused, candles[:100])
			reused.Reset()

			if reused.Ready() {
				t.Error("Ready() is true immediately after Reset()")
			}

			afterReset := indicator.Evaluate(reused, candles)
			fresh := indicator.Evaluate(newIndicator(), candles)

			if len(afterReset) != len(fresh) {
				t.Fatalf("after reset emitted %d values, fresh %d", len(afterReset), len(fresh))
			}
			for i := range fresh {
				if math.Float64bits(afterReset[i].Value) != math.Float64bits(fresh[i].Value) {
					t.Fatalf("value %d at %s: reset instance %.17g, fresh %.17g",
						i, fresh[i].OpenTime, afterReset[i].Value, fresh[i].Value)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// RSI degenerate cases
// ---------------------------------------------------------------------------

func TestRSIDegenerateCases(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	const period = 14

	rsi, err := _indicator_us.NewRSI(period)
	if err != nil {
		t.Fatalf("NewRSI() returned error: %v", err)
	}
	length := rsi.WarmupPeriod() + 10

	tests := []struct {
		name   string
		closes func() []float64
		want   float64
		reason string
	}{
		{
			name: "all gains",
			closes: func() []float64 {
				out := make([]float64, length)
				for i := range out {
					out[i] = 64000 + float64(i)*10
				}
				return out
			},
			want:   100,
			reason: "average loss is zero, so relative strength is infinite and RSI saturates rather than dividing by zero",
		},
		{
			name: "all losses",
			closes: func() []float64 {
				out := make([]float64, length)
				for i := range out {
					out[i] = 64000 - float64(i)*10
				}
				return out
			},
			want:   0,
			reason: "average gain is zero",
		},
		{
			name: "perfectly flat",
			closes: func() []float64 {
				out := make([]float64, length)
				for i := range out {
					out[i] = 64000
				}
				return out
			},
			want:   50,
			reason: "a market that never moved is neutral by convention, not 0/0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ind, err := _indicator_us.NewRSI(period)
			if err != nil {
				t.Fatalf("NewRSI() returned error: %v", err)
			}

			values := indicator.Evaluate(ind, closeSeries(start, tt.closes()))
			if len(values) == 0 {
				t.Fatal("RSI emitted no values")
			}

			got := values[len(values)-1].Value
			if math.Abs(got-tt.want) > 1e-9 {
				t.Errorf("RSI = %g, want %g (%s)", got, tt.want, tt.reason)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// VWAP session boundary
// ---------------------------------------------------------------------------

// TestVWAPResetsAtUTCMidnight covers the boundary directly, including a series
// that spans it.
func TestVWAPResetsAtUTCMidnight(t *testing.T) {
	// Two bars before midnight and two after.
	lastDay := time.Date(2026, 1, 1, 23, 58, 0, 0, time.UTC)

	candles := []models.Candle{
		candleAt(lastDay, 100, 110, 90, 100, 10),                     // typical 100
		candleAt(lastDay.Add(time.Minute), 200, 220, 180, 200, 10),   // typical 200
		candleAt(lastDay.Add(2*time.Minute), 300, 330, 270, 300, 10), // 00:00 next day
		candleAt(lastDay.Add(3*time.Minute), 400, 440, 360, 400, 10), // 00:01
	}

	values := indicator.Evaluate(_indicator_us.NewVWAP(), candles)
	if len(values) != 4 {
		t.Fatalf("VWAP emitted %d values, want 4", len(values))
	}

	want := []float64{
		100, // first bar of 1 Jan session
		150, // (100*10 + 200*10) / 20
		300, // session reset: only the 00:00 bar counts
		350, // (300*10 + 400*10) / 20
	}
	for i, expected := range want {
		if math.Abs(values[i].Value-expected) > 1e-9 {
			t.Errorf("value %d = %g, want %g", i, values[i].Value, expected)
		}
	}

	// The reset must be driven by the candle's open_time, so the third bar
	// starting a fresh session is what proves it rather than any wall clock.
	if !values[2].OpenTime.Equal(time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("value 2 is at %s, want the first bar of 2 Jan", values[2].OpenTime)
	}
}

// TestVWAPResetIsDrivenByCandleTimeNotWallClock replays a series from years
// ago and asserts the sessions land where that history says they should.
func TestVWAPResetIsDrivenByCandleTimeNotWallClock(t *testing.T) {
	// Deliberately in the past: a wall-clock implementation would put every
	// bar in today's session and never reset.
	start := time.Date(2023, 3, 14, 23, 59, 0, 0, time.UTC)

	candles := []models.Candle{
		candleAt(start, 100, 100, 100, 100, 5),
		candleAt(start.Add(time.Minute), 500, 500, 500, 500, 5), // crosses midnight
	}

	values := indicator.Evaluate(_indicator_us.NewVWAP(), candles)
	if len(values) != 2 {
		t.Fatalf("VWAP emitted %d values, want 2", len(values))
	}
	if math.Abs(values[1].Value-500) > 1e-9 {
		t.Errorf("second value = %g, want 500: the session did not reset at the 2023 midnight",
			values[1].Value)
	}
}

// TestVWAPHandlesZeroVolumeSession covers a stretch of bars with no trades,
// which would otherwise divide by zero.
func TestVWAPHandlesZeroVolumeSession(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	candles := []models.Candle{
		candleAt(start, 100, 120, 80, 100, 0),
		candleAt(start.Add(time.Minute), 200, 220, 180, 200, 0),
	}

	values := indicator.Evaluate(_indicator_us.NewVWAP(), candles)
	if len(values) != 2 {
		t.Fatalf("VWAP emitted %d values, want 2", len(values))
	}
	for i, value := range values {
		if math.IsNaN(value.Value) || math.IsInf(value.Value, 0) {
			t.Errorf("value %d is %g on a zero-volume session", i, value.Value)
		}
	}
	// With no volume the typical price is the honest stand-in.
	if math.Abs(values[1].Value-200) > 1e-9 {
		t.Errorf("value 1 = %g, want the typical price 200", values[1].Value)
	}
}

// ---------------------------------------------------------------------------
// Set
// ---------------------------------------------------------------------------

// TestSetWithholdsPartialSnapshots is the rule that keeps a half-warmed
// snapshot out of a strategy.
func TestSetWithholdsPartialSnapshots(t *testing.T) {
	candles := loadReferenceCandles(t)

	set, err := _indicator_us.NewSet(_indicator_us.DefaultSetConfig())
	if err != nil {
		t.Fatalf("NewSet() returned error: %v", err)
	}

	warmup := set.WarmupPeriod()
	if want := 200 * constants.WarmupMultiplier; warmup != want {
		t.Fatalf("WarmupPeriod() = %d, want %d (the longest member)", warmup, want)
	}

	emitted := 0
	for i, c := range candles {
		snapshot, ok := set.Update(c)
		consumed := i + 1

		if consumed < warmup {
			if ok {
				t.Fatalf("candle %d of %d: a partial snapshot escaped", consumed, warmup)
			}
			if set.Ready() {
				t.Fatalf("candle %d: Ready() is true while a member is still warming", consumed)
			}
			continue
		}

		if !ok {
			t.Fatalf("candle %d: no snapshot after warm-up", consumed)
		}
		emitted++

		for name, value := range map[string]float64{
			"ema": snapshot.EMA, "rsi": snapshot.RSI, "atr": snapshot.ATR, "vwap": snapshot.VWAP,
		} {
			if math.IsNaN(value) {
				t.Fatalf("candle %d: snapshot carries NaN for %s", consumed, name)
			}
		}
		if !snapshot.OpenTime.Equal(c.OpenTime) {
			t.Fatalf("candle %d: snapshot open time %s, want %s", consumed, snapshot.OpenTime, c.OpenTime)
		}
	}

	if emitted != len(candles)-warmup+1 {
		t.Errorf("emitted %d snapshots, want %d", emitted, len(candles)-warmup+1)
	}
}

// TestSetMembersStayInStepDuringWarmup guards a subtle bug: every member must
// be updated on every candle even while the set is withholding output, or a
// member skipped during warm-up would be permanently behind the others.
func TestSetMembersStayInStepDuringWarmup(t *testing.T) {
	candles := loadReferenceCandles(t)

	set, err := _indicator_us.NewSet(_indicator_us.DefaultSetConfig())
	if err != nil {
		t.Fatalf("NewSet() returned error: %v", err)
	}
	snapshots := _indicator_us.EvaluateSet(set, candles)
	if len(snapshots) == 0 {
		t.Fatal("the set emitted no snapshots")
	}

	// Each member run standalone over the same series must agree exactly with
	// what the set reported.
	ema, _ := _indicator_us.NewEMA(200)
	rsi, _ := _indicator_us.NewRSI(14)
	atr, _ := _indicator_us.NewATR(14)

	standalone := map[string][]indicator.Value{
		"ema":  indicator.Evaluate(ema, candles),
		"rsi":  indicator.Evaluate(rsi, candles),
		"atr":  indicator.Evaluate(atr, candles),
		"vwap": indicator.Evaluate(_indicator_us.NewVWAP(), candles),
	}

	last := snapshots[len(snapshots)-1]
	for name, values := range standalone {
		want := values[len(values)-1]
		if !want.OpenTime.Equal(last.OpenTime) {
			continue // different warm-up lengths; compare by time below
		}

		var got float64
		switch name {
		case "ema":
			got = last.EMA
		case "rsi":
			got = last.RSI
		case "atr":
			got = last.ATR
		case "vwap":
			got = last.VWAP
		}
		if math.Float64bits(got) != math.Float64bits(want.Value) {
			t.Errorf("%s in the set is %.17g, standalone %.17g", name, got, want.Value)
		}
	}
}

func TestMaxWarmupPeriod(t *testing.T) {
	ema, _ := _indicator_us.NewEMA(200)
	rsi, _ := _indicator_us.NewRSI(14)

	got := indicator.MaxWarmupPeriod(ema, rsi, _indicator_us.NewVWAP(), nil)
	if want := 200 * constants.WarmupMultiplier; got != want {
		t.Errorf("MaxWarmupPeriod() = %d, want %d", got, want)
	}
	if indicator.MaxWarmupPeriod() != 0 {
		t.Error("MaxWarmupPeriod() with no indicators should be 0")
	}
}
