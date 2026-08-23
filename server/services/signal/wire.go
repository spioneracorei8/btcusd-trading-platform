package signal

import (
	"encoding/json"
	"time"

	"github.com/spioneracorei8/btcusd-trading-platform/server/helper"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
)

// SignalResponse is how a signal appears on the wire.
//
// Shared by GET /api/v1/signals, GET /api/v1/signals/{id} and the websocket's
// signals topic, so a client parses one shape however the signal reached it.
type SignalResponse struct {
	Id         string    `json:"id"`
	Symbol     string    `json:"symbol"`
	MarketType string    `json:"market_type"`
	Timeframe  string    `json:"timeframe"`
	SignalTime time.Time `json:"signal_time"`
	Direction  string    `json:"direction"`

	// SignalPrice is the close the strategy decided on and EntryPrice what a
	// position would have opened at — the next bar's open plus slippage,
	// which is not knowable when the signal is recorded and is null until
	// that bar closes. They are different numbers and conflating them would
	// put a systematic difference into every comparison.
	SignalPrice *string `json:"signal_price"`
	EntryPrice  *string `json:"entry_price"`
	StopLoss    *string `json:"stop_loss"`
	TakeProfit  *string `json:"take_profit"`

	StrategyName    string `json:"strategy_name"`
	StrategyVersion string `json:"strategy_version"`

	CreatedAt time.Time `json:"created_at"`

	// Reason is present on the single-signal endpoint only. It is large and a
	// list of fifty would be mostly reason.
	Reason json.RawMessage `json:"reason,omitempty"`
}

// ToSignalResponse renders one signal, with or without its reason.
func ToSignalResponse(s models.Signal, withReason bool) SignalResponse {
	out := SignalResponse{
		Id:          s.Id.String(),
		Symbol:      s.Symbol,
		MarketType:  s.MarketType.String(),
		Timeframe:   s.Timeframe.String(),
		SignalTime:  s.SignalTime.UTC(),
		Direction:   s.Direction.String(),
		SignalPrice: helper.NullableDecimal(s.SignalPrice),
		EntryPrice:  helper.NullableDecimal(s.EntryPrice),
		StopLoss:    helper.NullableDecimal(s.StopLoss),
		TakeProfit:  helper.NullableDecimal(s.TakeProfit),

		StrategyName:    s.StrategyName,
		StrategyVersion: s.StrategyVersion,
		CreatedAt:       s.CreatedAt.UTC(),
	}
	if withReason && len(s.Reason) > 0 {
		out.Reason = json.RawMessage(s.Reason)
	}
	return out
}
