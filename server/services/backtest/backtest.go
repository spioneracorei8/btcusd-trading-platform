// Package backtest declares the contract of the measuring instrument.
//
// Everything built after this phase is judged by numbers this package
// produces, which makes a flattering bug here worse than a crash: a crash is
// found in an hour, an optimistic backtest is acted on for months. Every
// simplification the engine makes is therefore reported rather than hidden —
// the report carries the costs applied, the bars it refused to evaluate, and
// how often an ambiguous bar was resolved by assumption.
package backtest

import (
	"context"
	"fmt"
	"time"

	"github.com/shopspring/decimal"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/strategy"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/trend"
)

// GapPolicy decides what a run does when the requested range has holes.
//
// There is deliberately no policy that produces a clean-looking report over
// dirty data: ignore runs, but everything it emits is stamped incomplete.
type GapPolicy string

// The gap policies, in order of how much they let you get away with.
const (
	// GapHalt refuses to run. It is the default because the alternative —
	// quietly returning a number computed over missing bars — is the failure
	// mode this whole gate exists to prevent.
	GapHalt GapPolicy = "halt"

	// GapSkip runs but excludes the affected regions: no bar inside a gap is
	// evaluated, and a position open when a gap begins is force-closed at the
	// last known close.
	GapSkip GapPolicy = "skip"

	// GapIgnore runs straight through. Every report from such a run carries
	// DATA_INCOMPLETE.
	GapIgnore GapPolicy = "ignore"
)

// Valid reports whether p is a known policy.
func (p GapPolicy) Valid() bool {
	switch p {
	case GapHalt, GapSkip, GapIgnore:
		return true
	default:
		return false
	}
}

// String returns the flag representation of the policy.
func (p GapPolicy) String() string { return string(p) }

// ParseGapPolicy converts s into a GapPolicy, rejecting unknown values.
func ParseGapPolicy(s string) (GapPolicy, error) {
	p := GapPolicy(s)
	if !p.Valid() {
		return "", fmt.Errorf("unknown gap policy %q (want %q, %q or %q)", s, GapHalt, GapSkip, GapIgnore)
	}
	return p, nil
}

// Costs are the trading costs a run applies, taken from configuration.
//
// They are not optional and there is no flag that disables them. At 1m-5m
// frequency a round trip costs roughly 0.1% before slippage, which is a third
// of the edge of a strategy targeting 0.3% a trade; a backtest that omitted
// them would be describing a market that does not exist.
type Costs struct {
	// FeeTakerPct is the taker fee in percent, e.g. 0.05 means 0.05%.
	FeeTakerPct decimal.Decimal

	// SlippageTicks is how many ticks a fill is assumed to slip, always
	// against the trade. TickSize is what one tick is worth.
	SlippageTicks int
	TickSize      decimal.Decimal
}

// SlippageAmount is the price offset one fill is assumed to suffer.
func (c Costs) SlippageAmount() decimal.Decimal {
	return c.TickSize.Mul(decimal.NewFromInt(int64(c.SlippageTicks)))
}

// SizingMode decides how much a position commits.
type SizingMode string

// The sizing modes.
const (
	// SizingAllIn commits the whole account, fee included. It is what phase 04
	// measured with, and it is the only mode available to a strategy that sets
	// no stop — buy-and-hold has no stop distance to size against.
	SizingAllIn SizingMode = "all_in"

	// SizingFixedFractional risks a fixed share of equity per trade, with the
	// size derived from the distance to the stop. It is the mode real
	// strategies use: a fixed notional makes every trade's risk depend on how
	// far away the stop happens to be, which is the opposite of controlling it.
	SizingFixedFractional SizingMode = "fixed_fractional"
)

// Valid reports whether m is a known sizing mode.
func (m SizingMode) Valid() bool {
	switch m {
	case SizingAllIn, SizingFixedFractional:
		return true
	default:
		return false
	}
}

// String returns the wire representation of the sizing mode.
func (m SizingMode) String() string { return string(m) }

// Sizing is how much of the account a position commits.
//
// It is part of RunParams rather than of a strategy because it changes every
// reported number, and because live and backtest must size identically — the
// same code, not two implementations that agree today.
type Sizing struct {
	Mode SizingMode

	// RiskPct is the share of equity risked per trade under
	// SizingFixedFractional, in percent: 1 means 1%.
	RiskPct decimal.Decimal
}

// DefaultSizing risks 1% of equity per trade against the stop distance.
func DefaultSizing() Sizing {
	return Sizing{Mode: SizingFixedFractional, RiskPct: decimal.NewFromInt(1)}
}

// AllInSizing is phase 04's behaviour, kept for strategies with no stop.
func AllInSizing() Sizing {
	return Sizing{Mode: SizingAllIn}
}

// Validate rejects a sizing that could not produce a position.
func (s Sizing) Validate() error {
	if !s.Mode.Valid() {
		return fmt.Errorf("backtest: %q is not a sizing mode", s.Mode)
	}
	if s.Mode == SizingFixedFractional {
		if !s.RiskPct.IsPositive() {
			return fmt.Errorf("backtest: risk %s%% per trade is not positive", s.RiskPct)
		}
		if s.RiskPct.GreaterThan(decimal.NewFromInt(100)) {
			return fmt.Errorf("backtest: risk %s%% per trade exceeds the whole account", s.RiskPct)
		}
	}
	return nil
}

// RunParams is one backtest run.
type RunParams struct {
	Symbol     string
	MarketType constants.MarketType
	Timeframe  constants.Timeframe

	// From and To bound the range to be scored, inclusive on open_time.
	From time.Time
	To   time.Time

	// InitialEquity is the starting balance in quote currency. Every trade
	// commits all of it, so returns compound and the equity curve is directly
	// comparable with buy-and-hold.
	InitialEquity decimal.Decimal

	Costs     Costs
	GapPolicy GapPolicy

	// Sizing decides how much each position commits. The zero value is
	// rejected; callers state it, because it changes every reported number.
	Sizing Sizing

	// Strategy is the code under measurement.
	Strategy strategy.Strategy

	// TrendFilter vetoes entries the higher timeframes do not permit. Nil
	// runs unfiltered, which is the control the filtered run is compared
	// against — a filter whose benefit is never measured is a decoration.
	TrendFilter trend.Filter

	// TrendAligner supplies the higher-timeframe readings. It must be
	// non-nil whenever TrendFilter is, and it is what enforces that no
	// contribution comes from a bar that had not closed.
	TrendAligner trend.Aligner

	// TrendConfig is recorded in the report. A filter version says which code
	// scored the run; the configuration says what it scored with, and two runs
	// of the same version under different weights are not comparable.
	TrendConfig trend.Config
}

// Filtered reports whether this run has a trend filter attached.
func (p RunParams) Filtered() bool { return p.TrendFilter != nil }

// ExitReason records why a position was closed.
type ExitReason string

// The ways a position ends.
const (
	// ExitTarget and ExitStop are level hits. When both were reachable inside
	// one bar the stop is taken, and the trade is flagged ambiguous.
	ExitTarget ExitReason = "target"
	ExitStop   ExitReason = "stop"

	// ExitStrategy is the strategy asking to close.
	ExitStrategy ExitReason = "strategy_exit"

	// ExitGapForced is the engine closing a position because the data ends,
	// a gap begins, or the market became untradeable.
	ExitGapForced ExitReason = "gap_forced"

	// ExitEndOfRun closes whatever is still open when the range ends, so the
	// reported return is realised rather than partly on paper.
	ExitEndOfRun ExitReason = "end_of_run"
)

// String returns the wire representation of the exit reason.
func (r ExitReason) String() string { return string(r) }

// Trade is one completed round trip.
type Trade struct {
	Direction constants.Direction

	EntryTime  time.Time
	EntryPrice decimal.Decimal
	ExitTime   time.Time
	ExitPrice  decimal.Decimal

	// Size is the position size in base currency.
	Size decimal.Decimal

	// GrossPnL is what the market did: the move between the two reference
	// prices, before any cost. Costs is what the round trip paid, and NetPnL
	// is what the account actually kept. All three are reported because a
	// strategy whose costs exceed its gross profit must be impossible to miss.
	//
	// GrossPnL is measured from the unslipped prices on purpose. Charging
	// slippage inside the fill and calling the result "gross" would hide half
	// the cost of trading inside the number that is supposed to be free of it.
	GrossPnL decimal.Decimal
	Costs    decimal.Decimal
	NetPnL   decimal.Decimal

	// Fees and Slippage break Costs down. At scalping frequency they are
	// comparable in size, and a strategy killed by one is fixed differently
	// from a strategy killed by the other.
	Fees     decimal.Decimal
	Slippage decimal.Decimal

	ExitReason ExitReason
	EntryNote  string
	ExitNote   string

	// StopAndTargetBothReachable marks a bar where both levels lay inside the
	// range and the stop was taken by assumption rather than by evidence.
	StopAndTargetBothReachable bool

	// ForcedByGap marks a trade closed because the data stopped being
	// trustworthy rather than because anything about the market changed.
	ForcedByGap bool
}

// EquityPoint is the account value at one evaluated bar. Drawdown is computed
// from this series, not from trade endpoints: a position that went deeply
// against the account before recovering did happen, and a per-trade view
// would report it as if it had not.
type EquityPoint struct {
	OpenTime time.Time
	Equity   decimal.Decimal
}

// Result is everything one run produced, before it is turned into a report.
type Result struct {
	Params RunParams

	// StrategyName and StrategyVersion are captured at run time so a stored
	// report stays attributable after the strategy has moved on.
	StrategyName    string
	StrategyVersion string

	// FirstBar and LastBar are the bars actually evaluated, which can be
	// narrower than the requested range when data is short or skipped.
	FirstBar time.Time
	LastBar  time.Time

	BarsEvaluated int64

	// BarsSkippedWarmup and BarsSkippedGap are counted apart on purpose: the
	// first is the cost of the indicators, the second is missing data, and
	// conflating them hides which one is eating the range.
	BarsSkippedWarmup int64
	BarsSkippedGap    int64

	// AmbiguousBars counts bars where stop and target were both reachable. If
	// it is large the result rests on the stop-first assumption rather than on
	// evidence, and says more about the data's resolution than the strategy.
	AmbiguousBars int64

	// EntriesSizeCapped counts entries where fixed-fractional sizing wanted
	// more than the account could buy and was capped at all-in.
	//
	// This matters more than it looks. A 0.1% stop distance at 1% risk implies
	// ten times the notional, which a spot account cannot hold; capping is the
	// only honest answer, but a run where the cap binds often is not risking
	// what it claims to, and its drawdown will be worse than the risk setting
	// suggests.
	EntriesSizeCapped int64

	// BarsVetoed counts bars where the trend filter blocked an entry the
	// strategy asked for.
	//
	// Both extremes need to be visible. A filter that vetoes almost nothing is
	// not doing anything; one that vetoes almost everything leaves too few
	// surviving trades for the statistics to mean much. Neither is legible
	// from the returns alone.
	BarsVetoed int64

	// BarsFilterNotReady counts bars where the filter had no answer yet —
	// warming up, or recovering from a gap. Reported apart from BarsVetoed
	// because "blocked on purpose" and "could not say" are different findings:
	// a run that is mostly the second one was simply started too early.
	BarsFilterNotReady int64

	// TrendFilterName, TrendFilterVersion and TrendFilterConfig record what
	// filtered the run, empty when nothing did.
	TrendFilterName    string
	TrendFilterVersion string
	TrendFilterConfig  string

	Trades []Trade
	Equity []EquityPoint

	// UnfilledGaps are the gaps found in range, whatever the policy did about
	// them. They are reported even by a halted run.
	UnfilledGaps []models.DataGap

	// UntradeableWindows are the known exchange outages intersecting the
	// range. During one, no order could have been placed at all.
	UntradeableWindows []UntradeableWindow

	// DataIncomplete stamps a run that was allowed to proceed over holes.
	DataIncomplete bool
}

// UntradeableWindow is a period during which the exchange itself was not
// accepting orders.
//
// This is not the same thing as missing data. A gap means we do not know what
// happened; an outage means nothing could have happened. A backtest that
// trades through one is reporting fills that were impossible, so entries are
// refused and open positions are closed at the last price before the halt.
type UntradeableWindow struct {
	Symbol     string
	MarketType constants.MarketType

	// Start is inclusive and End exclusive, both UTC.
	Start time.Time
	End   time.Time

	Reason string
}

// Covers reports whether t falls inside the window.
func (w UntradeableWindow) Covers(t time.Time) bool {
	return !t.Before(w.Start) && t.Before(w.End)
}

// Overlaps reports whether the window intersects [from, to].
func (w UntradeableWindow) Overlaps(from, to time.Time) bool {
	return w.Start.Before(to) && from.Before(w.End)
}

// BacktestUsecase replays stored candles through a strategy.
type BacktestUsecase interface {
	// Run replays the requested range and returns what happened.
	//
	// It returns constants.ErrDataIncomplete when the range has unfilled gaps
	// and the policy is GapHalt. That is a refusal, not a failure: the
	// returned Result still carries the gaps, so the caller can print them.
	Run(ctx context.Context, params RunParams) (Result, error)
}
