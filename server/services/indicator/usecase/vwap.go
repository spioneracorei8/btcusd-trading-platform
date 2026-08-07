package usecase

import (
	"time"

	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/indicator"
)

// VWAP is the volume weighted average price, reset daily at 00:00 UTC.
//
// VWAP has no single definition, so this one is pinned down deliberately and
// recorded in docs/decisions/0008-vwap-definition.md:
//
//   - The session boundary is UTC midnight. Crypto has no trading session, and
//     UTC midnight is what charting tools use for it.
//   - The price is the typical price, (high+low+close)/3, not the close.
//   - The weight is base volume, so the value is sum(typical*volume)/sum(volume).
//
// The reset is driven by the candle's open_time, never by wall-clock time. A
// backtest replaying 2023 has to produce the 2023 resets, and reading the
// clock would make the same input produce different output on a different day.
type VWAP struct {
	// day identifies the UTC session currently accumulating.
	day    time.Time
	hasDay bool

	cumulativePV     float64
	cumulativeVolume float64

	value    float64
	hasValue bool
	count    int
}

// NewVWAP builds a daily-reset VWAP.
func NewVWAP() *VWAP { return &VWAP{} }

// Update feeds one closed candle.
func (v *VWAP) Update(c models.Candle) (float64, bool) {
	v.count++

	day := utcDay(c.OpenTime)
	if !v.hasDay || !day.Equal(v.day) {
		// A new UTC session starts from nothing; carrying yesterday's volume
		// forward would make the first hours of every day meaningless.
		v.day = day
		v.hasDay = true
		v.cumulativePV = 0
		v.cumulativeVolume = 0
	}

	typical := (highFloat(c) + lowFloat(c) + closeFloat(c)) / 3
	volume := volumeFloat(c)

	v.cumulativePV += typical * volume
	v.cumulativeVolume += volume

	if v.cumulativeVolume == 0 {
		// A session of entirely empty bars has no traded price to average.
		// The typical price is the honest stand-in and keeps the series
		// continuous.
		v.value = typical
	} else {
		v.value = v.cumulativePV / v.cumulativeVolume
	}
	v.hasValue = true

	return v.value, true
}

// WarmupPeriod is one candle.
//
// Unlike the smoothed indicators, VWAP has no memory to converge: it is exact
// for the bars it has seen. It is worth knowing that the first bars of a UTC
// session average a very small sample and are correspondingly jumpy, but that
// is a property of the definition rather than an unconverged state.
func (v *VWAP) WarmupPeriod() int { return 1 }

// Ready reports whether any candle has been consumed.
func (v *VWAP) Ready() bool { return v.count >= v.WarmupPeriod() }

// Reset returns the instance to its freshly constructed state.
func (v *VWAP) Reset() {
	v.day = time.Time{}
	v.hasDay = false
	v.cumulativePV = 0
	v.cumulativeVolume = 0
	v.value = 0
	v.hasValue = false
	v.count = 0
}

// Name identifies the indicator.
func (v *VWAP) Name() string { return "vwap_daily_utc" }

// utcDay truncates an instant to the start of its UTC day.
func utcDay(t time.Time) time.Time {
	utc := t.UTC()
	return time.Date(utc.Year(), utc.Month(), utc.Day(), 0, 0, 0, 0, time.UTC)
}

// Compile-time proof that VWAP satisfies the contract; the others are checked
// in set.go where they are held as interface values.
var _ indicator.Indicator = (*VWAP)(nil)
