// Package strategy declares what a trading strategy is allowed to see and
// allowed to say. There are deliberately no implementations here: the
// measuring instrument is built before anything to measure, so that a
// strategy can be judged rather than believed.
//
// # Nothing here can place an order
//
// An Intent is a request the engine may refuse, not a fill. The whole output
// of this system is a signal, a reason and a notification; no type in this
// package, and no type it references, can reach an exchange.
package strategy

import (
	"time"

	"github.com/shopspring/decimal"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
)

// Strategy sees one bar at a time and may return intents.
//
// The same implementation runs in a backtest and, from phase 06, live. There
// is no mode flag and no way to ask which one is running: if a strategy could
// tell, the backtest would stop describing what live does and nothing would
// report the divergence.
type Strategy interface {
	// OnBar is called once per closed candle, in chronological order, and
	// exactly once per bar.
	//
	// It takes no context and returns no error on purpose. A strategy is a
	// pure decision over what it is given: nothing to cancel, nothing to fail
	// at, no I/O to be tempted into.
	OnBar(bar BarContext) []Intent

	// WarmupPeriod is how many bars must precede the first OnBar call. The
	// engine waits for the longer of this and the indicator set's own warm-up.
	WarmupPeriod() int

	// Name and Version are recorded in every report. Six weeks after a
	// surprising result, the only useful question is which code produced it.
	Name() string
	Version() string
}

// BarContext is the entire world a strategy may observe at a decision point.
//
// # Why this is a struct of values and not a series
//
// It carries no slice of candles, no index into one, and no reader that could
// fetch another bar. That is the whole design: look-ahead is prevented by
// there being nothing to look ahead through, not by a rule asking nobody to.
// A rule gets broken by accident during a refactor; a field that does not
// exist cannot be dereferenced.
//
// The same reasoning excludes a clock. A strategy that could read wall time
// would behave differently in a replay than it did live, and the difference
// would surface as an unreproducible backtest rather than as an error.
type BarContext struct {
	// Candle is the bar that just closed. Its close is the last price the
	// strategy is entitled to know.
	Candle models.Candle

	// Indicators are the values at this candle's close, already warmed up.
	Indicators models.IndicatorSnapshot

	// Position is what the strategy is holding right now, flat included.
	Position Position
}

// Position is the open position at a decision point, or a flat one.
//
// It is a copy. A strategy mutating it changes nothing: the engine owns
// position state, because a strategy that could rewrite its own entry price
// could report any result it wanted.
type Position struct {
	// Direction is long, short, or flat. Flat means the other fields are
	// zero and must not be read.
	Direction constants.Direction

	// EntryPrice is what the engine actually filled at, inclusive of the
	// costs it charged — not the price that triggered the entry.
	EntryPrice decimal.Decimal

	// EntryTime is the open time of the bar the entry filled on.
	EntryTime time.Time

	// Size is the position size in base currency.
	Size decimal.Decimal

	// Stop and Target are the levels currently attached, zero when unset.
	Stop   decimal.Decimal
	Target decimal.Decimal

	// BarsHeld counts closed bars since entry, so a strategy can express a
	// time-based exit without needing a clock.
	BarsHeld int
}

// IsOpen reports whether a position is held.
func (p Position) IsOpen() bool {
	return p.Direction == constants.DirectionLong || p.Direction == constants.DirectionShort
}

// IntentKind is what a strategy is asking for.
type IntentKind string

// The intents a strategy may express.
const (
	// IntentEnterLong and IntentEnterShort are ignored while a position is
	// already open: one position at a time, no pyramiding.
	IntentEnterLong  IntentKind = "enter_long"
	IntentEnterShort IntentKind = "enter_short"

	// IntentExit closes the open position at the next bar's open.
	IntentExit IntentKind = "exit"

	// IntentSetStop and IntentSetTarget attach a level to the open position.
	// They take effect immediately rather than at the next bar, because they
	// describe a level, not a fill.
	IntentSetStop   IntentKind = "set_stop"
	IntentSetTarget IntentKind = "set_target"
)

// Valid reports whether k is a known intent kind.
func (k IntentKind) Valid() bool {
	switch k {
	case IntentEnterLong, IntentEnterShort, IntentExit, IntentSetStop, IntentSetTarget:
		return true
	default:
		return false
	}
}

// String returns the wire representation of the intent kind.
func (k IntentKind) String() string { return string(k) }

// Intent is what a strategy wants, never what happens.
//
// It carries no price for an entry or an exit and no timestamp. The engine
// decides both: a fill happens at the next bar's open, after costs, and a
// strategy that could name its own fill price could report any result it
// liked. Price is only meaningful for the two level-setting intents, where
// it is a threshold rather than a fill.
type Intent struct {
	Kind IntentKind

	// Price is the level for IntentSetStop and IntentSetTarget, and is
	// ignored for every other kind.
	Price decimal.Decimal

	// Reason is carried into the trade record and, from phase 07, into the
	// notification. A signal without a reason is not actionable.
	Reason string
}

// EnterLong builds a long entry intent.
func EnterLong(reason string) Intent {
	return Intent{Kind: IntentEnterLong, Reason: reason}
}

// EnterShort builds a short entry intent. The engine rejects it outright on a
// spot market, where a short is fiction rather than an unsupported feature.
func EnterShort(reason string) Intent {
	return Intent{Kind: IntentEnterShort, Reason: reason}
}

// Exit builds an exit intent.
func Exit(reason string) Intent {
	return Intent{Kind: IntentExit, Reason: reason}
}

// SetStop builds an intent attaching a stop level to the open position.
func SetStop(price decimal.Decimal, reason string) Intent {
	return Intent{Kind: IntentSetStop, Price: price, Reason: reason}
}

// SetTarget builds an intent attaching a target level to the open position.
func SetTarget(price decimal.Decimal, reason string) Intent {
	return Intent{Kind: IntentSetTarget, Price: price, Reason: reason}
}
