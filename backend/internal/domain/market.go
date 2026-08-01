// Package domain holds the core value types shared by every other package.
//
// It must not import any other package of this project: the dependency
// direction is cmd -> internal/* -> domain and never the other way around.
package domain

import (
	"fmt"
	"time"
)

// MarketType selects which Binance market a symbol is traded on.
//
// Futures logic (funding rate, leverage) is intentionally not implemented yet,
// but the type exists from day one so no endpoint or symbol format ends up
// hardcoded in calculation code.
type MarketType string

// Supported market types.
const (
	MarketTypeSpot    MarketType = "spot"
	MarketTypeFutures MarketType = "futures"
)

// Valid reports whether m is a known market type.
func (m MarketType) Valid() bool {
	switch m {
	case MarketTypeSpot, MarketTypeFutures:
		return true
	default:
		return false
	}
}

// String returns the wire/database representation of the market type.
func (m MarketType) String() string { return string(m) }

// ParseMarketType converts s into a MarketType, rejecting unknown values.
func ParseMarketType(s string) (MarketType, error) {
	m := MarketType(s)
	if !m.Valid() {
		return "", fmt.Errorf("unknown market type %q (want %q or %q)", s, MarketTypeSpot, MarketTypeFutures)
	}
	return m, nil
}

// Timeframe is a candle interval expressed with Binance's notation.
type Timeframe string

// Timeframes the platform is allowed to work with. 1m and 5m drive the
// scalping strategies; 15m and 1h are used as trend filters.
const (
	Timeframe1m  Timeframe = "1m"
	Timeframe3m  Timeframe = "3m"
	Timeframe5m  Timeframe = "5m"
	Timeframe15m Timeframe = "15m"
	Timeframe30m Timeframe = "30m"
	Timeframe1h  Timeframe = "1h"
	Timeframe2h  Timeframe = "2h"
	Timeframe4h  Timeframe = "4h"
	Timeframe6h  Timeframe = "6h"
	Timeframe12h Timeframe = "12h"
	Timeframe1d  Timeframe = "1d"
)

// timeframeDurations is the single source of truth for valid timeframes.
var timeframeDurations = map[Timeframe]time.Duration{
	Timeframe1m:  time.Minute,
	Timeframe3m:  3 * time.Minute,
	Timeframe5m:  5 * time.Minute,
	Timeframe15m: 15 * time.Minute,
	Timeframe30m: 30 * time.Minute,
	Timeframe1h:  time.Hour,
	Timeframe2h:  2 * time.Hour,
	Timeframe4h:  4 * time.Hour,
	Timeframe6h:  6 * time.Hour,
	Timeframe12h: 12 * time.Hour,
	Timeframe1d:  24 * time.Hour,
}

// Valid reports whether t is a supported timeframe.
func (t Timeframe) Valid() bool {
	_, ok := timeframeDurations[t]
	return ok
}

// Duration returns the wall-clock length of one candle of this timeframe.
// It returns 0 for an unknown timeframe.
func (t Timeframe) Duration() time.Duration { return timeframeDurations[t] }

// String returns the wire/database representation of the timeframe.
func (t Timeframe) String() string { return string(t) }

// ParseTimeframe converts s into a Timeframe, rejecting unknown values.
func ParseTimeframe(s string) (Timeframe, error) {
	tf := Timeframe(s)
	if !tf.Valid() {
		return "", fmt.Errorf("unsupported timeframe %q", s)
	}
	return tf, nil
}
