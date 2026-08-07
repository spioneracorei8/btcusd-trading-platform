// Package indicator declares the contract every technical indicator obeys.
//
// Indicators are pure computation: no I/O, nothing stored. Computed values
// are deliberately never persisted — a stored EMA(200) whose warm-up rules
// later change becomes stale data with no way to detect it, and recomputing
// from candles is cheap.
package indicator

import (
	"math"
	"time"

	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
)

// NotReady is what Update returns for a value that does not exist yet.
//
// It is NaN rather than zero on purpose. A caller that ignores ok and uses the
// value gets NaN, which propagates loudly through any arithmetic and shows up
// immediately; a zero would flow silently into a strategy and become a phantom
// signal indistinguishable from a real one.
var NotReady = math.NaN()

// Indicator is one stateful calculation fed a candle at a time.
//
// # Closed candles only
//
// Update accepts closed candles. It cannot verify this — models.Candle carries
// IsClosed but an indicator has no way to reject a caller that lies about the
// pipeline it came from — so the contract lives here and callers enforce it.
// Feeding a forming bar makes every value flicker as the bar changes, and a
// backtest fed the same series would then disagree with what live produced.
//
// # Not goroutine-safe
//
// One instance per (symbol, timeframe, params). The ingestion pipeline is
// single-writer by design, so there are deliberately no mutexes: adding them
// would hide a wiring mistake rather than fix it.
type Indicator interface {
	// Update feeds exactly one closed candle and returns the current value.
	// ok is false while the indicator is still warming up, and the value is
	// then NotReady.
	Update(c models.Candle) (value float64, ok bool)

	// WarmupPeriod is how many candles must be consumed before Update
	// returns ok=true.
	WarmupPeriod() int

	// Ready reports whether enough candles have been consumed.
	Ready() bool

	// Reset clears all internal state, returning the instance to exactly the
	// condition of a freshly constructed one.
	Reset()

	// Name is a stable identifier including parameters, e.g. "ema_200".
	Name() string
}

// Value is one emitted indicator value, tied to the candle that produced it.
type Value struct {
	OpenTime time.Time
	Value    float64
}

// Snapshot is one row of indicator values at a candle close.
//
// It is a plain value type with no pointers and no behaviour: it is what
// phases 04 and 06 consume, and a shared map or slice inside it would let a
// later mutation rewrite a snapshot somebody already took.
type Snapshot struct {
	OpenTime time.Time

	EMA  float64
	RSI  float64
	ATR  float64
	VWAP float64
}

// MaxWarmupPeriod reports the longest warm-up across a set of indicators.
//
// Downstream code refuses to evaluate a bar where any required indicator is
// not ready, so this is the number of candles that must precede the first
// bar a backtest may score.
func MaxWarmupPeriod(indicators ...Indicator) int {
	longest := 0
	for _, ind := range indicators {
		if ind == nil {
			continue
		}
		if period := ind.WarmupPeriod(); period > longest {
			longest = period
		}
	}
	return longest
}

// Evaluate runs an indicator over a series and returns the values it emitted.
//
// It is a loop over Update and nothing else. There is exactly one
// implementation of every indicator and it is the incremental one, because
// the moment batch evaluation gets its own code path the two drift and the
// backtest stops describing what live would have done.
func Evaluate(ind Indicator, candles []models.Candle) []Value {
	values := make([]Value, 0, len(candles))

	for _, c := range candles {
		value, ok := ind.Update(c)
		if !ok {
			continue
		}
		values = append(values, Value{OpenTime: c.OpenTime, Value: value})
	}
	return values
}
