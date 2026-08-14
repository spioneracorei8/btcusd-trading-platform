package signal

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
)

// Reason is what a signal's jsonb column holds.
//
// # Why the whole snapshot and not a sentence
//
// It cannot be reconstructed after the fact. Indicators are never persisted
// (ADR: services/indicator), so six weeks after a surprising alert the values
// behind it are gone unless they were captured with it — recomputing them
// would need the exact warm-up state the live process had, which nothing
// stores. A sentence describing the decision is not enough to check it.
//
// # Determinism
//
// The struct has no map and no wall-clock field, for the same reason the
// backtest report has neither: two identical decisions must serialise
// identically, or a duplicate cannot be recognised as one.
type Reason struct {
	// Trigger is the strategy's own words for what fired. It is the first
	// thing a person reads, so it goes first.
	Trigger string `json:"trigger"`

	// Bar identifies the candle the decision was taken on.
	Bar BarReason `json:"bar"`

	// Indicators are the base-timeframe values at that close.
	Indicators IndicatorReason `json:"indicators"`

	// Trend is the multi-timeframe filter's verdict, absent when the run was
	// unfiltered. A signal that a filter permitted and one that had no filter
	// at all are different events.
	Trend *TrendReason `json:"trend,omitempty"`

	// Levels are the advisory stop and target. This system never places
	// orders; these are numbers for the owner to act on or ignore.
	Levels LevelReason `json:"levels"`
}

// BarReason identifies the candle behind a signal.
type BarReason struct {
	OpenTime  string `json:"open_time"`
	CloseTime string `json:"close_time"`
	Open      string `json:"open"`
	High      string `json:"high"`
	Low       string `json:"low"`
	Close     string `json:"close"`
}

// IndicatorReason is the base-timeframe snapshot.
type IndicatorReason struct {
	EMA  float64 `json:"ema"`
	RSI  float64 `json:"rsi"`
	ATR  float64 `json:"atr"`
	VWAP float64 `json:"vwap"`
}

// TrendReason is the filter's verdict at the same instant.
type TrendReason struct {
	Filter     string            `json:"filter"`
	Version    string            `json:"version"`
	Bias       string            `json:"bias"`
	Confidence float64           `json:"confidence"`
	Ready      bool              `json:"ready"`
	PerTF      []TimeframeReason `json:"per_timeframe"`
}

// TimeframeReason is one contributing timeframe's reading.
//
// CloseTime is included because it is the evidence that the reading came from
// a bar that had closed. Without it a stored signal cannot be checked for
// cross-timeframe look-ahead after the fact, which is when it usually matters.
type TimeframeReason struct {
	Timeframe string  `json:"timeframe"`
	Score     float64 `json:"score"`
	Weight    float64 `json:"weight"`
	CloseTime string  `json:"close_time"`
	Ready     bool    `json:"ready"`
}

// LevelReason holds the advisory prices.
type LevelReason struct {
	Entry  string `json:"entry"`
	Stop   string `json:"stop"`
	Target string `json:"target"`
}

// BuildReason assembles the audit record for one decision.
func BuildReason(
	trigger string,
	bar models.Candle,
	indicators models.IndicatorSnapshot,
	entry, stop, target string,
) Reason {
	return Reason{
		Trigger: trigger,
		Bar: BarReason{
			OpenTime:  bar.OpenTime.UTC().Format(time.RFC3339),
			CloseTime: bar.CloseTime.UTC().Format(time.RFC3339),
			Open:      bar.Open.String(),
			High:      bar.High.String(),
			Low:       bar.Low.String(),
			Close:     bar.Close.String(),
		},
		Indicators: IndicatorReason{
			EMA: indicators.EMA, RSI: indicators.RSI,
			ATR: indicators.ATR, VWAP: indicators.VWAP,
		},
		Levels: LevelReason{Entry: entry, Stop: stop, Target: target},
	}
}

// Encode renders the reason for the jsonb column.
//
// NaN and infinity are rejected rather than coerced: encoding/json refuses
// them, and silently substituting zero would store a plausible-looking value
// that never existed. A signal whose indicators could not be represented is a
// signal that should not have been emitted.
func (r Reason) Encode() (json.RawMessage, error) {
	for name, value := range map[string]float64{
		"ema": r.Indicators.EMA, "rsi": r.Indicators.RSI,
		"atr": r.Indicators.ATR, "vwap": r.Indicators.VWAP,
	} {
		if value != value { //nolint:gocritic // NaN is only detectable this way
			return nil, fmt.Errorf("signal reason: %s is NaN; the indicators were not ready", name)
		}
	}

	encoded, err := json.Marshal(r)
	if err != nil {
		return nil, fmt.Errorf("encode signal reason: %w", err)
	}
	return encoded, nil
}

// ValidateForBar rejects a signal that does not belong to a closed candle.
//
// CLAUDE.md §3.1 keeps a forming bar out of everything downstream. A signal is
// downstream: emitting one from a bar that has not closed would alert the
// owner about a price that can still change, and the alert cannot be recalled.
func ValidateForBar(bar models.Candle, signalTime time.Time) error {
	if !bar.IsClosed {
		return fmt.Errorf("%w: signal at %s",
			constants.ErrUnclosedCandle, bar.OpenTime.UTC().Format(time.RFC3339))
	}
	if !signalTime.Equal(bar.CloseTime.UTC()) {
		return fmt.Errorf(
			"signal time %s is not the candle's close time %s; the two must match or "+
				"live and backtest signals cannot be compared",
			signalTime.UTC().Format(time.RFC3339), bar.CloseTime.UTC().Format(time.RFC3339))
	}
	return nil
}
