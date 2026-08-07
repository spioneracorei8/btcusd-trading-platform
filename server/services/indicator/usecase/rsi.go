package usecase

import (
	"fmt"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/indicator"
)

// RSI is Wilder's relative strength index.
//
// This is the Wilder-smoothed variant, not the SMA-based one. The two are
// routinely confused and produce visibly different numbers — on a sample
// series they differ by nearly 3 points — so a strategy tuned against one and
// run against the other is not the same strategy. TA-Lib, which the fixtures
// come from, is also Wilder.
type RSI struct {
	period int

	previousClose float64
	hasPrevious   bool

	// Seed sums of the first `period` deltas, per Wilder's initial average.
	seedGain float64
	seedLoss float64

	averageGain float64
	averageLoss float64

	// deltas counts price changes seen, which is one fewer than candles.
	deltas int
	count  int
}

// NewRSI builds an RSI over the given period.
func NewRSI(period int) (*RSI, error) {
	if period < 2 {
		return nil, fmt.Errorf("rsi period %d is below the minimum of 2", period)
	}
	return &RSI{period: period}, nil
}

// Update feeds one closed candle.
func (r *RSI) Update(c models.Candle) (float64, bool) {
	close := closeFloat(c)
	r.count++

	if !r.hasPrevious {
		r.previousClose = close
		r.hasPrevious = true
		return indicator.NotReady, false
	}

	gain, loss := 0.0, 0.0
	if change := close - r.previousClose; change > 0 {
		gain = change
	} else {
		loss = -change
	}
	r.previousClose = close
	r.deltas++

	switch {
	case r.deltas < r.period:
		r.seedGain += gain
		r.seedLoss += loss
	case r.deltas == r.period:
		r.seedGain += gain
		r.seedLoss += loss
		r.averageGain = r.seedGain / float64(r.period)
		r.averageLoss = r.seedLoss / float64(r.period)
	default:
		// Wilder's smoothing: the previous average decays by (p-1)/p.
		r.averageGain = (r.averageGain*float64(r.period-1) + gain) / float64(r.period)
		r.averageLoss = (r.averageLoss*float64(r.period-1) + loss) / float64(r.period)
	}

	if !r.Ready() {
		return indicator.NotReady, false
	}
	return r.currentValue(), true
}

// currentValue applies the RSI formula, including its two degenerate cases.
func (r *RSI) currentValue() float64 {
	// A series that never moved has no strength either way. Returning 50 by
	// convention beats 0/0: a flat market is neutral, not maximally weak.
	if r.averageLoss == 0 && r.averageGain == 0 {
		return 50
	}
	// Only gains: relative strength is infinite, so RSI saturates rather
	// than dividing by zero.
	if r.averageLoss == 0 {
		return 100
	}
	if r.averageGain == 0 {
		return 0
	}

	strength := r.averageGain / r.averageLoss
	return 100 - 100/(1+strength)
}

// WarmupPeriod is 5x the period: Wilder's smoothing has the same infinite
// memory as an EMA and converges just as slowly.
func (r *RSI) WarmupPeriod() int { return r.period * constants.WarmupMultiplier }

// Ready reports whether the warm-up window has passed.
func (r *RSI) Ready() bool { return r.count >= r.WarmupPeriod() }

// Reset returns the instance to its freshly constructed state.
func (r *RSI) Reset() {
	r.previousClose = 0
	r.hasPrevious = false
	r.seedGain = 0
	r.seedLoss = 0
	r.averageGain = 0
	r.averageLoss = 0
	r.deltas = 0
	r.count = 0
}

// Name identifies the indicator and its parameters.
func (r *RSI) Name() string { return fmt.Sprintf("rsi_%d", r.period) }
