package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
)

// Signal is one strategy decision taken at the close of a candle.
type Signal struct {
	Id         uuid.UUID
	Symbol     string
	MarketType constants.MarketType
	Timeframe  constants.Timeframe

	// SignalTime is the close time of the candle that produced the signal,
	// not the time the row was written. Using the bar close keeps live and
	// backtest runs comparable.
	SignalTime time.Time

	Direction constants.Direction

	// Strength is a 0-100 confidence score, numeric(5,2) in the database.
	Strength decimal.Decimal

	// SignalPrice is the close the strategy decided on, and EntryPrice what a
	// position would have been opened at.
	//
	// # Why these are two fields
	//
	// A decision taken on a bar's close cannot also fill on it, so the
	// backtest fills at the next bar's open plus slippage. Recording the close
	// as the entry would put that difference into every live-against-backtest
	// comparison as though it were slippage, permanently.
	//
	// SignalPrice is known immediately and is what a notification quotes.
	// EntryPrice stays unset until the next bar opens.
	SignalPrice decimal.NullDecimal

	// Advisory levels only. This system never places orders.
	EntryPrice decimal.NullDecimal
	StopLoss   decimal.NullDecimal
	TakeProfit decimal.NullDecimal

	StrategyName    string
	StrategyVersion string

	// Reason carries the indicator values behind the decision so a signal can
	// be audited long after the fact.
	Reason json.RawMessage

	CreatedAt time.Time
}
