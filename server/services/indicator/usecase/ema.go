package usecase

import (
	"fmt"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/indicator"
)

// EMA is an exponential moving average over the close price.
type EMA struct {
	period     int
	multiplier float64

	// seed accumulates the first `period` closes into an SMA. Seeding with a
	// mean rather than the first close matters: a single close is an
	// arbitrary starting point that the average then spends hundreds of bars
	// forgetting.
	seed  float64
	count int

	value  float64
	seeded bool
}

// NewEMA builds an EMA over the given period.
func NewEMA(period int) (*EMA, error) {
	if period < 2 {
		return nil, fmt.Errorf("ema period %d is below the minimum of 2", period)
	}
	return &EMA{
		period:     period,
		multiplier: 2.0 / (float64(period) + 1.0),
	}, nil
}

// Update feeds one closed candle.
func (e *EMA) Update(c models.Candle) (float64, bool) {
	close := closeFloat(c)
	e.count++

	switch {
	case e.count < e.period:
		e.seed += close
	case e.count == e.period:
		e.seed += close
		e.value = e.seed / float64(e.period)
		e.seeded = true
	default:
		e.value = (close-e.value)*e.multiplier + e.value
	}

	if !e.Ready() {
		return indicator.NotReady, false
	}
	return e.value, true
}

// WarmupPeriod is 5x the period, not the arithmetic minimum.
//
// An EMA never fully forgets its seed, so one fed exactly `period` candles
// still carries most of the arbitrary starting mean. Emitting from there would
// let a backtest score its earliest bars against unconverged numbers and call
// the result history. See docs/decisions/0007-indicator-warmup-multiplier.md.
func (e *EMA) WarmupPeriod() int { return e.period * constants.WarmupMultiplier }

// Ready reports whether the warm-up window has passed.
func (e *EMA) Ready() bool { return e.count >= e.WarmupPeriod() }

// Reset returns the instance to its freshly constructed state.
func (e *EMA) Reset() {
	e.seed = 0
	e.count = 0
	e.value = 0
	e.seeded = false
}

// Name identifies the indicator and its parameters.
func (e *EMA) Name() string { return fmt.Sprintf("ema_%d", e.period) }
