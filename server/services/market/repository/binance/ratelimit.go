package binance

import (
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
)

// weightTracker records the request budget Binance reports on every response
// and decides when to pause.
//
// The point is to stop *before* being told off. A 429 already costs a delay;
// repeated ones escalate to a 418 and an IP ban, which would take the
// collector off the air for hours and leave a gap in the data.
type weightTracker struct {
	mu sync.Mutex

	// used is the weight consumed in the current minute, as last reported.
	used int

	// blockedUntil is set when Binance returns 429 or 418. No request may be
	// sent before it passes.
	blockedUntil time.Time
}

func newWeightTracker() *weightTracker {
	return &weightTracker{}
}

// softLimit is the point at which the client starts pausing voluntarily.
func softLimit() int {
	return int(float64(constants.WeightLimitPerMinute) * constants.WeightSoftLimitRatio)
}

// observe records the headers of a successful response.
func (w *weightTracker) observe(header http.Header) {
	raw := header.Get(constants.UsedWeightHeader)
	if raw == "" {
		return
	}
	used, err := strconv.Atoi(raw)
	if err != nil {
		return
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	w.used = used
}

// block records a rate-limited response and how long to stay quiet.
func (w *weightTracker) block(now time.Time, retryAfter time.Duration) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.blockedUntil = now.Add(retryAfter)
}

// waitFor reports how long the caller must sleep before its next request.
//
// A hard block is honoured in full. Otherwise, once the used weight crosses
// the soft limit the client waits out the remainder of the current minute,
// which is when Binance's counter resets.
func (w *weightTracker) waitFor(now time.Time) time.Duration {
	w.mu.Lock()
	defer w.mu.Unlock()

	if now.Before(w.blockedUntil) {
		return w.blockedUntil.Sub(now)
	}
	if w.used >= softLimit() {
		return timeUntilNextMinute(now)
	}
	return 0
}

// timeUntilNextMinute is when Binance's per-minute counter rolls over.
func timeUntilNextMinute(now time.Time) time.Duration {
	next := now.Truncate(time.Minute).Add(time.Minute)
	return next.Sub(now)
}

// rateLimitError builds the error for a 429 or 418 response and reports how
// long to back off.
//
// Binance sends Retry-After on these; when it does not, a fixed cooldown is
// used rather than retrying straight away. Neither status is ever treated as
// immediately retryable — that is precisely how an IP ban is earned.
func rateLimitError(status int, header http.Header) (time.Duration, error) {
	retryAfter := constants.RateLimitCooldown
	if raw := header.Get(constants.RetryAfterHeader); raw != "" {
		if seconds, err := strconv.Atoi(raw); err == nil && seconds > 0 {
			retryAfter = time.Duration(seconds) * time.Second
		}
	}

	return retryAfter, fmt.Errorf("%w: http %d, backing off for %s",
		constants.ErrRateLimited, status, retryAfter)
}

// isRateLimitStatus reports whether a status means "stop sending".
//
// 418 is Binance's "you ignored 429 and are now banned"; both must halt the
// caller.
func isRateLimitStatus(status int) bool {
	return status == http.StatusTooManyRequests || status == http.StatusTeapot
}
