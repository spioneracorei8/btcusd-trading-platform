// Package binance implements the market data repository against Binance's
// public REST and WebSocket endpoints.
//
// Everything Binance-shaped stops here. The DTOs in this file are converted
// to models.Candle at the package boundary, so no field name, array position
// or JSON quirk of this exchange reaches a usecase, and swapping the exchange
// later means rewriting this package alone.
//
// Only public market data is read. No endpoint that could place, amend or
// cancel an order is called, and no API key is used: public market data needs
// none, and a key with trading rights must never be introduced.
package binance

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/shopspring/decimal"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
)

// restKline is one entry of the /api/v3/klines response.
//
// Binance returns each kline as a heterogeneous JSON array rather than an
// object, so the field order below is part of the wire format and must not be
// reordered. Prices and volumes arrive as JSON strings and are kept as
// strings until decimal parsing: routing them through float64, even briefly,
// is what the coding standard forbids.
type restKline struct {
	OpenTime    int64  // 0
	Open        string // 1
	High        string // 2
	Low         string // 3
	Close       string // 4
	Volume      string // 5
	CloseTime   int64  // 6
	QuoteVolume string // 7
	TradeCount  int64  // 8
	// Positions 9-11 (taker buy volumes and an unused field) are ignored.
}

// restKlineFieldCount is the number of leading fields this client relies on.
const restKlineFieldCount = 9

// UnmarshalJSON decodes the positional array form.
func (k *restKline) UnmarshalJSON(data []byte) error {
	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("%w: kline is not an array: %w", constants.ErrUnexpectedPayload, err)
	}
	if len(raw) < restKlineFieldCount {
		return fmt.Errorf("%w: kline has %d fields, want at least %d",
			constants.ErrUnexpectedPayload, len(raw), restKlineFieldCount)
	}

	nums := []struct {
		index int
		dst   *int64
	}{
		{0, &k.OpenTime},
		{6, &k.CloseTime},
		{8, &k.TradeCount},
	}
	for _, f := range nums {
		if err := json.Unmarshal(raw[f.index], f.dst); err != nil {
			return fmt.Errorf("%w: kline field %d is not a number: %w",
				constants.ErrUnexpectedPayload, f.index, err)
		}
	}

	strs := []struct {
		index int
		dst   *string
	}{
		{1, &k.Open},
		{2, &k.High},
		{3, &k.Low},
		{4, &k.Close},
		{5, &k.Volume},
		{7, &k.QuoteVolume},
	}
	for _, f := range strs {
		if err := json.Unmarshal(raw[f.index], f.dst); err != nil {
			return fmt.Errorf("%w: kline field %d is not a string: %w",
				constants.ErrUnexpectedPayload, f.index, err)
		}
	}
	return nil
}

// toCandle converts the DTO into the model.
//
// isClosed is decided by the caller: a REST page cannot tell on its own
// whether its last entry is still forming, only a comparison against the
// exchange clock can.
func (k restKline) toCandle(symbol string, marketType constants.MarketType, timeframe constants.Timeframe, isClosed bool) (models.Candle, error) {
	candle := models.Candle{
		Symbol:     symbol,
		MarketType: marketType,
		Timeframe:  timeframe,
		OpenTime:   millisToUTC(k.OpenTime),
		CloseTime:  millisToUTC(k.CloseTime),
		TradeCount: int32(k.TradeCount),
		IsClosed:   isClosed,
	}

	for _, field := range []struct {
		name string
		raw  string
		dst  *decimal.Decimal
	}{
		{"open", k.Open, &candle.Open},
		{"high", k.High, &candle.High},
		{"low", k.Low, &candle.Low},
		{"close", k.Close, &candle.Close},
		{"volume", k.Volume, &candle.Volume},
		{"quote_volume", k.QuoteVolume, &candle.QuoteVolume},
	} {
		value, err := decimal.NewFromString(field.raw)
		if err != nil {
			return models.Candle{}, fmt.Errorf("%w: %s %s %s: %s %q is not a number: %w",
				constants.ErrUnexpectedPayload, symbol, timeframe,
				candle.OpenTime.Format(time.RFC3339), field.name, field.raw, err)
		}
		*field.dst = value
	}

	if err := validateCandle(candle); err != nil {
		return models.Candle{}, err
	}
	return candle, nil
}

// streamMessage is the envelope of a combined stream
// (/stream?streams=a/b/c). The single-stream endpoint has no envelope, which
// is one reason this client always uses the combined form.
type streamMessage struct {
	Stream string          `json:"stream"`
	Data   json.RawMessage `json:"data"`
}

// Binance's kline object distinguishes fields only by letter case: "l" is the
// low price but "L" is the last trade id, "v" is volume but "V" is taker buy
// volume, "t" is open time but "T" is close time.
//
// Go's encoding/json falls back to a case-insensitive match when no exact tag
// matches, so a struct that declares only the lower-case fields silently binds
// "L" to Low, "V" to Volume and "T" to OpenTime. Every uppercase twin below is
// therefore declared even where its value is unused: the declaration exists to
// give the decoder an exact match so it never reaches for the wrong field.
//
// Deleting one of these "unused" fields reintroduces the bug quietly, and the
// corrupted price would be indistinguishable from a real one downstream.

// klineEvent is the payload of a kline stream message.
type klineEvent struct {
	EventType string  `json:"e"`
	EventTime int64   `json:"E"` // twin of "e"
	Symbol    string  `json:"s"`
	Kline     wsKline `json:"k"`
}

// wsKline is the kline object inside a stream event. Unlike the REST form it
// is a real object, so the fields are named — cryptically, but named.
type wsKline struct {
	OpenTime    int64  `json:"t"`
	CloseTime   int64  `json:"T"` // twin of "t"
	Symbol      string `json:"s"`
	Interval    string `json:"i"`
	Open        string `json:"o"`
	Close       string `json:"c"`
	High        string `json:"h"`
	Low         string `json:"l"`
	Volume      string `json:"v"`
	QuoteVolume string `json:"q"`
	TradeCount  int64  `json:"n"`

	// IsClosed is Binance's "k.x". It is the single most consequential field
	// in this phase: false means the bar is still forming and must never be
	// stored, only cached for display.
	IsClosed bool `json:"x"`

	// Declared purely to claim the exact key, so the decoder cannot
	// case-insensitively bind these to Low, Volume and QuoteVolume above.
	FirstTradeId        int64  `json:"f"`
	LastTradeId         int64  `json:"L"` // twin of "l"
	TakerBuyVolume      string `json:"V"` // twin of "v"
	TakerBuyQuoteVolume string `json:"Q"` // twin of "q"
	Ignore              string `json:"B"`
}

// toCandle converts the stream DTO into the model, carrying the exchange's
// own closed flag rather than inferring one.
func (k wsKline) toCandle(marketType constants.MarketType) (models.Candle, error) {
	timeframe, err := constants.ParseTimeframe(k.Interval)
	if err != nil {
		return models.Candle{}, fmt.Errorf("%w: %w", constants.ErrUnexpectedPayload, err)
	}

	rest := restKline{
		OpenTime:    k.OpenTime,
		Open:        k.Open,
		High:        k.High,
		Low:         k.Low,
		Close:       k.Close,
		Volume:      k.Volume,
		CloseTime:   k.CloseTime,
		QuoteVolume: k.QuoteVolume,
		TradeCount:  k.TradeCount,
	}
	return rest.toCandle(k.Symbol, marketType, timeframe, k.IsClosed)
}

// validateCandle rejects a bar that cannot be true, before it reaches the
// database and its own constraints.
//
// Binance is reliable, but a malformed candle that slips through here becomes
// an indicator value, then a signal, and the error is invisible by then.
func validateCandle(c models.Candle) error {
	if c.Symbol == "" {
		return fmt.Errorf("%w: candle has no symbol", constants.ErrUnexpectedPayload)
	}
	if !c.CloseTime.After(c.OpenTime) {
		return fmt.Errorf("%w: %s %s close_time %s is not after open_time %s",
			constants.ErrUnexpectedPayload, c.Symbol, c.Timeframe,
			c.CloseTime.Format(time.RFC3339), c.OpenTime.Format(time.RFC3339))
	}
	if c.High.LessThan(c.Low) {
		return fmt.Errorf("%w: %s %s at %s: high %s is below low %s",
			constants.ErrUnexpectedPayload, c.Symbol, c.Timeframe,
			c.OpenTime.Format(time.RFC3339), c.High, c.Low)
	}
	for _, field := range []struct {
		name  string
		value decimal.Decimal
	}{
		{"open", c.Open}, {"high", c.High}, {"low", c.Low}, {"close", c.Close},
	} {
		if field.value.IsNegative() || field.value.IsZero() {
			return fmt.Errorf("%w: %s %s at %s: %s is %s",
				constants.ErrUnexpectedPayload, c.Symbol, c.Timeframe,
				c.OpenTime.Format(time.RFC3339), field.name, field.value)
		}
	}
	if c.Volume.IsNegative() || c.QuoteVolume.IsNegative() {
		return fmt.Errorf("%w: %s %s at %s: negative volume",
			constants.ErrUnexpectedPayload, c.Symbol, c.Timeframe,
			c.OpenTime.Format(time.RFC3339))
	}
	if c.TradeCount < 0 {
		return fmt.Errorf("%w: %s %s at %s: negative trade count",
			constants.ErrUnexpectedPayload, c.Symbol, c.Timeframe,
			c.OpenTime.Format(time.RFC3339))
	}
	return nil
}

// millisToUTC converts Binance's millisecond epoch into a UTC time.
func millisToUTC(ms int64) time.Time {
	return time.UnixMilli(ms).UTC()
}
