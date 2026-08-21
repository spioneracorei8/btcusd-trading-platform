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
	"github.com/spioneracorei8/btcusd-trading-platform/server/helper"
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

	// FeeMakerPct is what a fill that rested on the book pays instead.
	//
	// Zero means "the same as taker", which is what every run produced before
	// maker fees existed. That reading is deliberate: a zero-value Costs must
	// keep charging the full rate, because the alternative — a zero-value
	// struct that silently makes trading free — is the flattering default
	// CLAUDE.md §3.4 exists to prevent.
	FeeMakerPct decimal.Decimal

	// SlippageTicks is how many ticks a fill is assumed to slip, always
	// against the trade. TickSize is what one tick is worth.
	//
	// It applies to market fills only. A resting order does not cross the
	// spread, which is the entire reason to use one. Slippage is charged on
	// top of the spread under either cost model: the spread is what crossing
	// costs, slippage is the book moving while you cross, and they are
	// different things.
	SlippageTicks int
	TickSize      decimal.Decimal

	// Model selects percentage-of-notional or spread-in-points. Its zero
	// value is percentage, which is what every earlier evaluation used.
	Model constants.CostModel

	// SpreadPoints x PointValue is the quoted spread in price terms: 2500
	// points at 0.01 gives 25.00 of price.
	SpreadPoints int
	PointValue   decimal.Decimal

	// ContractSize is units per lot, MinLot the smallest tradeable size and
	// LotStep the increment between sizes. They describe the venue, not the
	// strategy, and apply only under the spread model.
	ContractSize decimal.Decimal
	MinLot       decimal.Decimal
	LotStep      decimal.Decimal

	// CommissionPerLot is charged per lot per side. Zero on a Standard
	// account, where the cost is entirely in the spread.
	CommissionPerLot decimal.Decimal
}

// CostModel returns the model in force, defaulting to percentage.
func (c Costs) CostModel() constants.CostModel {
	if c.Model == "" {
		return constants.CostModelPercentage
	}
	return c.Model
}

// SpreadPrice is the quoted spread expressed in price.
func (c Costs) SpreadPrice() decimal.Decimal {
	return c.PointValue.Mul(decimal.NewFromInt(int64(c.SpreadPoints)))
}

// HalfSpread is what one side of a round trip pays in price terms.
//
// # Why half, and not a full crossing on each side
//
// A quoted spread of 25 is the distance between bid and ask. Buying at the ask
// and later selling at the bid gives up that distance once across the round
// trip, not twice: 25 x 0.01 BTC = 0.25 USD at the minimum lot, which is the
// figure the venue's own arithmetic gives. Charging a full spread per side
// would double every cost in the model and make a strategy look half as
// viable as it is — an error in the safe direction, and still an error.
//
// Modelled as half on each side of the mid so that entry and exit are
// symmetric, and so a long and a short of the same size cost the same.
func (c Costs) HalfSpread() decimal.Decimal {
	return c.SpreadPrice().Div(decimal.NewFromInt(2))
}

// LotConstrained reports whether position sizes must land on the venue's lot
// grid.
//
// Tied to the spread model rather than to MinLot being set, because the
// constraint arrives with the venue. A percentage run keeps the continuous
// sizing every earlier evaluation used, which is what lets those results stay
// comparable.
func (c Costs) LotConstrained() bool {
	return c.CostModel() == constants.CostModelSpread &&
		c.ContractSize.IsPositive() && c.LotStep.IsPositive()
}

// MakerFeePct is the resting-order rate, falling back to the taker rate.
//
// The fallback is what makes an execution model that was never configured
// cost the same as it always did.
func (c Costs) MakerFeePct() decimal.Decimal {
	if c.FeeMakerPct.IsZero() {
		return c.FeeTakerPct
	}
	return c.FeeMakerPct
}

// Exits are the exit mechanisms the engine enforces, independently of what a
// strategy asks for.
//
// # Why these live on the engine rather than in each strategy
//
// A trailing stop and a holding-time limit are not rules about *when to enter*
// — they are what happens to a position after it exists, which is the engine's
// business. Four copies in four strategy configurations would drift, and would
// make "the same strategy with and without a trail" impossible to express
// without editing the strategy.
//
// Every field is zero by default, and zero disables it. Every evaluation
// before this had only a fixed stop and a fixed target, so a run that mentions
// none of this behaves exactly as it always did.
type Exits struct {
	// TrailingATRMult is how far the trailing stop sits from the running
	// extreme, in ATR. Zero disables trailing entirely.
	TrailingATRMult float64 `param:"trailing_atr_mult,step=0.25"`

	// TrailingActivateATR is how much profit is required, in ATR, before the
	// trail arms. Until then the fixed stop applies unchanged.
	//
	// Arming immediately would convert every entry into a trailing exit at
	// roughly the noise level, which is a different strategy rather than a
	// modification of this one.
	TrailingActivateATR float64 `param:"trailing_activate_atr,step=0.25"`
}

// Trailing reports whether a trailing stop is configured.
func (e Exits) Trailing() bool { return e.TrailingATRMult > 0 }

// Validate rejects an exit configuration that cannot mean anything.
func (e Exits) Validate() error {
	if e.TrailingATRMult < 0 {
		return fmt.Errorf("backtest: trailing distance %v ATR is negative", e.TrailingATRMult)
	}
	if e.TrailingActivateATR < 0 {
		return fmt.Errorf("backtest: trailing activation %v ATR is negative", e.TrailingActivateATR)
	}
	// A distance that only matters once trailing is on. Stating one without
	// the other is a configuration that reads as though it does something.
	if e.TrailingActivateATR > 0 && !e.Trailing() {
		return fmt.Errorf(
			"backtest: trailing_activate_atr is %v but trailing_atr_mult is zero, so nothing trails",
			e.TrailingActivateATR)
	}
	return nil
}

// Execution is how orders reach the book.
//
// # Why this is not part of Costs
//
// Costs answers "what does a fill pay". Execution answers "does the fill
// happen at all", and the second question is the one that makes maker fees
// honest: a limit order pays less and sometimes does not trade. Folding them
// together would also make the cost sweep ambiguous, since the sweep scales
// what things cost and must not quietly change how they are placed.
//
// # Why the zero value is market
//
// Unlike Sizing, which has no natural default and therefore demands one be
// stated, market is both what every completed evaluation used and the
// conservative side of this choice: it always fills and pays the higher fee.
// An unstated execution model cannot flatter a result. Defaulting to limit
// would be the dangerous direction, and that is the one this refuses.
type Execution struct {
	EntryOrderType constants.OrderType
	ExitOrderType  constants.OrderType

	// LimitTimeoutBars is how many bars an unfilled limit order rests. Zero
	// is read as one, so a limit configuration is never a no-op.
	LimitTimeoutBars int
}

// Entry returns the entry order type, defaulting to market.
func (e Execution) Entry() constants.OrderType {
	if e.EntryOrderType == "" {
		return constants.OrderTypeMarket
	}
	return e.EntryOrderType
}

// Exit returns the exit order type, defaulting to market.
func (e Execution) Exit() constants.OrderType {
	if e.ExitOrderType == "" {
		return constants.OrderTypeMarket
	}
	return e.ExitOrderType
}

// Timeout returns how many bars a limit order rests, at least one.
func (e Execution) Timeout() int {
	if e.LimitTimeoutBars < 1 {
		return 1
	}
	return e.LimitTimeoutBars
}

// Validate rejects an execution model that could not be simulated.
func (e Execution) Validate() error {
	if e.EntryOrderType != "" && !e.EntryOrderType.Valid() {
		return fmt.Errorf("backtest: %q is not an entry order type", e.EntryOrderType)
	}
	if e.ExitOrderType != "" && !e.ExitOrderType.Valid() {
		return fmt.Errorf("backtest: %q is not an exit order type", e.ExitOrderType)
	}
	if e.LimitTimeoutBars < 0 {
		return fmt.Errorf("backtest: a limit order cannot rest for %d bars", e.LimitTimeoutBars)
	}
	return nil
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

	// MaxLeverage is how much notional the account may hold per unit of
	// equity. Zero means 1, which is a cash account.
	//
	// # Why this exists, and what went wrong without it
	//
	// Position size was capped at equity/price — the most BTC a spot account
	// could pay for. On a margin venue that is the wrong constraint: a CFD
	// account posts margin, not notional, and the engine's own accounting
	// already works that way (equity moves by realised P&L and costs, and
	// never has the notional deducted).
	//
	// With a 100 USD balance at 27,000 the cap was 0.0037 BTC, below the 0.01
	// lot minimum, so *every* entry was refused — and because the cap bound
	// before the risk arithmetic could matter, 1% risk and 20% risk produced
	// byte-identical runs. The sizing rule was reporting a number it never
	// used.
	//
	// One is still the default: an unstated leverage must not silently make
	// positions larger, and every evaluation before this was a cash account.
	MaxLeverage decimal.Decimal
}

// Leverage is MaxLeverage with its zero value read as a cash account.
func (s Sizing) Leverage() decimal.Decimal {
	if !s.MaxLeverage.IsPositive() {
		return decimal.NewFromInt(1)
	}
	return s.MaxLeverage
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
	if s.MaxLeverage.IsNegative() {
		return fmt.Errorf("backtest: leverage %s is negative", s.MaxLeverage)
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

	// Execution is how orders reach the book. Its zero value is market on both
	// sides, which is what every completed evaluation used.
	Execution Execution

	// Exits are the engine-enforced exit mechanisms. Their zero value is the
	// fixed stop and fixed target every earlier evaluation used.
	Exits Exits

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

	// StrategyAligner supplies higher-timeframe readings to the strategy
	// itself, and is non-nil only for a strategy.MultiTimeframe.
	//
	// # Why this is not TrendAligner
	//
	// They answer different questions and can be configured differently: the
	// filter watches what ADR 0018's table says to watch from this base, while
	// a multi-timeframe strategy names its own contributors. Sharing one
	// aligner would mean whichever was configured first silently decided the
	// other's inputs — and a run with --no-trend-filter, which is the normal
	// way such a strategy is run, would leave the strategy with nothing.
	//
	// They are also advanced at different points in the bar: this one before
	// the strategy decides, the filter's after, because a veto is applied to a
	// decision that has already been made.
	StrategyAligner trend.Aligner

	// StrategyParams and FilterParams are the parameters that differ from
	// their documented defaults.
	//
	// Recorded on the run rather than left in the CLI because a run whose
	// parameters are not in its own report is not reproducible, and every
	// evaluation before this one used defaults — which made "defaults" a safe
	// assumption exactly until it stopped being one.
	StrategyParams []helper.ParamChange
	FilterParams   []helper.ParamChange

	// TrendUnavailable explains why no filter is attached, when the reason is
	// that none could be built rather than that the operator declined one.
	//
	// The two read identically in a header otherwise, and they are not the
	// same finding: one is a choice about the experiment, the other is a limit
	// of the collected data.
	TrendUnavailable string

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

	// ExitTrailingStop is a stop that had been moved in the position's favour
	// before it triggered.
	//
	// Distinct from ExitStop so the two can be counted apart: whether the
	// trail is doing anything, or merely adding a code path, is answerable
	// only if its exits are separable from the fixed stop's.
	ExitTrailingStop ExitReason = "trailing_stop"

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

	// StopPrice is the protective level the position was opened with, or zero
	// when it had none. Recorded so the report can state what each trade
	// risked rather than only what it made: on a small balance the intended
	// risk in currency is what decides whether a losing streak is survivable.
	StopPrice decimal.Decimal

	// EquityAtEntry is what the account was worth the instant before this
	// position opened.
	//
	// It is what the trade's risk is a share *of*. Measuring every trade
	// against the run's starting balance instead would be wrong the moment
	// equity compounds: a trade risking 1% of a grown account reports as
	// hundreds of percent "of balance", which is alarming, meaningless, and
	// not what the sizing rule did.
	EquityAtEntry decimal.Decimal

	// EntryMaker and ExitMaker record how each side of the round trip filled.
	//
	// They are per-trade rather than per-run because a run can be
	// configured with a limit exit and still leave through a stop, which is
	// always a market order. Reading the configuration instead of the trade
	// would report a maker fee that was never paid.
	EntryMaker bool
	ExitMaker  bool

	// EntryATR is the base-timeframe ATR at the close the entry was decided
	// on — the bar before the fill, which is the last one the strategy saw.
	//
	// It is recorded so a report can group trades by the conditions they were
	// taken in. Grouping by anything measured after the exit describes how the
	// trade turned out, not what it faced: sorting losses into a bucket and
	// then reporting that the bucket lost is circular. This is the volatility
	// the strategy actually conditioned on, and it is knowable at entry.
	//
	// float64 rather than decimal.Decimal because it is an indicator value,
	// not money — CLAUDE.md §4 draws the line there.
	EntryATR float64

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

	// MakerEntries and the three counters beside it break the fills down by
	// how they reached the book. They sum to the number of trades on each
	// side, which is what makes an unexpected taker fill findable.
	MakerEntries, TakerEntries int64
	MakerExits, TakerExits     int64

	// EntriesRequested is how many entry intents the engine acted on, filled
	// or not, and LimitOrdersExpired how many of those never filled.
	//
	// The second is the number to watch under a limit entry model. If a large
	// share of signals never became trades, the surviving sample is a filtered
	// subset of the strategy's intent, and its statistics describe something
	// other than the strategy as written.
	EntriesRequested   int64
	LimitOrdersExpired int64

	// TrailAmbiguousBars counts bars where the trailing stop would have both
	// extended and triggered, and the trigger was assumed.
	//
	// Reported beside the stop-before-target count because it is the same
	// class of thing: a simplification the result rests on, which the reader
	// has to be able to weigh.
	TrailAmbiguousBars int64

	// EntriesBelowMinLot counts entries the venue could not have taken: the
	// size the strategy asked for was under the minimum lot, so the trade did
	// not happen rather than happening larger.
	//
	// On a small balance this can be most of the signals, and it is a fact
	// about the account rather than about the strategy.
	EntriesBelowMinLot int64

	// EntriesRefusedAfterCap is how many of those refusals were of a position
	// the risk rule had *already* shrunk to fit the account's notional limit.
	//
	// It separates two findings that look identical in a zero-trade run: a
	// strategy asking for tiny positions, and an account too small to hold the
	// positions the strategy asked for. Only the second is fixed by a setting.
	EntriesRefusedAfterCap int64

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

	// TrendUnavailable explains an absent filter that was wanted but could not
	// be built, as distinct from one the operator turned off.
	TrendUnavailable string

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
