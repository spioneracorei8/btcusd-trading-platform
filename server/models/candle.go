// Package models holds the entities the whole system is written in terms of.
//
// A model is plain data: it carries no database tags and no ORM tags, so the
// repository layer is free to map it onto SQL rows and the handler layer onto
// JSON without either dictating the shape of the other.
package models

import (
	"time"

	"github.com/shopspring/decimal"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
)

// Candle is a single OHLCV bar for one symbol/market/timeframe.
//
// Prices and volumes are decimal.Decimal, never float64: rounding drift on
// money is not acceptable in a system whose whole purpose is measurement.
//
// OpenTime is the primary key component and is always UTC.
type Candle struct {
	Symbol      string
	MarketType  constants.MarketType
	Timeframe   constants.Timeframe
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
