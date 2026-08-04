package binance

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
)

// readFixture loads a recorded Binance payload. Tests never reach the network.
func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "testdata", "binance", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return data
}

func TestRestKlineToCandlePreservesDecimalPrecision(t *testing.T) {
	var klines []restKline
	if err := json.Unmarshal(readFixture(t, "klines_1m.json"), &klines); err != nil {
		t.Fatalf("decode klines: %v", err)
	}
	if len(klines) != 2 {
		t.Fatalf("decoded %d klines, want 2", len(klines))
	}

	candle, err := klines[0].toCandle("BTCUSDT", constants.MarketTypeSpot, constants.Timeframe1m, true)
	if err != nil {
		t.Fatalf("toCandle() returned error: %v", err)
	}

	// Every price and volume must survive at full 8-decimal precision. A
	// float64 round trip would quietly change these digits.
	for _, tt := range []struct {
		name string
		got  string
		want string
	}{
		{"open", candle.Open.String(), "64000.1"},
		{"high", candle.High.String(), "64100.55"},
		{"low", candle.Low.String(), "63950"},
		{"close", candle.Close.String(), "64080.25"},
		{"volume", candle.Volume.String(), "12.3456789"},
		{"quote_volume", candle.QuoteVolume.String(), "790123.456789"},
	} {
		if tt.got != tt.want {
			t.Errorf("%s = %s, want %s", tt.name, tt.got, tt.want)
		}
	}

	// The awkward one: 8 significant decimals that a float64 cannot hold
	// exactly.
	second, err := klines[1].toCandle("BTCUSDT", constants.MarketTypeSpot, constants.Timeframe1m, true)
	if err != nil {
		t.Fatalf("toCandle() returned error: %v", err)
	}
	if got := second.Close.String(); got != "64120.87654321" {
		t.Errorf("close = %s, want 64120.87654321", got)
	}
	if got := second.Volume.String(); got != "8.00000001" {
		t.Errorf("volume = %s, want 8.00000001", got)
	}

	if candle.TradeCount != 431 {
		t.Errorf("TradeCount = %d, want 431", candle.TradeCount)
	}
	if !candle.IsClosed {
		t.Error("IsClosed = false, want true")
	}
}

func TestRestKlineTimesAreUTC(t *testing.T) {
	var klines []restKline
	if err := json.Unmarshal(readFixture(t, "klines_1m.json"), &klines); err != nil {
		t.Fatalf("decode klines: %v", err)
	}

	candle, err := klines[0].toCandle("BTCUSDT", constants.MarketTypeSpot, constants.Timeframe1m, true)
	if err != nil {
		t.Fatalf("toCandle() returned error: %v", err)
	}

	wantOpen := time.UnixMilli(1767225600000).UTC()
	if !candle.OpenTime.Equal(wantOpen) {
		t.Errorf("OpenTime = %s, want %s", candle.OpenTime, wantOpen)
	}
	if candle.OpenTime.Location() != time.UTC || candle.CloseTime.Location() != time.UTC {
		t.Error("candle times must be UTC")
	}
	// Binance close times are the last millisecond of the interval, so the
	// bar is 59.999s long rather than a round minute.
	if got := candle.CloseTime.Sub(candle.OpenTime); got != 59999*time.Millisecond {
		t.Errorf("bar length = %s, want 59.999s", got)
	}
}

func TestRestKlineRejectsMalformedPayloads(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{name: "not an array", raw: `{"open":"1"}`},
		{name: "too few fields", raw: `[1767225600000,"1","2","3"]`},
		{name: "open time not a number", raw: `["nope","1","2","3","4","5",1767225659999,"7",8]`},
		{name: "price not a string", raw: `[1767225600000,1,"2","3","4","5",1767225659999,"7",8]`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var k restKline
			err := json.Unmarshal([]byte(tt.raw), &k)
			if err == nil {
				t.Fatalf("decoding %s returned no error", tt.raw)
			}
			if !errors.Is(err, constants.ErrUnexpectedPayload) {
				t.Errorf("error %v does not wrap ErrUnexpectedPayload", err)
			}
		})
	}
}

func TestRestKlineRejectsUnparseablePrice(t *testing.T) {
	k := restKline{
		OpenTime: 1767225600000, CloseTime: 1767225659999,
		Open: "not-a-number", High: "1", Low: "1", Close: "1",
		Volume: "1", QuoteVolume: "1", TradeCount: 1,
	}

	_, err := k.toCandle("BTCUSDT", constants.MarketTypeSpot, constants.Timeframe1m, true)
	if err == nil {
		t.Fatal("toCandle() accepted an unparseable price")
	}
	if !errors.Is(err, constants.ErrUnexpectedPayload) {
		t.Errorf("error %v does not wrap ErrUnexpectedPayload", err)
	}
}

// TestValidateCandleRejectsImpossibleBars covers the values that would still
// parse but cannot be true. Left unchecked they become an indicator value,
// then a signal, and by then the mistake is invisible.
func TestValidateCandleRejectsImpossibleBars(t *testing.T) {
	base := restKline{
		OpenTime: 1767225600000, CloseTime: 1767225659999,
		Open: "64000", High: "64100", Low: "63900", Close: "64050",
		Volume: "1", QuoteVolume: "64000", TradeCount: 1,
	}

	tests := []struct {
		name   string
		mutate func(*restKline)
	}{
		{"high below low", func(k *restKline) { k.High, k.Low = "63000", "64000" }},
		{"zero price", func(k *restKline) { k.Open = "0" }},
		{"negative price", func(k *restKline) { k.Close = "-1" }},
		{"negative volume", func(k *restKline) { k.Volume = "-1" }},
		{"negative trade count", func(k *restKline) { k.TradeCount = -1 }},
		{"close time before open time", func(k *restKline) { k.CloseTime = k.OpenTime - 1 }},
		{"close time equals open time", func(k *restKline) { k.CloseTime = k.OpenTime }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			k := base
			tt.mutate(&k)

			if _, err := k.toCandle("BTCUSDT", constants.MarketTypeSpot, constants.Timeframe1m, true); err == nil {
				t.Fatal("toCandle() accepted an impossible candle")
			}
		})
	}
}

// TestStreamKlineCarriesClosedFlag is the phase's central rule at the parsing
// layer: whatever the exchange says about k.x must survive into the model
// untouched, because everything downstream keys off it.
func TestStreamKlineCarriesClosedFlag(t *testing.T) {
	for _, tt := range []struct {
		fixture  string
		isClosed bool
	}{
		{"stream_kline_closed.json", true},
		{"stream_kline_open.json", false},
	} {
		t.Run(tt.fixture, func(t *testing.T) {
			var msg streamMessage
			if err := json.Unmarshal(readFixture(t, tt.fixture), &msg); err != nil {
				t.Fatalf("decode envelope: %v", err)
			}
			if msg.Stream != "btcusdt@kline_1m" {
				t.Errorf("Stream = %q", msg.Stream)
			}

			var event klineEvent
			if err := json.Unmarshal(msg.Data, &event); err != nil {
				t.Fatalf("decode event: %v", err)
			}

			candle, err := event.Kline.toCandle(constants.MarketTypeSpot)
			if err != nil {
				t.Fatalf("toCandle() returned error: %v", err)
			}
			if candle.IsClosed != tt.isClosed {
				t.Errorf("IsClosed = %v, want %v", candle.IsClosed, tt.isClosed)
			}
			if candle.Timeframe != constants.Timeframe1m {
				t.Errorf("Timeframe = %q, want 1m", candle.Timeframe)
			}
			if candle.Symbol != "BTCUSDT" {
				t.Errorf("Symbol = %q, want BTCUSDT", candle.Symbol)
			}
			if got := candle.Close.String(); got != "64080.25" {
				t.Errorf("Close = %s, want 64080.25", got)
			}
		})
	}
}

func TestStreamKlineRejectsUnknownInterval(t *testing.T) {
	k := wsKline{
		OpenTime: 1767225600000, CloseTime: 1767225659999,
		Symbol: "BTCUSDT", Interval: "7m",
		Open: "1", High: "1", Low: "1", Close: "1",
		Volume: "1", QuoteVolume: "1", TradeCount: 1,
	}

	_, err := k.toCandle(constants.MarketTypeSpot)
	if err == nil {
		t.Fatal("toCandle() accepted an unsupported interval")
	}
	if !errors.Is(err, constants.ErrUnexpectedPayload) {
		t.Errorf("error %v does not wrap ErrUnexpectedPayload", err)
	}
}

// TestStreamKlineCaseCollisions guards a bug that is invisible once shipped.
//
// Binance separates several kline fields by letter case alone: "l" is the low
// price, "L" the last trade id; likewise "v"/"V", "q"/"Q" and "t"/"T". Go's
// encoding/json falls back to a case-insensitive match, so a struct missing
// the uppercase twins binds "L" onto Low and "V" onto Volume. The result is a
// candle with a plausible-looking wrong price — the worst kind of corruption
// in a system whose whole purpose is measurement.
func TestStreamKlineCaseCollisions(t *testing.T) {
	// Uppercase twins deliberately carry values that would be obvious if they
	// leaked into the wrong field.
	const payload = `{
		"t": 1767225600000, "T": 1767225659999,
		"s": "BTCUSDT", "i": "1m",
		"f": 111, "L": 999,
		"o": "64000.00000000", "c": "64080.00000000",
		"h": "64100.00000000", "l": "63900.00000000",
		"v": "12.00000000", "n": 431, "x": true,
		"q": "790000.00000000",
		"V": "6.00000000", "Q": "390000.00000000", "B": "0"
	}`

	var k wsKline
	if err := json.Unmarshal([]byte(payload), &k); err != nil {
		t.Fatalf("decode kline: %v", err)
	}

	for _, tt := range []struct {
		field string
		got   string
		want  string
	}{
		{"low (must not take L)", k.Low, "63900.00000000"},
		{"volume (must not take V)", k.Volume, "12.00000000"},
		{"quote volume (must not take Q)", k.QuoteVolume, "790000.00000000"},
	} {
		if tt.got != tt.want {
			t.Errorf("%s = %q, want %q", tt.field, tt.got, tt.want)
		}
	}

	if k.OpenTime != 1767225600000 {
		t.Errorf("open time = %d, want 1767225600000 (must not take T)", k.OpenTime)
	}
	if k.CloseTime != 1767225659999 {
		t.Errorf("close time = %d, want 1767225659999", k.CloseTime)
	}

	candle, err := k.toCandle(constants.MarketTypeSpot)
	if err != nil {
		t.Fatalf("toCandle() returned error: %v", err)
	}
	if got := candle.Low.String(); got != "63900" {
		t.Errorf("candle low = %s, want 63900", got)
	}
	if got := candle.Volume.String(); got != "12" {
		t.Errorf("candle volume = %s, want 12", got)
	}
}
