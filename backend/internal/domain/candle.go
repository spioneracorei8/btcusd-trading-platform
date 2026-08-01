package domain

import (
	"time"

	"github.com/shopspring/decimal"
)

// Candle is a single OHLCV bar for one symbol/market/timeframe.
//
// Prices and volumes are decimal.Decimal, never float64: rounding drift on
// money is not acceptable in a system whose whole purpose is measurement.
//
// OpenTime is the primary key component and is always UTC.
type Candle struct {
	Symbol      string
	MarketType  MarketType
	Timeframe   Timeframe
	OpenTime    time.Time
	CloseTime   time.Time
	Open        decimal.Decimal
	High        decimal.Decimal
	Low         decimal.Decimal
	Close       decimal.Decimal
	Volume      decimal.Decimal
	QuoteVolume decimal.Decimal
	TradeCount  int32

	// IsClosed reports whether Binance considers the bar final (kline field
	// "k.x"). Only closed candles may reach indicators, strategies or the
	// candles table; unclosed ones are display-only.
	IsClosed bool
}
