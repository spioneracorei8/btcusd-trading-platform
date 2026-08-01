package domain

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// Direction is the side a signal points to. The system never places orders;
// a direction is advice for the owner, nothing more.
type Direction string

// Supported signal directions.
const (
	DirectionLong  Direction = "long"
	DirectionShort Direction = "short"
	DirectionFlat  Direction = "flat"
)

// Valid reports whether d is a known direction.
func (d Direction) Valid() bool {
	switch d {
	case DirectionLong, DirectionShort, DirectionFlat:
		return true
	default:
		return false
	}
}

// String returns the wire/database representation of the direction.
func (d Direction) String() string { return string(d) }

// ParseDirection converts s into a Direction, rejecting unknown values.
func ParseDirection(s string) (Direction, error) {
	d := Direction(s)
	if !d.Valid() {
		return "", fmt.Errorf("unknown direction %q", s)
	}
	return d, nil
}

// Signal is one strategy decision taken at the close of a candle.
type Signal struct {
	ID         uuid.UUID
	Symbol     string
	MarketType MarketType
	Timeframe  Timeframe

	// SignalTime is the close time of the candle that produced the signal,
	// not the time the row was written. Using the bar close keeps live and
	// backtest runs comparable.
	SignalTime time.Time

	Direction Direction

	// Strength is a 0-100 confidence score, numeric(5,2) in the database.
	Strength decimal.Decimal

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
