package usecase

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spioneracorei8/btcusd-trading-platform/server/services/stream"
)

func quietHub() *Hub {
	return NewHub(slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// TestASubscriberThatNeverReadsCannotStallThePublisher.
//
// # What this prevents
//
// The publisher is the market feed. If Publish blocked on a full subscriber
// queue, one phone on a bad connection would stop the feed for everyone —
// including, before the sources were separated, the goroutine reading the
// exchange. The failure would look like the exchange going quiet.
//
// The test fills a queue of one and then publishes far more, from the calling
// goroutine, with no reader at all. A blocking implementation deadlocks here
// rather than failing an assertion, which is why the whole thing runs behind a
// timeout.
func TestASubscriberThatNeverReadsCannotStallThePublisher(t *testing.T) {
	hub := quietHub()
	sub := hub.Subscribe([]stream.Topic{stream.TopicCandles}, 1)
	defer hub.Unsubscribe(sub)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 1000; i++ {
			hub.Publish(stream.Event{Topic: stream.TopicCandles, At: time.Unix(int64(i), 0)})
		}
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Publish blocked on a subscriber that was not reading")
	}

	if got := sub.Dropped(); got != 999 {
		t.Fatalf("dropped = %d, want 999 (1000 published into a queue of 1)", got)
	}
	if got := hub.Sequence(stream.TopicCandles); got != 1000 {
		t.Fatalf("sequence = %d, want 1000: every event is numbered, dropped or not", got)
	}
}

// TestDroppedIsReportedOnceAndThenReset.
//
// The handler reads Dropped before each event and emits a gap message when it
// is non-zero. If reading did not reset it, one dropped event would produce a
// gap notice on every subsequent event forever.
func TestDroppedIsReportedOnceAndThenReset(t *testing.T) {
	hub := quietHub()
	sub := hub.Subscribe([]stream.Topic{stream.TopicSignals}, 1)
	defer hub.Unsubscribe(sub)

	for i := 0; i < 4; i++ {
		hub.Publish(stream.Event{Topic: stream.TopicSignals})
	}

	if got := sub.Dropped(); got != 3 {
		t.Fatalf("first read = %d, want 3", got)
	}
	if got := sub.Dropped(); got != 0 {
		t.Fatalf("second read = %d, want 0: the count must reset once reported", got)
	}
}

// TestSequencesAreCountedPerTopic.
//
// A client's candle cursor must not move because a signal was published. If
// the counter were global, a reconnecting client would be told it was behind
// on candles because something else happened while it was away.
func TestSequencesAreCountedPerTopic(t *testing.T) {
	hub := quietHub()

	hub.Publish(stream.Event{Topic: stream.TopicCandles})
	hub.Publish(stream.Event{Topic: stream.TopicSignals})
	hub.Publish(stream.Event{Topic: stream.TopicCandles})

	if got := hub.Sequence(stream.TopicCandles); got != 2 {
		t.Fatalf("candles sequence = %d, want 2", got)
	}
	if got := hub.Sequence(stream.TopicSignals); got != 1 {
		t.Fatalf("signals sequence = %d, want 1", got)
	}
	if got := hub.Sequence(stream.TopicOutcomes); got != 0 {
		t.Fatalf("outcomes sequence = %d, want 0", got)
	}
}

// TestASubscriberOnlyReceivesItsOwnTopics.
func TestASubscriberOnlyReceivesItsOwnTopics(t *testing.T) {
	hub := quietHub()
	sub := hub.Subscribe([]stream.Topic{stream.TopicOutcomes}, 4)
	defer hub.Unsubscribe(sub)

	hub.Publish(stream.Event{Topic: stream.TopicCandles})
	hub.Publish(stream.Event{Topic: stream.TopicOutcomes})
	hub.Publish(stream.Event{Topic: stream.TopicStatus})

	select {
	case event := <-sub.Events():
		if event.Topic != stream.TopicOutcomes {
			t.Fatalf("received %s, want outcomes", event.Topic)
		}
	default:
		t.Fatal("the subscribed topic was not delivered")
	}

	select {
	case event := <-sub.Events():
		t.Fatalf("received %s, which was not subscribed to", event.Topic)
	default:
	}

	if got := sub.Dropped(); got != 0 {
		t.Fatalf("dropped = %d: an unsubscribed topic is not a drop", got)
	}
}

// TestUnsubscribeClosesTheQueue, so the handler's read loop ends rather than
// waiting on a channel nothing will ever write to.
func TestUnsubscribeClosesTheQueue(t *testing.T) {
	hub := quietHub()
	sub := hub.Subscribe(stream.Topics(), 2)
	hub.Unsubscribe(sub)

	select {
	case _, open := <-sub.Events():
		if open {
			t.Fatal("the queue delivered an event after unsubscribe")
		}
	case <-time.After(time.Second):
		t.Fatal("the queue was not closed by unsubscribe")
	}

	if got := hub.Subscribers(); got != 0 {
		t.Fatalf("subscribers = %d after unsubscribe, want 0", got)
	}
}

// TestPublishingConcurrentlyNumbersEveryEventExactlyOnce.
//
// Run under -race. Two sources publish at once in production — the market feed
// and the pollers — and a sequence that skipped or repeated would make a
// client's cursor mean nothing.
func TestPublishingConcurrentlyNumbersEveryEventExactlyOnce(t *testing.T) {
	hub := quietHub()
	sub := hub.Subscribe([]stream.Topic{stream.TopicCandles}, 4096)
	defer hub.Unsubscribe(sub)

	const writers, each = 8, 256

	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < each; i++ {
				hub.Publish(stream.Event{Topic: stream.TopicCandles})
			}
		}()
	}
	wg.Wait()

	seen := map[int64]bool{}
	for {
		select {
		case event := <-sub.Events():
			if seen[event.Sequence] {
				t.Fatalf("sequence %d was issued twice", event.Sequence)
			}
			seen[event.Sequence] = true
			continue
		default:
		}
		break
	}

	if len(seen) != writers*each {
		t.Fatalf("received %d distinct sequences, want %d", len(seen), writers*each)
	}
	for i := int64(1); i <= writers*each; i++ {
		if !seen[i] {
			t.Fatalf("sequence %d was never issued", i)
		}
	}
}

// TestRunReturnsOnlyAfterEverySourceHasFinished.
//
// # What this prevents
//
// Run is what the api's shutdown path waits on. If it returned while its
// sources were still unwinding, the process would exit with a market
// connection half closed and pollers mid-query — and the leak would be
// invisible, because the caller had already been told everything had stopped.
//
// The sources here take a moment to finish after cancellation, so a Run that
// launches goroutines and returns without waiting sees a finished count below
// the number of sources.
func TestRunReturnsOnlyAfterEverySourceHasFinished(t *testing.T) {
	hub := quietHub()
	ctx, cancel := context.WithCancel(context.Background())

	var started sync.WaitGroup
	started.Add(2)

	var finished atomic.Int64
	source := sourceFunc(func(ctx context.Context, publish func(stream.Event)) error {
		started.Done()
		<-ctx.Done()
		time.Sleep(50 * time.Millisecond)
		finished.Add(1)
		return ctx.Err()
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		hub.Run(ctx, source, source)
	}()

	started.Wait()
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after the context was cancelled")
	}

	if got := finished.Load(); got != 2 {
		t.Fatalf("Run returned with %d of 2 sources finished; it must wait for all of them", got)
	}
}

type sourceFunc func(ctx context.Context, publish func(stream.Event)) error

func (f sourceFunc) Run(ctx context.Context, publish func(stream.Event)) error {
	return f(ctx, publish)
}
