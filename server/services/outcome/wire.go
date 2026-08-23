package outcome

import (
	"encoding/json"
	"time"

	"github.com/spioneracorei8/btcusd-trading-platform/server/helper"
)

// OutcomeResponse is how a resolved signal appears on the wire.
//
// Shared by GET /api/v1/outcomes and the websocket's outcomes topic.
type OutcomeResponse struct {
	SignalId   string    `json:"signal_id"`
	SignalTime time.Time `json:"signal_time"`
	Direction  string    `json:"direction"`
	Timeframe  string    `json:"timeframe"`

	Strategy        string `json:"strategy_name"`
	StrategyVersion string `json:"strategy_version"`

	Status   string `json:"status"`
	BarsHeld int32  `json:"bars_held"`

	// Measurable is false for an invalidated outcome: its window had missing
	// data, so whether it would have won is not knowable and it is excluded
	// from every statistic. Carried explicitly so a client is not left to
	// infer it from the status string.
	Measurable bool `json:"measurable"`

	ResolvedAt    *time.Time `json:"resolved_at"`
	SignalPrice   *string    `json:"signal_price"`
	EntryPrice    *string    `json:"entry_price"`
	ResolvedPrice *string    `json:"resolved_price"`

	// MAE and MFE are distances in price from the entry. An MAE routinely
	// close to the stop on trades that eventually win means the stop is
	// barely surviving, which is invisible in a win rate.
	MAE *string `json:"mae"`
	MFE *string `json:"mfe"`

	NetReturnPct *string `json:"net_return_pct"`

	// DivergenceNote is set when the resolution rested on an assumption
	// rather than on the data.
	DivergenceNote string `json:"divergence_note,omitempty"`
}

// ToOutcomeResponse renders one resolved signal.
func ToOutcomeResponse(row Resolved) OutcomeResponse {
	o, s := row.Outcome, row.Signal

	return OutcomeResponse{
		SignalId:        o.SignalId.String(),
		SignalTime:      s.SignalTime.UTC(),
		Direction:       s.Direction.String(),
		Timeframe:       s.Timeframe.String(),
		Strategy:        s.StrategyName,
		StrategyVersion: s.StrategyVersion,
		Status:          o.Status.String(),
		BarsHeld:        o.BarsHeld,
		Measurable:      o.Status.Measurable(),
		ResolvedAt:      o.ResolvedAt,
		SignalPrice:     helper.NullableDecimal(s.SignalPrice),
		EntryPrice:      helper.NullableDecimal(s.EntryPrice),
		ResolvedPrice:   helper.NullableDecimal(o.ResolvedPrice),
		MAE:             helper.NullableDecimal(o.MAE),
		MFE:             helper.NullableDecimal(o.MFE),
		NetReturnPct:    netReturnOf(o.BacktestWouldHave),
		DivergenceNote:  o.DivergenceNote,
	}
}

// netReturnOf reads the net return out of the stored accounting.
//
// The number is already there and already net of costs: recomputing it here
// would be a second implementation of the cost model, which is the one thing
// that must not have two.
func netReturnOf(accounting []byte) *string {
	if len(accounting) == 0 {
		return nil
	}

	var doc struct {
		NetReturnPct string `json:"net_return_pct"`
	}
	if err := json.Unmarshal(accounting, &doc); err != nil || doc.NetReturnPct == "" {
		return nil
	}
	return &doc.NetReturnPct
}
