package usecase

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/stream"
)

// fakeCandleSource replays a fixed list of klines and then stops.
type fakeCandleSource struct {
	candles []models.Candle

	// seen records what the source was asked to watch, so the test can check
	// the feed passes the configured instrument through unchanged.
	seenSymbol     string
	seenMarketType constants.MarketType
	seenTimeframes []constants.Timeframe
}

func (f *fakeCandleSource) Watch(
	ctx context.Context, symbol string, marketType constants.MarketType,
	timeframes []constants.Timeframe, onCandle func(models.Candle),
) error {
	f.seenSymbol, f.seenMarketType, f.seenTimeframes = symbol, marketType, timeframes
	for _, c := range f.candles {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		onCandle(c)
	}
	return nil
}

func bar(open time.Time, closePrice string, closed bool) models.Candle {
	return models.Candle{
		Symbol: "BTCUSDT", MarketType: constants.MarketTypeSpot,
		Timeframe: constants.Timeframe1m,
		OpenTime:  open, CloseTime: open.Add(time.Minute - time.Millisecond),
		Open:  decimal.RequireFromString("64000"),
		High:  decimal.RequireFromString("64100"),
		Low:   decimal.RequireFromString("63900"),
		Close: decimal.RequireFromString(closePrice),

		Volume:   decimal.RequireFromString("1.5"),
		IsClosed: closed,
	}
}

// collect runs a source to completion and returns what it published.
func collect(t *testing.T, source stream.Source) []stream.Event {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var events []stream.Event
	if err := source.Run(ctx, func(e stream.Event) { events = append(events, e) }); err != nil {
		t.Fatalf("run the source: %v", err)
	}
	return events
}

// TestAFormingCandleIsSentFlaggedAsUnclosed.
//
// # Why this test exists
//
// The websocket is the one place in this system permitted to send a bar that
// has not closed (CLAUDE.md §3.1), and the whole safety of that rests on the
// client being able to tell. A forming bar that reached a phone looking
// identical to a closed one would be charted, and — worse — could be treated
// as final by anything downstream.
//
// The assertion is on the encoded JSON rather than on a struct field, because
// what protects the client is the wire, and a renderer that dropped the field
// or renamed it would pass a struct-level check.
func TestAFormingCandleIsSentFlaggedAsUnclosed(t *testing.T) {
	open := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)
	source := &fakeCandleSource{candles: []models.Candle{
		bar(open, "64010", false),
		bar(open, "64020", false),
		bar(open, "64030", true),
	}}

	events := collect(t, CandleFeed{
		Source: source, Symbol: "BTCUSDT", MarketType: constants.MarketTypeSpot,
		Timeframes: []constants.Timeframe{constants.Timeframe1m},
	})

	if len(events) != 3 {
		t.Fatalf("published %d events, want 3: every kline is forwarded, forming or not", len(events))
	}

	want := []bool{false, false, true}
	for i, event := range events {
		if event.Topic != stream.TopicCandles {
			t.Fatalf("event %d went to %s, want candles", i, event.Topic)
		}

		encoded, err := json.Marshal(event.Payload)
		if err != nil {
			t.Fatalf("encode event %d: %v", i, err)
		}

		var onTheWire struct {
			IsClosed *bool  `json:"is_closed"`
			Close    string `json:"close"`
		}
		if err := json.Unmarshal(encoded, &onTheWire); err != nil {
			t.Fatalf("decode event %d: %v", i, err)
		}

		if onTheWire.IsClosed == nil {
			t.Fatalf("event %d has no is_closed on the wire: %s", i, encoded)
		}
		if *onTheWire.IsClosed != want[i] {
			t.Fatalf("event %d is_closed = %v, want %v: %s", i, *onTheWire.IsClosed, want[i], encoded)
		}
		if onTheWire.Close == "" {
			t.Fatalf("event %d has no close price on the wire: %s", i, encoded)
		}
	}
}

// TestAStreamedCandleHasTheSameShapeAsTheRestEndpoint.
//
// Two transports carry a candle. If they rendered independently, the app would
// need two parsers for one object and the difference would show up first on
// whichever field somebody forgot — most likely is_closed, which is the one
// that matters. Both go through candle.ToCandleResponse; this pins the keys.
func TestAStreamedCandleHasTheSameShapeAsTheRestEndpoint(t *testing.T) {
	open := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)
	source := &fakeCandleSource{candles: []models.Candle{bar(open, "64010", false)}}

	events := collect(t, CandleFeed{
		Source: source, Symbol: "BTCUSDT", MarketType: constants.MarketTypeSpot,
		Timeframes: []constants.Timeframe{constants.Timeframe1m},
	})

	encoded, err := json.Marshal(events[0].Payload)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	var keys map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &keys); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Every field, with the value it should carry. Presence alone is not
	// enough: a renderer that stopped setting one would still emit the key,
	// with an empty string that charts as nothing.
	want := map[string]string{
		"open_time":  `"2024-03-01T00:00:00Z"`,
		"close_time": `"2024-03-01T00:00:59.999Z"`,
		"open":       `"64000"`,
		"high":       `"64100"`,
		"low":        `"63900"`,
		"close":      `"64010"`,
		"volume":     `"1.5"`,
		"is_closed":  `false`,
	}
	for key, value := range want {
		got, ok := keys[key]
		if !ok {
			t.Errorf("the streamed candle has no %q; it must match GET /api/v1/candles: %s", key, encoded)
			continue
		}
		if string(got) != value {
			t.Errorf("the streamed candle has %s = %s, want %s", key, got, value)
		}
	}
	if len(keys) != len(want) {
		t.Errorf("the streamed candle has %d fields, want %d: %s", len(keys), len(want), encoded)
	}
}

// TestTheEventCursorIsTheBarsOwnTime.
//
// A client's cursor has to mean something about the market rather than about
// delivery: two forming updates of the same bar carry the same At, and a
// client reconnecting on it asks for the right range.
func TestTheEventCursorIsTheBarsOwnTime(t *testing.T) {
	first := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)
	second := first.Add(time.Minute)

	source := &fakeCandleSource{candles: []models.Candle{
		bar(first, "64010", false),
		bar(first, "64030", true),
		bar(second, "64040", false),
	}}

	events := collect(t, CandleFeed{
		Source: source, Symbol: "BTCUSDT", MarketType: constants.MarketTypeSpot,
		Timeframes: []constants.Timeframe{constants.Timeframe1m},
	})

	want := []time.Time{first, first, second}
	for i, event := range events {
		if !event.At.Equal(want[i]) {
			t.Fatalf("event %d At = %s, want the bar's open time %s", i, event.At, want[i])
		}
	}
}

// TestTheFeedWatchesTheConfiguredInstrument, rather than defaulting to
// something. A feed silently watching BTCUSDT on a deployment configured for
// something else would chart a different market beside the right signals.
func TestTheFeedWatchesTheConfiguredInstrument(t *testing.T) {
	source := &fakeCandleSource{}
	timeframes := []constants.Timeframe{constants.Timeframe1m, constants.Timeframe5m}

	collect(t, CandleFeed{
		Source: source, Symbol: "ETHUSDT", MarketType: constants.MarketTypeSpot,
		Timeframes: timeframes,
	})

	if source.seenSymbol != "ETHUSDT" {
		t.Errorf("watched %q, want ETHUSDT", source.seenSymbol)
	}
	if source.seenMarketType != constants.MarketTypeSpot {
		t.Errorf("watched market type %q, want spot", source.seenMarketType)
	}
	if len(source.seenTimeframes) != len(timeframes) {
		t.Fatalf("watched %d timeframes, want %d", len(source.seenTimeframes), len(timeframes))
	}
	for i, tf := range timeframes {
		if source.seenTimeframes[i] != tf {
			t.Errorf("timeframe %d = %s, want %s", i, source.seenTimeframes[i], tf)
		}
	}
}
