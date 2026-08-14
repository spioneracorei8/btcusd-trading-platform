package usecase

// ema is a strategy-local exponential moving average.
//
// # Why this is not services/indicator
//
// The engine supplies one indicator snapshot per bar at the configured
// periods. A strategy needing a different period — a 9 and a 21 where the
// engine runs a 200 — has to compute it, and computing it here from the same
// closed bars, bar by bar, is exactly what it would do live.
//
// The alternative would be for the engine to run every period any strategy
// might want, which couples the engine to its strategies and makes adding one
// a change to shared code.
//
// The arithmetic matches services/indicator/usecase/ema.go: SMA-seeded, then
// the standard recurrence. That file is the one with the reference tests
// against TA-Lib; this is the same formula at a strategy's own period.
type ema struct {
	period     int
	multiplier float64

	// seedSum accumulates the first `period` closes for the SMA seed. An EMA
	// seeded with its first value alone starts wrong and takes the whole
	// warm-up to stop being wrong.
	seedSum float64
	count   int

	value float64
}

func newEMA(period int) *ema {
	return &ema{
		period:     period,
		multiplier: 2.0 / (float64(period) + 1.0),
	}
}

// update feeds one closed bar's close and returns the current value. ok is
// false until the seed is complete.
func (e *ema) update(closePrice float64) (value float64, ok bool) {
	e.count++

	if e.count < e.period {
		e.seedSum += closePrice
		return 0, false
	}
	if e.count == e.period {
		e.seedSum += closePrice
		e.value = e.seedSum / float64(e.period)
		return e.value, true
	}

	e.value = (closePrice-e.value)*e.multiplier + e.value
	return e.value, true
}
