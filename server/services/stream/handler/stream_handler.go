package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/helper"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/stream"
	_stream_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/stream/usecase"
)

type streamHandler struct {
	hub    *_stream_us.Hub
	logger *slog.Logger

	// allowedOrigins are browser origins permitted in addition to the one
	// this request was served from. See NewStreamHandlerImpl.
	allowedOrigins []string
}

// NewStreamHandlerImpl builds the websocket handler.
//
// # Why origins are checked at all
//
// A websocket handshake is not bound by the same-origin policy the way fetch
// is. Any page loaded in a browser that can route to this host may open this
// endpoint and read the signal feed — every entry, stop, target and reason —
// unless the handshake refuses it.
//
// That was not true while the only client was a native app on a tailnet, and
// ADR 0024 said as much: origin checking was listed as something that becomes
// necessary "the moment a browser can reach it". Serving the app as a PWA is
// that moment, so this is now checked whether or not the API is public.
//
// The default needs no configuration: coder/websocket allows a request whose
// Origin host equals the Host it was sent to, and one with no Origin header at
// all — a native client or curl, which no page can forge on someone's behalf.
// Everything else must match allowedOrigins or the handshake is refused with
// 403. So a same-origin deployment is correct with the list empty, and the
// list exists for development, where the app is served by Metro on one port
// and the API answers on another.
func NewStreamHandlerImpl(
	hub *_stream_us.Hub, logger *slog.Logger, allowedOrigins []string,
) stream.StreamHandler {
	return &streamHandler{hub: hub, logger: logger, allowedOrigins: allowedOrigins}
}

// Stream answers GET /api/v1/stream.
//
// Query parameters:
//
//	topics  comma-separated; defaults to all of them
//	since   the sequence a client last saw, per topic, as topic:seq pairs
//
// # Reconnect
//
// A phone drops constantly. On reconnect the client says what it last saw and
// the server reports how far behind that is — it does not replay from the
// beginning, which on a candle topic would be the entire history and on a
// mobile connection would drop again before it finished.
//
// What it cannot do is replay the events themselves: the hub holds no history,
// deliberately. The gap is reported so the client can refetch the affected
// range over REST, which is the endpoint that is built for range queries.
func (h *streamHandler) Stream(w http.ResponseWriter, r *http.Request) {
	topics, err := parseTopics(r.URL.Query().Get("topics"))
	if err != nil {
		helper.WriteAPIError(w, h.logger, http.StatusBadRequest,
			constants.APIErrInvalidParameter, err.Error())
		return
	}

	since, err := parseSince(r.URL.Query().Get("since"))
	if err != nil {
		helper.WriteAPIError(w, h.logger, http.StatusBadRequest,
			constants.APIErrInvalidParameter, err.Error())
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// Same-origin and no-origin are allowed by the library; these are the
		// extra ones this deployment trusts. Accept has already written the
		// 403 by the time a refusal reaches the error below.
		OriginPatterns: h.allowedOrigins,
	})
	if err != nil {
		h.logger.WarnContext(r.Context(), "websocket handshake failed",
			"error", err, "origin", r.Header.Get("Origin"), "host", r.Host)
		return
	}
	defer conn.CloseNow()

	ctx := conn.CloseRead(r.Context())

	sub := h.hub.Subscribe(topics, constants.StreamQueueSize)
	defer h.hub.Unsubscribe(sub)

	if err := h.greet(ctx, conn, topics, since); err != nil {
		return
	}
	h.pump(ctx, conn, sub)
}

// greet tells the client what it subscribed to and how far behind it is.
func (h *streamHandler) greet(
	ctx context.Context, conn *websocket.Conn, topics []stream.Topic, since map[stream.Topic]int64,
) error {
	names := make([]string, 0, len(topics))
	behind := map[string]int64{}

	for _, topic := range topics {
		names = append(names, topic.String())

		current := h.hub.Sequence(topic)
		if seen, ok := since[topic]; ok && current > seen {
			behind[topic.String()] = current - seen
		}
	}

	return h.write(ctx, conn, envelope{
		Type:   "subscribed",
		SentAt: time.Now().UTC(),
		Topics: names,
		Behind: behind,
		Note: "The hub keeps no history, so missed events are not replayed. " +
			"`behind` is how many were issued while you were away — refetch that " +
			"range over REST.",
	})
}

// pump writes events until the connection or the context ends.
func (h *streamHandler) pump(ctx context.Context, conn *websocket.Conn, sub *_stream_us.Subscriber) {
	ping := time.NewTicker(constants.StreamPingInterval)
	defer ping.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case <-ping.C:
			// A phone drops without closing, and a socket nobody writes to
			// stays open long after the client is gone.
			pingCtx, cancel := context.WithTimeout(ctx, constants.StreamWriteTimeout)
			err := conn.Ping(pingCtx)
			cancel()
			if err != nil {
				return
			}

		case event, ok := <-sub.Events():
			if !ok {
				return
			}

			// A client that fell behind is told so before the next event, so
			// its view has a marked hole rather than a silent one.
			if dropped := sub.Dropped(); dropped > 0 {
				if err := h.write(ctx, conn, envelope{
					Type: "gap", SentAt: time.Now().UTC(), Dropped: dropped,
					Note: "events were dropped because this connection was not keeping up; " +
						"refetch over REST if the gap matters",
				}); err != nil {
					return
				}
			}

			if err := h.write(ctx, conn, envelope{
				Type:     "event",
				SentAt:   time.Now().UTC(),
				Topic:    event.Topic.String(),
				Sequence: event.Sequence,
				At:       helper.NullableTime(event.At),
				Data:     event.Payload,
			}); err != nil {
				return
			}
		}
	}
}

// write sends one message, bounded so a stalled socket cannot hold the
// goroutine open indefinitely.
func (h *streamHandler) write(ctx context.Context, conn *websocket.Conn, message envelope) error {
	payload, err := json.Marshal(message)
	if err != nil {
		h.logger.ErrorContext(ctx, "encode stream message", "error", err)
		return err
	}

	writeCtx, cancel := context.WithTimeout(ctx, constants.StreamWriteTimeout)
	defer cancel()
	return conn.Write(writeCtx, websocket.MessageText, payload)
}

// envelope is every message on the wire.
//
// One shape for all of them so a client parses once and branches on `type`,
// rather than guessing from which fields are present.
type envelope struct {
	Type   string    `json:"type"`
	SentAt time.Time `json:"sent_at"`

	// subscribed
	Topics []string         `json:"topics,omitempty"`
	Behind map[string]int64 `json:"behind,omitempty"`

	// event
	Topic    string     `json:"topic,omitempty"`
	Sequence int64      `json:"sequence,omitempty"`
	At       *time.Time `json:"at,omitempty"`
	Data     any        `json:"data,omitempty"`

	// gap
	Dropped int64 `json:"dropped,omitempty"`

	Note string `json:"note,omitempty"`
}

// parseTopics reads the subscription, defaulting to all of them.
func parseTopics(raw string) ([]stream.Topic, error) {
	if raw == "" {
		return stream.Topics(), nil
	}

	var topics []stream.Topic
	for _, name := range strings.Split(raw, ",") {
		topic := stream.Topic(strings.TrimSpace(name))
		if !topic.Valid() {
			return nil, fmt.Errorf("topics=%s contains %q, which is not a topic; the topics are %s",
				raw, topic, topicNames())
		}
		topics = append(topics, topic)
	}
	return topics, nil
}

// parseSince reads per-topic cursors, as topic:sequence pairs.
func parseSince(raw string) (map[stream.Topic]int64, error) {
	since := map[stream.Topic]int64{}
	if raw == "" {
		return since, nil
	}

	for _, pair := range strings.Split(raw, ",") {
		name, value, found := strings.Cut(strings.TrimSpace(pair), ":")
		if !found {
			return nil, fmt.Errorf("since=%s is not a list of topic:sequence pairs", raw)
		}

		topic := stream.Topic(strings.TrimSpace(name))
		if !topic.Valid() {
			return nil, fmt.Errorf("since names %q, which is not a topic", topic)
		}

		seq, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err != nil || seq < 0 {
			return nil, fmt.Errorf("since=%s has a sequence that is not a whole number", raw)
		}
		since[topic] = seq
	}
	return since, nil
}

func topicNames() string {
	names := make([]string, 0, 4)
	for _, t := range stream.Topics() {
		names = append(names, t.String())
	}
	return strings.Join(names, ", ")
}
