// Package stream declares the live push contract.
//
// # The forming candle
//
// This is the only place in the system where a candle that has not closed may
// be sent anywhere, and CLAUDE.md §3.1 is the rule most likely to be broken
// here by accident. Two things make it safe:
//
//   - every candle on the wire carries is_closed, and a forming one carries
//     false. A client that ignores the flag is charting a price that can still
//     change, which is legitimate for display and nothing else.
//   - nothing on the server computes from it. The forming bar is held in
//     memory, sent, and dropped when the closed version of the same bar
//     arrives. It is never stored, never fed to an indicator, and never
//     reaches a strategy.
//
// The second is the one that matters, because it is the one a future change
// could quietly break. TestNothingComputesFromAFormingCandle in the collector
// and the architecture test are what hold it.
package stream

import (
	"context"
	"net/http"
	"time"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
)

// Topic is one kind of thing a client can subscribe to.
type Topic string

// The subscribable topics.
const (
	// TopicCandles carries both closed and forming bars, flagged.
	TopicCandles Topic = "candles"

	// TopicSignals carries signals as they are recorded.
	TopicSignals Topic = "signals"

	// TopicOutcomes carries outcomes as they resolve.
	TopicOutcomes Topic = "outcomes"

	// TopicStatus carries the pipeline status on a slow tick.
	TopicStatus Topic = "status"
)

// Valid reports whether t is a known topic.
func (t Topic) Valid() bool {
	switch t {
	case TopicCandles, TopicSignals, TopicOutcomes, TopicStatus:
		return true
	default:
		return false
	}
}

// String returns the wire representation of the topic.
func (t Topic) String() string { return string(t) }

// Topics is every topic, for an error message that lists them.
func Topics() []Topic {
	return []Topic{TopicCandles, TopicSignals, TopicOutcomes, TopicStatus}
}

// Event is one message pushed to a subscriber.
type Event struct {
	Topic Topic

	// At is the instant the event describes, not the instant it was sent.
	// It is what a reconnecting client sends back as its cursor.
	At time.Time

	// Sequence orders events within a topic. A client that reconnects with
	// the last one it saw gets what came after it, rather than the whole
	// history again.
	Sequence int64

	// Payload is the topic's own shape, already rendered.
	Payload any
}

// StreamHandler serves the websocket.
type StreamHandler interface {
	// Stream answers GET /api/v1/stream.
	Stream(w http.ResponseWriter, r *http.Request)
}

// Source produces events for the hub to fan out.
type Source interface {
	// Run pushes events into publish until ctx is cancelled.
	Run(ctx context.Context, publish func(Event)) error
}

// CandleSource is the live market feed, forming bars included.
//
// It is separate from the collector's: the api is a different process and
// cannot see the collector's memory, and the forming bar exists only there.
// This one is read-only in the strongest sense — it has no repository, so
// there is nothing it could write to even by mistake.
type CandleSource interface {
	// Watch calls onCandle for every kline the exchange sends, closed or
	// not, until ctx is cancelled.
	Watch(ctx context.Context, symbol string, marketType constants.MarketType,
		timeframes []constants.Timeframe, onCandle func(models.Candle)) error
}
