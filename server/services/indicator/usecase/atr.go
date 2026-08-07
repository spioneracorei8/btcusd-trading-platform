package usecase

import (
	"fmt"
	"math"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/indicator"
)

// ATR is Wilder's average true range, smoothed consistently with RSI.
type ATR struct {
	period int

	previousClose float64
	hasPrevious   bool

	seed   float64
	ranges int

	value float64
	count int
}

// NewATR builds an ATR over the given period.
func NewATR(period int) (*ATR, error) {
	if period < 2 {
		return nil, fmt.Errorf("atr period %d is below the minimum of 2", period)
	}
	return &ATR{period: period}, nil
}

// Update feeds one closed candle.
func (a *ATR) Update(c models.Candle) (float64, bool) {
	high, low, close := highFloat(c), lowFloat(c), closeFloat(c)
	a.count++

	if !a.hasPrevious {
		// The first bar has no previous close, so it has no true range —
		// only its own span, which is a different quantity and would drag the
		// seed. It establishes the reference close and contributes nothing
		// else. TA-Lib does the same, and the fixtures disagree by 0.06% if
		// this bar is averaged in.
		a.previousClose = close
		a.hasPrevious = true
		return indicator.NotReady, false
	}

	trueRange := a.trueRange(high, low)
	a.previousClose = close
	a.ranges++

	switch {
	case a.ranges < a.period:
		a.seed += trueRange
	case a.ranges == a.period:
		a.seed += trueRange
		a.value = a.seed / float64(a.period)
	default:
		a.value = (a.value*float64(a.period-1) + trueRange) / float64(a.period)
	}

	if !a.Ready() {
		return indicator.NotReady, false
	}
	return a.value, true
}

// trueRange is max(high-low, |high-prevClose|, |low-prevClose|).
//
// It is only called once a previous close exists; the caller handles the
// first bar.
func (a *ATR) trueRange(high, low float64) float64 {
	return math.Max(high-low, math.Max(
		math.Abs(high-a.previousClose),
		math.Abs(low-a.previousClose),
	))
}

// WarmupPeriod is 5x the period, for the same convergence reason as RSI.
func (a *ATR) WarmupPeriod() int { return a.period * constants.WarmupMultiplier }

// Ready reports whether the warm-up window has passed.
func (a *ATR) Ready() bool { return a.count >= a.WarmupPeriod() }

// Reset returns the instance to its freshly constructed state.
func (a *ATR) Reset() {
	a.previousClose = 0
	a.hasPrevious = false
	a.seed = 0
	a.ranges = 0
	a.value = 0
	a.count = 0
}

// Name identifies the indicator and its parameters.
func (a *ATR) Name() string { return fmt.Sprintf("atr_%d", a.period) }
