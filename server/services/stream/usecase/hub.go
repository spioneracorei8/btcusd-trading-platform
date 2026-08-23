package usecase

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/spioneracorei8/btcusd-trading-platform/server/services/stream"
)

// Hub fans events out to subscribers.
//
// # Backpressure
//
// A phone on a mobile network stalls constantly. Each subscriber has a bounded
// queue and a send that would block is dropped instead — with the drop counted
// and reported to that subscriber, so it knows its view has a hole rather than
// believing it saw everything.
//
// Buffering without limit is the alternative and it is worse: one stalled
// client would grow the server's memory until something died, and the thing
// that died would not be the client.
type Hub struct {
	log *slog.Logger

	mu          sync.RWMutex
	subscribers map[int64]*Subscriber
	nextId      atomic.Int64

	// sequence numbers each topic independently, so a client's cursor for
	// candles is not disturbed by a signal arriving.
	seqMu    sync.Mutex
	sequence map[stream.Topic]int64
}

// NewHub builds an empty hub.
func NewHub(log *slog.Logger) *Hub {
	return &Hub{
		log:         log,
		subscribers: map[int64]*Subscriber{},
		sequence:    map[stream.Topic]int64{},
	}
}

// Subscriber is one connected client.
type Subscriber struct {
	id     int64
	topics map[stream.Topic]bool

	// events is bounded. A full queue means this client is not keeping up.
	events chan stream.Event

	// dropped counts events this subscriber missed. It is sent to the client
	// so a gap is visible rather than silent.
	dropped atomic.Int64
}

// Events is what the connection writes out.
func (s *Subscriber) Events() <-chan stream.Event { return s.events }

// Dropped is how many events this subscriber has missed, and resets the count.
func (s *Subscriber) Dropped() int64 { return s.dropped.Swap(0) }

// Subscribe registers a client for a set of topics.
func (h *Hub) Subscribe(topics []stream.Topic, queue int) *Subscriber {
	wanted := make(map[stream.Topic]bool, len(topics))
	for _, t := range topics {
		wanted[t] = true
	}

	sub := &Subscriber{
		id:     h.nextId.Add(1),
		topics: wanted,
		events: make(chan stream.Event, queue),
	}

	h.mu.Lock()
	h.subscribers[sub.id] = sub
	h.mu.Unlock()
	return sub
}

// Unsubscribe removes a client and closes its queue.
func (h *Hub) Unsubscribe(sub *Subscriber) {
	h.mu.Lock()
	delete(h.subscribers, sub.id)
	h.mu.Unlock()
	close(sub.events)
}

// Publish fans one event out, stamping it with the topic's next sequence.
//
// It never blocks. A subscriber whose queue is full has the event dropped and
// counted; the publisher — which is the market stream or a poller — must not
// be held up by the slowest phone on the network.
func (h *Hub) Publish(event stream.Event) {
	h.seqMu.Lock()
	h.sequence[event.Topic]++
	event.Sequence = h.sequence[event.Topic]
	h.seqMu.Unlock()

	h.mu.RLock()
	defer h.mu.RUnlock()

	for _, sub := range h.subscribers {
		if !sub.topics[event.Topic] {
			continue
		}
		select {
		case sub.events <- event:
		default:
			sub.dropped.Add(1)
		}
	}
}

// Sequence is the latest sequence issued for a topic, for a client asking
// what it is behind.
func (h *Hub) Sequence(topic stream.Topic) int64 {
	h.seqMu.Lock()
	defer h.seqMu.Unlock()
	return h.sequence[topic]
}

// Subscribers is how many clients are connected, for the status topic.
func (h *Hub) Subscribers() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.subscribers)
}

// Run drives every source until ctx is cancelled.
func (h *Hub) Run(ctx context.Context, sources ...stream.Source) {
	var wg sync.WaitGroup
	for _, source := range sources {
		wg.Add(1)
		go func(s stream.Source) {
			defer wg.Done()
			if err := s.Run(ctx, h.Publish); err != nil && ctx.Err() == nil {
				h.log.ErrorContext(ctx, "a stream source stopped", "error", err)
			}
		}(source)
	}
	wg.Wait()
}
