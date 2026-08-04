package binance

import (
	"testing"
	"time"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
)

// newFixedBackoff returns a backoff whose jitter is pinned, so the schedule
// itself can be asserted rather than a range.
func newFixedBackoff(random float64) *Backoff {
	return &Backoff{randFloat: func() float64 { return random }}
}

// TestBackoffDoublesAndCaps covers the schedule the spec fixes: 1s doubling
// to a 60s ceiling.
func TestBackoffDoublesAndCaps(t *testing.T) {
	// random = 0.5 puts the jitter exactly at the midpoint, i.e. no offset.
	b := newFixedBackoff(0.5)

	want := []time.Duration{
		1 * time.Second,
		2 * time.Second,
		4 * time.Second,
		8 * time.Second,
		16 * time.Second,
		32 * time.Second,
		60 * time.Second, // capped, not 64
		60 * time.Second,
		60 * time.Second,
	}

	for i, expected := range want {
		got := b.Next()
		if got != expected {
			t.Errorf("attempt %d: delay = %s, want %s", i+1, got, expected)
		}
	}
}

// TestBackoffIsMonotonicUntilCap states the property the spec asks for
// directly: each delay is at least as long as the one before, up to the cap.
func TestBackoffIsMonotonicUntilCap(t *testing.T) {
	b := newFixedBackoff(0.5)

	var previous time.Duration
	for i := range 12 {
		got := b.Next()
		if got < previous {
			t.Errorf("attempt %d: delay %s is shorter than the previous %s", i+1, got, previous)
		}
		if got > constants.BackoffMax {
			t.Errorf("attempt %d: delay %s exceeds the %s cap", i+1, got, constants.BackoffMax)
		}
		previous = got
	}
}

// TestBackoffJitterStaysWithinBand checks the ±20% envelope at both extremes
// of the random draw.
func TestBackoffJitterStaysWithinBand(t *testing.T) {
	for _, tt := range []struct {
		name   string
		random float64
		want   time.Duration // for the first attempt, base 1s
	}{
		{name: "lowest draw", random: 0, want: 800 * time.Millisecond},
		{name: "highest draw", random: 1, want: 1200 * time.Millisecond},
	} {
		t.Run(tt.name, func(t *testing.T) {
			b := newFixedBackoff(tt.random)
			if got := b.Next(); got != tt.want {
				t.Errorf("delay = %s, want %s", got, tt.want)
			}
		})
	}
}

// TestBackoffJitterIsAppliedAtTheCap guards the ceiling: even capped delays
// must be jittered, or a fleet that has been retrying for an hour ends up
// perfectly synchronised on a 60s beat.
func TestBackoffJitterIsAppliedAtTheCap(t *testing.T) {
	low := newFixedBackoff(0)
	high := newFixedBackoff(1)
	for range 10 {
		low.Next()
		high.Next()
	}

	lowDelay := low.Next()
	highDelay := high.Next()

	if lowDelay == highDelay {
		t.Fatalf("capped delays are identical (%s) regardless of the random draw", lowDelay)
	}
	if lowDelay != 48*time.Second {
		t.Errorf("lowest capped delay = %s, want 48s", lowDelay)
	}
	if highDelay != 72*time.Second {
		t.Errorf("highest capped delay = %s, want 72s", highDelay)
	}
}

// TestBackoffResetReturnsToTheStart covers the case that matters after a
// connection survives a while: it must not inherit the previous outage's
// delay.
func TestBackoffResetReturnsToTheStart(t *testing.T) {
	b := newFixedBackoff(0.5)
	for range 5 {
		b.Next()
	}
	if b.Attempt() != 5 {
		t.Fatalf("Attempt() = %d, want 5", b.Attempt())
	}

	b.Reset()
	if b.Attempt() != 0 {
		t.Errorf("Attempt() = %d after Reset, want 0", b.Attempt())
	}
	if got := b.Next(); got != time.Second {
		t.Errorf("delay after Reset = %s, want 1s", got)
	}
}

// TestBackoffDefaultRandomStaysInBand exercises the real random source.
func TestBackoffDefaultRandomStaysInBand(t *testing.T) {
	b := NewBackoff()

	for i := range 20 {
		got := b.Next()
		if got < 0 {
			t.Fatalf("attempt %d produced a negative delay %s", i+1, got)
		}
		// The cap plus its jitter is the true ceiling.
		if max := time.Duration(float64(constants.BackoffMax) * (1 + constants.BackoffJitter)); got > max {
			t.Errorf("attempt %d: delay %s exceeds %s", i+1, got, max)
		}
	}
}
