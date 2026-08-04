package binance

import (
	"math"
	"math/rand/v2"
	"time"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
)

// Backoff produces the delay before each reconnect attempt.
//
// Jitter matters more than it looks: after an exchange-side outage every
// client that was connected retries at once, and a fleet reconnecting in
// lockstep is how a recovering exchange gets knocked over again.
type Backoff struct {
	// attempt counts consecutive failures. Reset clears it.
	attempt int

	// randFloat returns a number in [0,1). Tests replace it to make the
	// jittered schedule deterministic.
	randFloat func() float64
}

// NewBackoff builds a backoff with the schedule from constants.
func NewBackoff() *Backoff {
	return &Backoff{randFloat: rand.Float64}
}

// Next returns the delay for the current attempt and advances the schedule.
//
// The delay doubles from BackoffInitial up to BackoffMax, then stays there:
// an outage lasting hours must not produce a delay measured in days.
func (b *Backoff) Next() time.Duration {
	base := float64(constants.BackoffInitial) * math.Pow(constants.BackoffFactor, float64(b.attempt))
	if base > float64(constants.BackoffMax) {
		base = float64(constants.BackoffMax)
	}
	b.attempt++

	// Jitter of ±BackoffJitter, applied around the base delay.
	spread := base * constants.BackoffJitter
	delay := base - spread + 2*spread*b.random()

	if delay < 0 {
		return 0
	}
	return time.Duration(delay)
}

// Reset clears the schedule after a successful connection, so a connection
// that survives an hour and then drops retries in one second rather than
// inheriting the previous outage's delay.
func (b *Backoff) Reset() {
	b.attempt = 0
}

// Attempt reports how many consecutive failures have been seen.
func (b *Backoff) Attempt() int { return b.attempt }

func (b *Backoff) random() float64 {
	if b.randFloat == nil {
		return rand.Float64()
	}
	return b.randFloat()
}
