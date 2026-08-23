package candle

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
)

// TestTheWireIsAlwaysUTC.
//
// # What this prevents
//
// Every timestamp in this system is UTC, but a time.Time carries a location
// and pgx hands one back in whatever zone the connection reports. If the
// renderer passed that through, a candle would arrive at the phone as
// 2024-03-01T09:00:00+09:00 — the same instant, formatted so that a client
// bucketing by date, or comparing against a signal_time rendered elsewhere,
// silently disagrees with the server.
//
// The fixture is deliberately built in a zone that is not UTC and not the test
// machine's, so a renderer that dropped .UTC() fails here rather than passing
// wherever the tests happen to run.
func TestTheWireIsAlwaysUTC(t *testing.T) {
	tokyo := time.FixedZone("JST", 9*60*60)
	open := time.Date(2024, 3, 1, 9, 0, 0, 0, tokyo)

	rendered := ToCandleResponse(models.Candle{
		Symbol: "BTCUSDT", MarketType: constants.MarketTypeSpot,
		Timeframe: constants.Timeframe1m,
		OpenTime:  open, CloseTime: open.Add(time.Minute - time.Millisecond),
		Open:  decimal.RequireFromString("64000"),
		High:  decimal.RequireFromString("64100"),
		Low:   decimal.RequireFromString("63900"),
		Close: decimal.RequireFromString("64010"),

		Volume:   decimal.RequireFromString("1.5"),
		IsClosed: true,
	})

	encoded, err := json.Marshal(rendered)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	var onTheWire struct {
		OpenTime  string `json:"open_time"`
		CloseTime string `json:"close_time"`
	}
	if err := json.Unmarshal(encoded, &onTheWire); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if onTheWire.OpenTime != "2024-03-01T00:00:00Z" {
		t.Errorf("open_time = %s, want 2024-03-01T00:00:00Z", onTheWire.OpenTime)
	}
	if onTheWire.CloseTime != "2024-03-01T00:00:59.999Z" {
		t.Errorf("close_time = %s, want 2024-03-01T00:00:59.999Z", onTheWire.CloseTime)
	}
}

// TestAPageOfCandlesIsAnEmptyArrayRatherThanNull.
//
// encoding/json renders a nil slice as null. A client written against a list
// then has to handle two shapes for "nothing", and the one it forgets is the
// one that arrives on a quiet day.
func TestAPageOfCandlesIsAnEmptyArrayRatherThanNull(t *testing.T) {
	encoded, err := json.Marshal(ToCandleResponses(nil))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if string(encoded) != "[]" {
		t.Fatalf("an empty page rendered as %s, want []", encoded)
	}
}

// TestPricesAreStringsOnTheWire.
//
// numeric(20,8) does not fit a float64, and a JSON number invites a client to
// parse one. The prices go out as strings; this fails if a field is ever
// changed to a numeric type.
func TestPricesAreStringsOnTheWire(t *testing.T) {
	rendered := ToCandleResponse(models.Candle{
		OpenTime: time.Unix(0, 0).UTC(), CloseTime: time.Unix(59, 0).UTC(),
		Open:  decimal.RequireFromString("64000.12345678"),
		High:  decimal.RequireFromString("64100.1"),
		Low:   decimal.RequireFromString("63900"),
		Close: decimal.RequireFromString("64010.00000001"),

		Volume: decimal.RequireFromString("0.00000001"),
	})

	encoded, err := json.Marshal(rendered)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	var keys map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &keys); err != nil {
		t.Fatalf("decode: %v", err)
	}

	for _, key := range []string{"open", "high", "low", "close", "volume"} {
		raw := string(keys[key])
		if len(raw) == 0 || raw[0] != '"' {
			t.Errorf("%s rendered as %s, want a JSON string", key, raw)
		}
	}

	// The exact digits survive: a float64 round trip would not keep these.
	if got := string(keys["open"]); got != `"64000.12345678"` {
		t.Errorf("open = %s, want \"64000.12345678\"", got)
	}
	if got := string(keys["close"]); got != `"64010.00000001"` {
		t.Errorf("close = %s, want \"64010.00000001\"", got)
	}
}
