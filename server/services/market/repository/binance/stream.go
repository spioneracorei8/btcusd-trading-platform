package binance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/coder/websocket"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/market"
)

// maxStreamMessageBytes caps a single frame. Kline messages are well under a
// kilobyte; anything approaching this is not a kline.
const maxStreamMessageBytes = 1 << 20 // 1 MiB

// StreamKlines opens one combined stream carrying every configured timeframe
// and delivers klines to onKline until the context is cancelled or the
// connection ends.
//
// One connection carries all timeframes rather than one per timeframe: fewer
// sockets to keep alive, and every timeframe shares the same fate, so a
// partial outage where 1m survives and 5m silently dies cannot happen.
//
// Binance sends a protocol ping every few minutes and closes the connection
// if it is not answered. coder/websocket replies to pings from inside Read,
// so there is no pong to send by hand — but it only does so while a Read is
// in flight, which the loop below guarantees.
func (c *client) StreamKlines(ctx context.Context, params market.StreamParams, onKline func(market.StreamedKline)) error {
	if params.MarketType != constants.MarketTypeSpot {
		return fmt.Errorf("binance: %s streaming is not implemented", params.MarketType)
	}
	if len(params.Timeframes) == 0 {
		return fmt.Errorf("binance: no timeframes to stream")
	}

	endpoint, err := c.streamURL(params)
	if err != nil {
		return err
	}

	conn, _, err := websocket.Dial(ctx, endpoint, &websocket.DialOptions{
		HTTPClient: c.httpClient,
	})
	if err != nil {
		return fmt.Errorf("%w: dial: %w", constants.ErrStreamClosed, err)
	}
	conn.SetReadLimit(maxStreamMessageBytes)
	// The status is best effort: by the time this runs the peer may be gone.
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()

	return c.readLoop(ctx, conn, params, onKline)
}

// readLoop consumes frames until the stream ends.
//
// The read deadline is the important part. A TCP connection can stay open
// while delivering nothing — a middlebox holding it, or Binance losing the
// subscription — and that failure is invisible: the socket looks healthy
// while the candle series quietly stops advancing. Bounding each read turns
// that silence into a reconnect.
func (c *client) readLoop(ctx context.Context, conn *websocket.Conn, params market.StreamParams, onKline func(market.StreamedKline)) error {
	for {
		readCtx, cancel := context.WithTimeout(ctx, constants.StreamStallTimeout)
		msgType, data, err := conn.Read(readCtx)
		cancel()

		if err != nil {
			return c.classifyReadError(ctx, err)
		}
		if msgType != websocket.MessageText {
			continue
		}

		kline, ok, err := decodeStreamedKline(data, params.MarketType)
		if err != nil {
			return err
		}
		if !ok {
			// Subscription acknowledgements and other non-kline traffic.
			continue
		}
		onKline(kline)
	}
}

// classifyReadError separates the three ways a stream ends, because they call
// for different responses and different log levels.
func (c *client) classifyReadError(ctx context.Context, err error) error {
	// The caller is shutting down; not a stream failure at all.
	if ctx.Err() != nil {
		return ctx.Err()
	}

	// The read deadline fired while the parent context is still live, so the
	// connection went silent rather than closed.
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%w: no message for %s", constants.ErrStreamStalled, constants.StreamStallTimeout)
	}

	// Everything else is an ordinary disconnect, including the 24 hour cycle
	// Binance applies to every connection. Expected, not a fault.
	return fmt.Errorf("%w: %w", constants.ErrStreamClosed, err)
}

// decodeStreamedKline parses one frame. ok is false for messages that are not
// klines, which are ignored rather than treated as errors.
func decodeStreamedKline(data []byte, marketType constants.MarketType) (market.StreamedKline, bool, error) {
	var msg streamMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return market.StreamedKline{}, false, fmt.Errorf("%w: decode stream envelope: %w",
			constants.ErrUnexpectedPayload, err)
	}
	if len(msg.Data) == 0 {
		return market.StreamedKline{}, false, nil
	}

	var event klineEvent
	if err := json.Unmarshal(msg.Data, &event); err != nil {
		return market.StreamedKline{}, false, fmt.Errorf("%w: decode kline event: %w",
			constants.ErrUnexpectedPayload, err)
	}
	if event.EventType != "kline" {
		return market.StreamedKline{}, false, nil
	}

	candle, err := event.Kline.toCandle(marketType)
	if err != nil {
		return market.StreamedKline{}, false, err
	}
	return market.StreamedKline{Candle: candle}, true, nil
}

// streamURL builds the combined-stream endpoint for every timeframe at once.
func (c *client) streamURL(params market.StreamParams) (string, error) {
	names := make([]string, 0, len(params.Timeframes))
	seen := make(map[constants.Timeframe]struct{}, len(params.Timeframes))

	for _, timeframe := range params.Timeframes {
		if !timeframe.Valid() {
			return "", fmt.Errorf("binance: unsupported timeframe %q", timeframe)
		}
		if _, dup := seen[timeframe]; dup {
			continue
		}
		seen[timeframe] = struct{}{}
		names = append(names, streamName(params.Symbol, timeframe))
	}

	return fmt.Sprintf("%s/stream?streams=%s", c.wsBaseURL, strings.Join(names, "/")), nil
}
