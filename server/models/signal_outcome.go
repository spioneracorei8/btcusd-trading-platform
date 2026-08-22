package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
)

// SignalOutcome is what became of one signal.
//
// # Why this is tracked at all
//
// A backtest reporting a 42.86% win rate and live signals coming in at 25%
// mean something is wrong in the pipeline — look-ahead, fill assumptions, a
// cost model that flatters. The same backtest and live at 41% mean the
// pipeline is sound and the edge is simply thin. Those two conclusions demand
// opposite responses, and only tracked outcomes tell them apart.
//
// It cannot be added later: the comparison has no history to draw on unless
// something was recording from the first signal.
type SignalOutcome struct {
	SignalId uuid.UUID
	Status   constants.OutcomeStatus

	// ResolvedAt and ResolvedPrice are unset while the status is open.
	ResolvedAt    *time.Time
	ResolvedPrice decimal.NullDecimal

	// MAE is the worst the position went against itself before resolving,
	// and MFE the best it went in favour, both as a distance in price from
	// the entry.
	//
	// # Why these are not decoration
	//
	// If MAE is routinely close to the stop on trades that eventually win,
	// the stop is barely surviving and a slightly worse fill would flip the
	// result. That is invisible in a win rate and decisive in practice.
	MAE decimal.NullDecimal
	MFE decimal.NullDecimal

	// BarsHeld counts the bars from the entry to the resolution, inclusive of
	// the bar the entry filled on — which can also be the bar that resolved
	// it.
	BarsHeld int32

	// BacktestWouldHave is what the engine's own accounting makes of this
	// trade: the entry it assumes, the exit, and the return after modelled
	// cost. Recorded per signal because a parameter change between two
	// signals otherwise leaves two incomparable groups looking alike.
	BacktestWouldHave json.RawMessage

	// DivergenceNote is filled when the live reading and the backtest's
	// disagree. Empty is the expected case and the interesting one is not.
	DivergenceNote string

	CreatedAt time.Time
	UpdatedAt time.Time
}

// Resolved reports whether the signal is finished with.
func (o SignalOutcome) Resolved() bool { return o.Status.Resolved() }
